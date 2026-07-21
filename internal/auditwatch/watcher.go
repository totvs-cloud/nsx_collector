package auditwatch

import (
	"context"
	"errors"
	"hash/fnv"
	"time"

	"github.com/influxdata/influxdb-client-go/v2/api/write"
	"go.uber.org/zap"

	influxpkg "nsx-collector/internal/influxdb"
	"nsx-collector/internal/nsx"
	"nsx-collector/internal/telemetry"
)

// Watcher tails the nsx-audit.log of every Manager node of one site and writes
// per-(src_ip, username, operation, status) call counters to InfluxDB
// (measurement nsx_api_client_calls). É o dado por trás do dashboard
// "NSX — API Usage": quem chama a API do Manager, o quê e com que frequência.
//
// Custo por intervalo: 1 GET /cluster/status + 1 GET de log por nó (leituras
// de arquivo, sem fan-out). O audit log registra só chamadas auditadas, então
// os números são um piso do volume real — suficiente para ranquear ofensores.
type Watcher struct {
	site     string
	client   *nsx.Client
	writer   *influxpkg.Writer
	logger   *zap.Logger
	interval time.Duration

	cursors  map[string]*nodeCursor // manager node UUID -> tail position
	lastPoll time.Time
}

// nodeCursor marks how far into a node's audit log we have already counted.
// seenAtLast holds the hashes of the lines sharing lastTS, so re-reading the
// same timestamp across polls never double-counts.
type nodeCursor struct {
	lastTS     time.Time
	seenAtLast map[uint64]struct{}
	baselined  bool
}

// New creates a watcher for one site. Cursors are in-memory: after a restart
// the first poll re-baselines (one interval of data is skipped, not recounted).
func New(site string, client *nsx.Client, writer *influxpkg.Writer, interval time.Duration) *Watcher {
	return &Watcher{
		site:     site,
		client:   client,
		writer:   writer,
		logger:   zap.L().Named("auditwatch").Named(site),
		interval: interval,
		cursors:  make(map[string]*nodeCursor),
	}
}

// Run polls until the context is cancelled. The first poll only baselines.
func (w *Watcher) Run(ctx context.Context) {
	w.logger.Info("auditwatch starting", zap.Duration("interval", w.interval))
	w.poll(ctx)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			w.logger.Info("auditwatch stopped")
			return
		case <-ticker.C:
			w.poll(ctx)
		}
	}
}

func (w *Watcher) poll(ctx context.Context) {
	now := time.Now()
	windowS := w.interval.Seconds()
	if !w.lastPoll.IsZero() {
		windowS = now.Sub(w.lastPoll).Seconds()
	}
	w.lastPoll = now

	cs, err := w.client.GetClusterStatus(ctx)
	if err != nil {
		w.logger.Warn("cluster status failed", zap.Error(err))
		telemetry.CollectErrors.WithLabelValues(w.site, "audit_watch").Inc()
		return
	}

	var points []*write.Point
	for _, n := range cs.MgmtClusterStatus.OnlineNodes {
		nodeTag := n.MgmtClusterListenIPAddress
		if nodeTag == "" {
			nodeTag = n.UUID
		}
		pts, err := w.pollNode(ctx, n.UUID, nodeTag, windowS, now)
		if err != nil {
			w.logger.Warn("audit log poll failed", zap.String("node", nodeTag), zap.Error(err))
			telemetry.CollectErrors.WithLabelValues(w.site, "audit_watch").Inc()
			continue
		}
		points = append(points, pts...)
	}

	if err := w.writer.WritePoints(ctx, points); err != nil {
		w.logger.Error("audit points write failed", zap.Error(err))
		telemetry.CollectErrors.WithLabelValues(w.site, "audit_watch_write").Inc()
		return
	}
	if len(points) > 0 {
		w.logger.Info("api call counters written", zap.Int("series", len(points)))
	}
}

func (w *Watcher) pollNode(ctx context.Context, nodeID, nodeTag string, windowS float64, now time.Time) ([]*write.Point, error) {
	raw, err := w.client.GetClusterNodeLog(ctx, nodeID, "nsx-audit.log")
	if err != nil {
		return nil, err
	}
	entries := parseAll(raw)

	cur, ok := w.cursors[nodeID]
	if !ok {
		cur = &nodeCursor{}
		w.cursors[nodeID] = cur
	}

	// Primeira leitura deste nó: só marca a posição (baseline) para não
	// contar histórico antigo como se fosse tráfego da janela atual.
	if !cur.baselined {
		cur.advanceTo(entries)
		cur.baselined = true
		w.logger.Info("baselined", zap.String("node", nodeTag), zap.Int("lines", len(entries)))
		return nil, nil
	}

	// Rotação entre polls: se a linha mais antiga do arquivo atual já é mais
	// nova que o cursor, o arquivo rotacionou — busca o .1 para cobrir o gap.
	if len(entries) > 0 && !cur.lastTS.IsZero() && entries[0].TS.After(cur.lastTS) {
		if rotated, rerr := w.client.GetClusterNodeLog(ctx, nodeID, "nsx-audit.log.1"); rerr == nil {
			entries = append(parseAll(rotated), entries...)
		} else if !errors.Is(rerr, nsx.ErrLogNotFound) {
			w.logger.Warn("rotated audit log fetch failed", zap.String("node", nodeTag), zap.Error(rerr))
		}
	}

	fresh := cur.filterNew(entries)
	cur.advanceTo(entries)
	if len(fresh) == 0 {
		return nil, nil
	}

	type key struct{ src, user, op, status string }
	counts := make(map[key]int64)
	for _, e := range fresh {
		counts[key{e.Src, e.User, e.Operation, e.Status}]++
	}

	pts := make([]*write.Point, 0, len(counts))
	for k, c := range counts {
		pts = append(pts, influxpkg.APIClientCallPoint(w.site, nodeTag, k.src, k.user, k.op, k.status, c, windowS, now))
	}
	return pts, nil
}

func lineHash(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

// filterNew returns entries strictly after the cursor, plus entries at the
// cursor timestamp whose line hash was not seen yet.
func (c *nodeCursor) filterNew(entries []Entry) []Entry {
	var out []Entry
	for _, e := range entries {
		switch {
		case e.TS.After(c.lastTS):
			out = append(out, e)
		case e.TS.Equal(c.lastTS):
			if _, seen := c.seenAtLast[lineHash(e.Raw)]; !seen {
				out = append(out, e)
			}
		}
	}
	return out
}

// advanceTo moves the cursor to the newest timestamp present in entries and
// records the hashes of every line at that timestamp.
func (c *nodeCursor) advanceTo(entries []Entry) {
	var maxTS time.Time
	for _, e := range entries {
		if e.TS.After(maxTS) {
			maxTS = e.TS
		}
	}
	if maxTS.IsZero() || maxTS.Before(c.lastTS) {
		return
	}
	if !maxTS.Equal(c.lastTS) {
		c.seenAtLast = make(map[uint64]struct{})
		c.lastTS = maxTS
	} else if c.seenAtLast == nil {
		c.seenAtLast = make(map[uint64]struct{})
	}
	for _, e := range entries {
		if e.TS.Equal(maxTS) {
			c.seenAtLast[lineHash(e.Raw)] = struct{}{}
		}
	}
}
