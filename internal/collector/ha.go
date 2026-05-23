package collector

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/influxdata/influxdb-client-go/v2/api/write"
	"go.uber.org/zap"

	"nsx-collector/internal/config"
	influxpkg "nsx-collector/internal/influxdb"
	"nsx-collector/internal/nsx"
	"nsx-collector/internal/telemetry"
)

// haObservedT1 is one entry in the persisted inventory file.
type haObservedT1 struct {
	ID    string `json:"id"`    // T1 logical-router UUID
	Name  string `json:"name"`  // display_name at the time of selection
	Pinned bool  `json:"pinned"` // true when selected via ha_watch.t1_names
}

// haClusterInventory is the persisted state per T0 edge cluster.
type haClusterInventory struct {
	T0ClusterID string         `json:"t0_cluster_id"`   // NSX edge_cluster_id
	T0Name      string         `json:"t0_name"`         // first T0 display name attached to this cluster
	Observed    []haObservedT1 `json:"observed"`        // current watchlist (size <= ha_watch.size)
	// MissCount[T1_ID] = consecutive cycles where the T1 was 404/missing.
	// We only substitute after 2 consecutive misses (configured below).
	MissCount map[string]int `json:"miss_count,omitempty"`
}

// HAInventory is the persisted state for one site (keyed by T0 cluster ID).
type HAInventory struct {
	Site     string                          `json:"site"`
	Updated  time.Time                       `json:"updated"`
	Clusters map[string]*haClusterInventory  `json:"clusters"`
}

const haMissCountThreshold = 2 // after N consecutive misses, the T1 is substituted

// HACollector runs the HA collection cycle for one manager, persisting the
// observed inventory between cycles and computing per-cluster summaries +
// change events when the majority of observed T1s shifts ACTIVE.
type HACollector struct {
	manager  config.Manager
	client   *nsx.Client
	logger   *zap.Logger

	mu       sync.Mutex
	// prevActive[t0_cluster_id][t1_id] = transport_node_id last seen as ACTIVE.
	// Lost on restart by design — first cycle after restart only baselines.
	prevActive map[string]map[string]string
}

// NewHACollector creates a new HA collector for one manager.
func NewHACollector(mgr config.Manager, client *nsx.Client, logger *zap.Logger) *HACollector {
	return &HACollector{
		manager:    mgr,
		client:     client,
		logger:     logger,
		prevActive: make(map[string]map[string]string),
	}
}

