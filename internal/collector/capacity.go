package collector

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/influxdata/influxdb-client-go/v2/api/write"
	"go.uber.org/zap"

	"nsx-collector/internal/config"
	influxpkg "nsx-collector/internal/influxdb"
	"nsx-collector/internal/nsx"
	"nsx-collector/internal/t1watch"
	"nsx-collector/internal/telemetry"
)

// t0Meta is the per-T0 metadata index built from the Policy API listing,
// keyed by the Tier-0 path (which appears as Tier1.tier0_path on each T1).
type t0Meta struct {
	ID    string
	Name  string
	IsVRF bool
}

// CapacityCollector runs the extended Capacity NSX collection for one manager:
//   - LB credits (manager + edge_node)
//   - Policy API T0/T1 inventory (distinguishes VRFs from regular T0s)
//   - T1-per-VRF and T1-per-T0 aggregates with configured limits
//   - Edge cluster ID → display_name resolution (for t1watch messages)
//   - Segments per parent (T1/VRF/T0) — when capacity.collect_segments=true
//   - Gateway firewall rules per gateway — when capacity.collect_gw_policies=true
//   - Groups inventory (total + empty) — when capacity.collect_groups=true
//   - NAT rules per T1 (top-N or all) — when capacity.collect_nat_per_t1=true
//   - T1 lifecycle diff + Slack notification via t1watch.Notifier
type CapacityCollector struct {
	site        string
	client      *nsx.Client
	logger      *zap.Logger
	stateDir    string
	cfg         config.CapacityConfig
	t1cfg       config.T1WatchConfig
	notifier    *t1watch.Notifier
}

// NewCapacityCollector builds the collector. notifier may be nil when t1_watch
// is disabled — the inventory snapshot is still maintained for next-run
// readiness, but no Slack messages go out.
func NewCapacityCollector(
	site string,
	client *nsx.Client,
	stateDir string,
	cap config.CapacityConfig,
	t1cfg config.T1WatchConfig,
	notifier *t1watch.Notifier,
	logger *zap.Logger,
) *CapacityCollector {
	return &CapacityCollector{
		site:     site,
		client:   client,
		logger:   logger.Named("capacity"),
		stateDir: stateDir,
		cfg:      cap,
		t1cfg:    t1cfg,
		notifier: notifier,
	}
}

// Collect runs one Capacity NSX cycle and returns:
//   - capacityPoints: go to the capacity bucket
//   - points:         go to the default bucket
// Errors are logged but never abort the cycle — every collection is best-effort.
func (cc *CapacityCollector) Collect(ctx context.Context, now time.Time) (capacityPoints, points []*write.Point) {
	site := cc.site

	// ---- LB credits -------------------------------------------------------
	if summary, err := cc.client.GetLBNodeUsageSummary(ctx); err != nil {
		cc.logger.Warn("lb credits failed", zap.Error(err))
		telemetry.CollectErrors.WithLabelValues(site, "lb_credits").Inc()
	} else if summary != nil {
		capacityPoints = append(capacityPoints, influxpkg.LBCreditsManagerPoint(site, summary, now))
		telemetry.LBCreditsPct.WithLabelValues(site).Set(summary.UsagePercentage)
		for i := range summary.NodeUsages {
			capacityPoints = append(capacityPoints, influxpkg.LBCreditsNodePoint(site, &summary.NodeUsages[i], now))
		}
	}

	// ---- Policy API T0/T1 + edge clusters --------------------------------
	t0s, err := cc.client.GetPolicyTier0s(ctx)
	if err != nil {
		cc.logger.Warn("policy tier-0s failed", zap.Error(err))
		telemetry.CollectErrors.WithLabelValues(site, "policy_tier0").Inc()
		// Without T0 inventory we can't build the VRF map. Bail on the rest of
		// the Policy-API-dependent collection (segments/NAT/FW are pointless
		// without parent attribution).
		return capacityPoints, points
	}
	t1s, err := cc.client.GetPolicyTier1s(ctx)
	if err != nil {
		cc.logger.Warn("policy tier-1s failed", zap.Error(err))
		telemetry.CollectErrors.WithLabelValues(site, "policy_tier1").Inc()
		return capacityPoints, points
	}

	// Resolve edge_cluster_id → display_name. Best-effort.
	ecNames := map[string]string{}
	if ecs, err := cc.client.GetPolicyEdgeClusters(ctx); err != nil {
		cc.logger.Debug("policy edge-clusters failed", zap.Error(err))
	} else {
		for _, ec := range ecs {
			if ec.NSXID != "" {
				ecNames[ec.NSXID] = ec.DisplayName
			}
		}
	}

	// Build T0/VRF lookups by path (the tier0_path on each T1 is the join key).
	t0ByPath := make(map[string]t0Meta, len(t0s))
	for i := range t0s {
		t := &t0s[i]
		t0ByPath[t.Path] = t0Meta{ID: t.UniqueID, Name: t.DisplayName, IsVRF: t.IsVRF()}
	}

	// ---- Build live T1 view + aggregates --------------------------------
	// Also need edge_cluster_id per T1 — Policy tier-1 list doesn't include it,
	// so we use the legacy logical-routers list as the source of truth.
	legacy, err := cc.client.GetLogicalRouters(ctx)
	if err != nil {
		cc.logger.Warn("legacy logical-routers failed", zap.Error(err))
		telemetry.CollectErrors.WithLabelValues(site, "legacy_routers").Inc()
	}
	legacyByUniqueID := make(map[string]nsx.LogicalRouter, len(legacy))
	for _, r := range legacy {
		if r.ID != "" {
			legacyByUniqueID[r.ID] = r
		}
	}

	var live []t1watch.LiveT1
	vrfCount := map[string]int64{}
	vrfIDByName := map[string]string{}
	vrfT0Parent := map[string]string{} // VRF name → parent T0 display name (best effort: extract from suffix)
	t0Count := map[string]int64{}
	t0IDByName := map[string]string{}
	var siteT1Total, onVRF, onT0 int64

	for i := range t1s {
		t := &t1s[i]
		parent, ok := t0ByPath[t.Tier0Path]
		if !ok {
			// Tier0 path references a T0 we didn't see — treat as orphan/unknown.
			parent = t0Meta{ID: "", Name: nsx.LastPathSegment(t.Tier0Path), IsVRF: false}
		}
		kind := "t0"
		if parent.IsVRF {
			kind = "vrf"
		}
		ecID := ""
		ecName := ""
		if lr, ok := legacyByUniqueID[t.UniqueID]; ok {
			ecID = lr.EdgeClusterID
			ecName = ecNames[ecID]
		}
		live = append(live, t1watch.LiveT1{
			ID:              t.UniqueID,
			Name:            t.DisplayName,
			ParentT0ID:      parent.ID,
			ParentT0Name:    parent.Name,
			ParentKind:      kind,
			EdgeClusterID:   ecID,
			EdgeClusterName: ecName,
		})

		siteT1Total++
		if parent.IsVRF {
			onVRF++
			vrfCount[parent.Name]++
			vrfIDByName[parent.Name] = parent.ID
			// Best-effort guess: VRF names follow "<T0>-vrf_<suffix>" — split on "-vrf_".
			if idx := strings.LastIndex(parent.Name, "-vrf_"); idx >= 0 {
				vrfT0Parent[parent.Name] = parent.Name[:idx]
			}
		} else {
			onT0++
			t0Count[parent.Name]++
			t0IDByName[parent.Name] = parent.ID
		}
	}

	points = append(points, influxpkg.SiteT1TotalsPoint(site, siteT1Total, onVRF, onT0, now))

	// VRF and T0 limit resolvers.
	vrfLimit := t1watch.LimitResolver(cc.t1cfg.VRFT1LimitDefault, cc.t1cfg.VRFT1Limits)
	t0Limit := t1watch.LimitResolver(cc.t1cfg.T0T1LimitDefault, cc.t1cfg.T0T1Limits)

	for vrfName, cnt := range vrfCount {
		capacityPoints = append(capacityPoints, influxpkg.T1PerVRFPoint(
			site, vrfName, vrfIDByName[vrfName], vrfT0Parent[vrfName],
			cnt, vrfLimit(vrfName), now,
		))
	}
	for t0Name, cnt := range t0Count {
		capacityPoints = append(capacityPoints, influxpkg.T1PerT0Point(
			site, t0Name, t0IDByName[t0Name], cnt, t0Limit(t0Name), now,
		))
	}

	// ---- t1watch — diff + notify ----------------------------------------
	if cc.t1cfg.Enabled || cc.notifier != nil {
		// Snapshot is loaded/saved regardless of Enabled so the baseline is
		// in place when the operator flips Enabled to true.
		snap, baselined, err := t1watch.LoadSnapshot(cc.stateDir, site)
		if err != nil {
			cc.logger.Warn("t1watch load snapshot failed", zap.Error(err))
		}
		if snap == nil {
			snap = &t1watch.Snapshot{Site: site, Known: map[string]t1watch.T1Info{}}
		}
		events := t1watch.Detect(snap, live, baselined, vrfCount, siteT1Total, vrfLimit, now)
		telemetry.T1KnownGauge.WithLabelValues(site).Set(float64(len(snap.Known)))
		if err := t1watch.SaveSnapshot(cc.stateDir, snap); err != nil {
			cc.logger.Warn("t1watch save snapshot failed", zap.Error(err))
		}

		// Emit InfluxDB event points for every detected event (regardless of
		// whether Slack is enabled, so the dashboard "novos T1s" panel still
		// works).
		for _, ev := range events {
			ecName := ev.T1.EdgeClusterName
			if ecName == "" {
				ecName = ev.T1.EdgeClusterID
			}
			points = append(points, influxpkg.T1EventPoint(
				site, ev.Kind, ev.T1.ID, ev.T1.Name, ev.T1.ParentT0Name, ecName,
				ev.VRFT1CountAfter, ev.VRFT1Limit, ev.SiteT1Total, ev.OccurredAt,
			))
			if ev.Kind == "created" {
				telemetry.T1Created.WithLabelValues(site).Inc()
			} else if ev.Kind == "deleted" {
				telemetry.T1Deleted.WithLabelValues(site).Inc()
			}
		}

		if cc.t1cfg.Enabled && cc.notifier != nil && len(events) > 0 {
			sent, errs := cc.notifier.Send(events)
			if sent > 0 {
				telemetry.T1NotifySent.WithLabelValues(site).Add(float64(sent))
			}
			if errs > 0 {
				telemetry.T1NotifyErrors.WithLabelValues(site).Add(float64(errs))
			}
		}
	}

	// ---- Segments per parent --------------------------------------------
	if cc.cfg.CollectSegments {
		telemetry.CapacityExtrasPolls.WithLabelValues(site, "segments").Inc()
		if segs, err := cc.client.GetPolicySegments(ctx); err != nil {
			cc.logger.Warn("policy segments failed", zap.Error(err))
			telemetry.CollectErrors.WithLabelValues(site, "segments").Inc()
		} else {
			perParent := map[string]int64{} // key: "kind|name|id"
			for _, s := range segs {
				kind, name, id := classifyConnectivityPath(s.ConnectivityPath, t0ByPath, t1s)
				key := kind + "|" + name + "|" + id
				perParent[key]++
			}
			for key, count := range perParent {
				parts := strings.SplitN(key, "|", 3)
				if len(parts) != 3 {
					continue
				}
				capacityPoints = append(capacityPoints, influxpkg.SegmentsPerParentPoint(
					site, parts[0], parts[1], parts[2], count, now,
				))
			}
		}
	}

	// ---- Gateway firewall policies per gateway --------------------------
	if cc.cfg.CollectGWPolicies {
		telemetry.CapacityExtrasPolls.WithLabelValues(site, "gw_policies").Inc()
		if policies, err := cc.client.GetGatewayPolicies(ctx); err != nil {
			cc.logger.Warn("gateway policies failed", zap.Error(err))
			telemetry.CollectErrors.WithLabelValues(site, "gw_policies").Inc()
		} else {
			type acc struct {
				rules    int64
				policies int64
			}
			perGW := map[string]acc{} // key: "kind|name|id"
			for _, p := range policies {
				for _, scope := range p.Scope {
					kind, name, id := classifyConnectivityPath(scope, t0ByPath, t1s)
					if kind == "overlay" || kind == "unknown" {
						continue
					}
					k := kind + "|" + name + "|" + id
					a := perGW[k]
					a.rules += int64(p.RuleCount)
					a.policies++
					perGW[k] = a
				}
			}
			for key, a := range perGW {
				parts := strings.SplitN(key, "|", 3)
				if len(parts) != 3 {
					continue
				}
				capacityPoints = append(capacityPoints, influxpkg.FWPerGatewayPoint(
					site, parts[0], parts[1], parts[2], a.rules, a.policies, now,
				))
			}
		}
	}

	// ---- Groups inventory -----------------------------------------------
	if cc.cfg.CollectGroups {
		telemetry.CapacityExtrasPolls.WithLabelValues(site, "groups").Inc()
		if groups, err := cc.client.GetPolicyGroups(ctx); err != nil {
			cc.logger.Warn("policy groups failed", zap.Error(err))
			telemetry.CollectErrors.WithLabelValues(site, "groups").Inc()
		} else {
			var total, empty int64
			for _, g := range groups {
				total++
				if len(g.Expression) == 0 {
					empty++
				}
			}
			capacityPoints = append(capacityPoints, influxpkg.GroupsInventoryPoint(site, total, empty, now))
		}
	}

	// ---- NAT rules per T1 (expensive — 1 req per T1) ---------------------
	if cc.cfg.CollectNATPerT1 && len(t1s) > 0 {
		telemetry.CapacityExtrasPolls.WithLabelValues(site, "nat_per_t1").Inc()
		pace := time.Duration(cc.cfg.NATPerT1PaceMS) * time.Millisecond
		parallel := cc.cfg.NATPerT1Parallel
		if parallel <= 0 {
			parallel = 4
		}
		natResults := cc.fetchNATPerT1(ctx, t1s, pace, parallel)
		for _, r := range natResults {
			parent := t0ByPath[r.tier0Path]
			kind := "t0"
			if parent.IsVRF {
				kind = "vrf"
			}
			capacityPoints = append(capacityPoints, influxpkg.NATPerT1Point(
				site, r.t1ID, r.t1Name, kind, parent.Name, int64(r.natCount), now,
			))
		}
	}

	return capacityPoints, points
}