// CollectHA runs one HA collection cycle:
//  1. Lists T0s + T1s, groups T1s by their edge_cluster_id.
//  2. Loads/refreshes the inventory of observed T1s per T0 cluster.
//  3. Fetches HA status for each observed T1 (parallelism-limited).
//  4. Computes consensus ACTIVE per cluster and detects majority shifts.
//  5. Returns InfluxDB points (state per T1, summary per cluster, change events).
func (h *HACollector) CollectHA(ctx context.Context) ([]*write.Point, error) {
	site := h.manager.Site
	telemetry.HAPolls.WithLabelValues(site).Inc()

	// 1. Inventory routers and group T1s by edge cluster.
	routers, err := h.client.GetLogicalRouters(ctx)
	if err != nil {
		return nil, fmt.Errorf("ha: list logical routers: %w", err)
	}

	t0Names := map[string]string{}          // edge_cluster_id → T0 display name (first match)
	t1ByCluster := map[string][]nsx.LogicalRouter{}
	for _, r := range routers {
		if r.EdgeClusterID == "" {
			continue
		}
		switch r.RouterType {
		case "TIER0":
			if _, ok := t0Names[r.EdgeClusterID]; !ok {
				t0Names[r.EdgeClusterID] = r.DisplayName
			}
		case "TIER1":
			t1ByCluster[r.EdgeClusterID] = append(t1ByCluster[r.EdgeClusterID], r)
		}
	}
	if len(t0Names) == 0 {
		h.logger.Debug("ha: no T0 edge clusters found")
		return nil, nil
	}

	// 2. Load/refresh inventory file.
	inv, err := h.loadInventory()
	if err != nil {
		h.logger.Warn("ha: load inventory failed (starting fresh)", zap.Error(err))
		inv = &HAInventory{Site: site, Clusters: map[string]*haClusterInventory{}}
	}
	if inv.Clusters == nil {
		inv.Clusters = map[string]*haClusterInventory{}
	}

	// Ensure each T0 cluster has a watchlist of the target size, healing as needed.
	for clusterID, t0Name := range t0Names {
		ci := inv.Clusters[clusterID]
		if ci == nil {
			ci = &haClusterInventory{T0ClusterID: clusterID, T0Name: t0Name}
			inv.Clusters[clusterID] = ci
		}
		// keep T0 name in sync (managers may rename)
		ci.T0Name = t0Name
		h.refreshWatchlist(ci, t1ByCluster[clusterID])
		telemetry.HAObservedT1s.WithLabelValues(site, clusterID).Set(float64(len(ci.Observed)))
	}

	// Drop watchlists for clusters that no longer exist.
	for clusterID := range inv.Clusters {
		if _, ok := t0Names[clusterID]; !ok {
			delete(inv.Clusters, clusterID)
		}
	}

	// 3. Fetch HA status for each observed T1 — parallel with a small semaphore.
	now := time.Now()
	var (
		ptsMu   sync.Mutex
		points  []*write.Point
		wg      sync.WaitGroup
		sem     = make(chan struct{}, 4)
		missedT1 = map[string]map[string]struct{}{} // cluster → T1 ids that 404'd this cycle
		missedMu sync.Mutex
		// activeByCluster[clusterID][t1_id] = transport_node_id currently ACTIVE
		activeByCluster   = map[string]map[string]string{}
		activeByClusterMu sync.Mutex
	)

	for clusterID, ci := range inv.Clusters {
		clusterID := clusterID
		ci := ci
		for _, obs := range ci.Observed {
			obs := obs
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				st, err := h.client.GetLogicalRouterStatus(ctx, obs.ID)
				if err != nil {
					// Treat as "missing" for healing accounting; we don't know
					// for sure it's a 404 (could be 5xx), but the rate-limit
					// path already retries. A persistent failure → substitute.
					missedMu.Lock()
					if missedT1[clusterID] == nil {
						missedT1[clusterID] = map[string]struct{}{}
					}
					missedT1[clusterID][obs.ID] = struct{}{}
					missedMu.Unlock()
					h.logger.Debug("ha: status fetch failed",
						zap.String("t1_id", obs.ID),
						zap.String("t1_name", obs.Name),
						zap.Error(err),
					)
					return
				}

				// Find ACTIVE transport node (if any) and emit per-node state points.
				var activeTN string
				for _, pn := range st.PerNodeStatus {
					ptsMu.Lock()
					points = append(points, influxpkg.HAStatePoint(
						site, clusterID, ci.T0Name,
						obs.ID, obs.Name,
						pn.TransportNodeID, pn.HighAvailabilityStatus,
						now,
					))
					ptsMu.Unlock()
					if strings.EqualFold(strings.TrimSpace(pn.HighAvailabilityStatus), "ACTIVE") {
						activeTN = pn.TransportNodeID
					}
				}

				activeByClusterMu.Lock()
				if activeByCluster[clusterID] == nil {
					activeByCluster[clusterID] = map[string]string{}
				}
				activeByCluster[clusterID][obs.ID] = activeTN
				activeByClusterMu.Unlock()
			}()
		}
	}
	wg.Wait()

	// 4. Per-cluster consensus + change detection.
	for clusterID, ci := range inv.Clusters {
		seen := activeByCluster[clusterID]
		if len(seen) == 0 {
			// Whole cluster failed (manager unreachable, all 404). Skip — next
			// cycle will retry. Update miss counters for substitution.
			h.bumpMissCounts(ci, missedT1[clusterID])
			continue
		}

		// Compute consensus ACTIVE = most frequent transport_node_id across
		// observed T1s that returned an ACTIVE.
		nodeCount := map[string]int{}
		for _, tn := range seen {
			if tn == "" {
				continue
			}
			nodeCount[tn]++
		}
		consensusNode, consensusCount := mostFrequent(nodeCount)
		points = append(points, influxpkg.HAClusterSummaryPoint(
			site, clusterID, ci.T0Name, consensusNode,
			len(seen), consensusCount, now,
		))

		// Diff vs previous cycle. prev[clusterID] may be empty (first run
		// after restart, or new cluster) → only baseline.
		h.mu.Lock()
		prev := h.prevActive[clusterID]
		if prev == nil {
			prev = map[string]string{}
		}
		var changed []string
		var fromActive, toActive string
		for t1ID, currTN := range seen {
			oldTN := prev[t1ID]
			if oldTN == "" {
				// no baseline for this T1 → don't count as change yet
				continue
			}
			if oldTN != currTN && currTN != "" {
				// find the T1 display name from the inventory for the event payload
				name := t1ID
				for _, o := range ci.Observed {
					if o.ID == t1ID {
						name = o.Name
						break
					}
				}
				changed = append(changed, name)
				fromActive = oldTN
				toActive = currTN
			}
		}

		// Refresh prevActive snapshot for the next cycle (only for T1s seen).
		newPrev := map[string]string{}
		for t1ID, tn := range seen {
			if tn == "" {
				// keep previous value if we couldn't determine ACTIVE this cycle
				if old, ok := prev[t1ID]; ok {
					newPrev[t1ID] = old
				}
				continue
			}
			newPrev[t1ID] = tn
		}
		h.prevActive[clusterID] = newPrev
		h.mu.Unlock()

		// Majority rule: emit change event only if changed_count >= ceil(observed/2),
		// or any change in a cluster with effective_observed < 3.
		observed := len(seen)
		threshold := (observed + 1) / 2 // ceil division
		if observed < 3 {
			threshold = 1
		}
		if len(changed) >= threshold && observed > 0 {
			sort.Strings(changed)
			points = append(points, influxpkg.HAChangeEventPoint(
				site, clusterID, ci.T0Name, fromActive, toActive,
				len(changed), observed, changed, now,
			))
			telemetry.HAChanges.WithLabelValues(site, clusterID).Inc()
			h.logger.Warn("ha: failover detected",
				zap.String("t0_cluster", ci.T0Name),
				zap.Int("changed", len(changed)),
				zap.Int("observed", observed),
				zap.String("from_active", fromActive),
				zap.String("to_active", toActive),
			)
		}

		// Update miss counts for healing.
		h.bumpMissCounts(ci, missedT1[clusterID])
	}

	inv.Updated = now
	if err := h.saveInventory(inv); err != nil {
		h.logger.Warn("ha: persist inventory failed", zap.Error(err))
	}

	return points, nil
}

// refreshWatchlist ensures ci.Observed has up to mgr.HAWatch.Size entries,
// honoring pinned names when present and topping up with random picks.
// Pinned entries are never substituted by random ones, but they are dropped
// if they no longer exist among the cluster's T1s.
func (h *HACollector) refreshWatchlist(ci *haClusterInventory, t1s []nsx.LogicalRouter) {
	size := h.manager.HAWatch.Size
	if size <= 0 {
		size = 10
	}
	mode := strings.ToLower(strings.TrimSpace(h.manager.HAWatch.Mode))
	if mode == "" {
		mode = "auto"
	}

	// Build lookup of existing T1s in the cluster.
	exists := map[string]string{} // t1_id → display_name
	byName := map[string]string{} // display_name → t1_id
	for _, t := range t1s {
		exists[t.ID] = t.DisplayName
		byName[t.DisplayName] = t.ID
	}

	// 1. Keep only entries that still exist; substitute missing non-pinned
	// entries that have reached the miss threshold.
	kept := ci.Observed[:0]
	dropped := 0
	for _, o := range ci.Observed {
		if _, ok := exists[o.ID]; ok {
			// reset miss counter, T1 is alive in the listing
			if ci.MissCount != nil {
				delete(ci.MissCount, o.ID)
			}
			// keep latest display name
			if name, ok := exists[o.ID]; ok && name != "" {
				o.Name = name
			}
			kept = append(kept, o)
			continue
		}
		// Not in current listing.
		if o.Pinned {
			// pinned T1 missing: log + drop (will be retried if it reappears
			// because we re-evaluate t1_names on every cycle below)
			h.logger.Warn("ha: pinned T1 not present in cluster, dropping",
				zap.String("t1_name", o.Name),
				zap.String("t0_cluster", ci.T0Name),
			)
			dropped++
			continue
		}
		if ci.MissCount == nil {
			ci.MissCount = map[string]int{}
		}
		ci.MissCount[o.ID]++
		if ci.MissCount[o.ID] < haMissCountThreshold {
			// give it one more cycle before substituting
			kept = append(kept, o)
			continue
		}
		delete(ci.MissCount, o.ID)
		dropped++
	}
	ci.Observed = kept

	if dropped > 0 {
		telemetry.HAWatchSubstitutions.WithLabelValues(h.manager.Site, ci.T0ClusterID).Add(float64(dropped))
	}

	// 2. Add pinned names from config that exist in the cluster and aren't yet observed.
	if mode == "pinned" || mode == "hybrid" {
		alreadyObserved := map[string]bool{}
		for _, o := range ci.Observed {
			alreadyObserved[o.ID] = true
		}
		for _, name := range h.manager.HAWatch.T1Names {
			id, ok := byName[name]
			if !ok || alreadyObserved[id] {
				continue
			}
			ci.Observed = append(ci.Observed, haObservedT1{ID: id, Name: name, Pinned: true})
			alreadyObserved[id] = true
			if len(ci.Observed) >= size {
				break
			}
		}
	}

	// 3. Top up with random picks (auto or hybrid).
	if mode == "auto" || mode == "hybrid" {
		alreadyObserved := map[string]bool{}
		for _, o := range ci.Observed {
			alreadyObserved[o.ID] = true
		}
		var candidates []nsx.LogicalRouter
		for _, t := range t1s {
			if !alreadyObserved[t.ID] {
				candidates = append(candidates, t)
			}
		}
		shuffleT1s(candidates)
		for _, t := range candidates {
			if len(ci.Observed) >= size {
				break
			}
			ci.Observed = append(ci.Observed, haObservedT1{ID: t.ID, Name: t.DisplayName, Pinned: false})
		}
	}

	// 4. Trim if somehow over size (e.g. ha_watch.size was reduced).
	if len(ci.Observed) > size {
		ci.Observed = ci.Observed[:size]
	}
}