type natResult struct {
	t1ID      string
	t1Name    string
	tier0Path string
	natCount  int
}

// fetchNATPerT1 issues one NAT-count request per T1 with bounded parallelism
// and inter-request pacing. Errors are logged and counted but never abort the
// batch — partial results are written.
func (cc *CapacityCollector) fetchNATPerT1(ctx context.Context, t1s []nsx.PolicyTier1, pace time.Duration, parallel int) []natResult {
	sem := make(chan struct{}, parallel)
	out := make([]natResult, 0, len(t1s))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := range t1s {
		t1 := t1s[i]
		// Pace requests globally to keep the NSX Manager happy.
		if pace > 0 && i > 0 {
			select {
			case <-ctx.Done():
				wg.Wait()
				return out
			case <-time.After(pace):
			}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			cnt, err := cc.client.GetTier1NATRuleCount(ctx, t1.ID)
			if err != nil {
				telemetry.CollectErrors.WithLabelValues(cc.site, "nat_per_t1").Inc()
				return
			}
			mu.Lock()
			out = append(out, natResult{
				t1ID:      t1.ID,
				t1Name:    t1.DisplayName,
				tier0Path: t1.Tier0Path,
				natCount:  cnt,
			})
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// classifyConnectivityPath inspects a Policy API path (/infra/tier-1s/<id>,
// /infra/tier-0s/<id>, or empty) and returns (kind, parent_name, parent_id).
// kind ∈ {"t1","vrf","t0","overlay","unknown"}.
//
// When kind=="t1" the returned name/id are the T1's; for "vrf"/"t0" they are
// the T0/VRF's. "overlay" means the segment is connected to neither (likely
// transport zone overlay) — caller decides whether to track.
func classifyConnectivityPath(p string, t0ByPath map[string]t0Meta, t1s []nsx.PolicyTier1) (kind, name, id string) {
	if p == "" {
		return "overlay", "-", "-"
	}
	if strings.Contains(p, "/tier-1s/") {
		for i := range t1s {
			if t1s[i].Path == p {
				return "t1", t1s[i].DisplayName, t1s[i].UniqueID
			}
		}
		return "t1", nsx.LastPathSegment(p), nsx.LastPathSegment(p)
	}
	if strings.Contains(p, "/tier-0s/") {
		if meta, ok := t0ByPath[p]; ok {
			if meta.IsVRF {
				return "vrf", meta.Name, meta.ID
			}
			return "t0", meta.Name, meta.ID
		}
		return "t0", nsx.LastPathSegment(p), nsx.LastPathSegment(p)
	}
	return "unknown", "-", "-"
}