// bumpMissCounts increments the miss counter for T1s that failed this cycle.
// Substitution happens lazily on the next refreshWatchlist call.
func (h *HACollector) bumpMissCounts(ci *haClusterInventory, missed map[string]struct{}) {
	if len(missed) == 0 {
		return
	}
	if ci.MissCount == nil {
		ci.MissCount = map[string]int{}
	}
	for id := range missed {
		ci.MissCount[id]++
	}
}

// mostFrequent returns the key with the highest value and its count.
// Ties broken by lexical order to keep output deterministic.
func mostFrequent(counts map[string]int) (string, int) {
	bestKey := ""
	bestCnt := 0
	for k, v := range counts {
		switch {
		case v > bestCnt:
			bestKey, bestCnt = k, v
		case v == bestCnt && k < bestKey:
			bestKey = k
		}
	}
	return bestKey, bestCnt
}

// shuffleT1s shuffles in place using crypto/rand.
func shuffleT1s(s []nsx.LogicalRouter) {
	for i := len(s) - 1; i > 0; i-- {
		j, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return // best-effort; leave as-is on rand failure
		}
		k := int(j.Int64())
		s[i], s[k] = s[k], s[i]
	}
}

// ---------------------------------------------------------------------------
// Inventory persistence
// ---------------------------------------------------------------------------

func (h *HACollector) inventoryPath() string {
	dir := h.manager.StateDir
	if dir == "" {
		dir = "/home/nsx_collector/state"
	}
	safeSite := strings.ToLower(strings.ReplaceAll(h.manager.Site, "/", "_"))
	return filepath.Join(dir, "ha-watch-"+safeSite+".json")
}

func (h *HACollector) loadInventory() (*HAInventory, error) {
	path := h.inventoryPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &HAInventory{Site: h.manager.Site, Clusters: map[string]*haClusterInventory{}}, nil
		}
		return nil, err
	}
	var inv HAInventory
	if err := json.Unmarshal(data, &inv); err != nil {
		return nil, err
	}
	return &inv, nil
}

func (h *HACollector) saveInventory(inv *HAInventory) error {
	path := h.inventoryPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir state_dir: %w", err)
	}
	data, err := json.MarshalIndent(inv, "", "  ")
	if err != nil {
		return err
	}
	// Atomic write: tmp + rename
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ---------------------------------------------------------------------------
// Helpers used by --print-clusters
// ---------------------------------------------------------------------------

// T0Cluster is a flattened (T0 → edge_cluster) pair for the --print-clusters
// CLI output (consumed by scripts/generate-mrpe-ha.sh).
type T0Cluster struct {
	Site          string `json:"site"`
	T0ClusterID   string `json:"t0_cluster_id"`
	T0DisplayName string `json:"t0_display_name"`
}

// ListT0Clusters returns one entry per T0 (TIER0 router with edge_cluster_id).
// Used by the --print-clusters command to feed the MRPE generator.
func ListT0Clusters(ctx context.Context, client *nsx.Client, site string) ([]T0Cluster, error) {
	routers, err := client.GetLogicalRouters(ctx)
	if err != nil {
		return nil, err
	}
	var out []T0Cluster
	seen := map[string]bool{}
	for _, r := range routers {
		if r.RouterType != "TIER0" || r.EdgeClusterID == "" {
			continue
		}
		key := r.EdgeClusterID
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, T0Cluster{
			Site:          site,
			T0ClusterID:   r.EdgeClusterID,
			T0DisplayName: r.DisplayName,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].T0DisplayName < out[j].T0DisplayName })
	return out, nil
}
