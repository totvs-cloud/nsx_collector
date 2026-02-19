package collector

import (
	"context"
	"time"

	"github.com/influxdata/influxdb-client-go/v2/api/write"
	"go.uber.org/zap"

	"nsx-collector/internal/config"
	influxpkg "nsx-collector/internal/influxdb"
	"nsx-collector/internal/nsx"
	"nsx-collector/internal/telemetry"
)

// Worker collects all NSX metrics for a single manager and writes to InfluxDB.
type Worker struct {
	manager config.Manager
	client  *nsx.Client
	writer  *influxpkg.Writer
	logger  *zap.Logger
}

// NewWorker creates a new collector worker for the given manager.
func NewWorker(mgr config.Manager, writer *influxpkg.Writer) *Worker {
	client := nsx.NewClient(mgr.URL, mgr.Username, mgr.Password, mgr.TLSSkipVerify)
	return &Worker{
		manager: mgr,
		client:  client,
		writer:  writer,
		logger:  zap.L().Named(mgr.Site),
	}
}

// Collect runs a full collection cycle for this manager.
func (w *Worker) Collect(ctx context.Context) {
	start := time.Now()
	site := w.manager.Site
	logger := w.logger

	defer func() {
		elapsed := time.Since(start)
		telemetry.CollectCyclesTotal.WithLabelValues(site).Inc()
		telemetry.CollectDuration.WithLabelValues(site).Observe(elapsed.Seconds())
		logger.Debug("collection cycle complete", zap.Duration("elapsed", elapsed))
	}()

	now := time.Now()
	var points []*write.Point

	// 1. Cluster status
	if cs, err := w.client.GetClusterStatus(ctx); err != nil {
		logger.Warn("cluster status failed", zap.Error(err))
		telemetry.CollectErrors.WithLabelValues(site, "cluster").Inc()
	} else {
		points = append(points, influxpkg.ClusterStatusPoint(site, cs, now))
	}

	// 2. Transport nodes — list all, then fetch status for each
	nodes, err := w.client.GetTransportNodes(ctx)
	if err != nil {
		logger.Warn("transport nodes list failed", zap.Error(err))
		telemetry.CollectErrors.WithLabelValues(site, "transport_nodes").Inc()
	} else {
		for _, node := range nodes {
			nodeID := node.ID
			nodeName := node.DisplayName
			nodeType := node.NodeDeploymentInfo.ResourceType
			if nodeType == "" {
				nodeType = "HostNode"
			}

			ts, err := w.client.GetTransportNodeStatus(ctx, nodeID)
			if err != nil {
				logger.Warn("transport node status failed",
					zap.String("node", nodeName),
					zap.Error(err),
				)
				telemetry.CollectErrors.WithLabelValues(site, "transport_node_status").Inc()
				continue
			}

			pts := influxpkg.TransportNodeStatusPoints(site, nodeID, nodeName, nodeType, ts, now)
			points = append(points, pts...)
		}
		logger.Debug("transport nodes collected", zap.Int("count", len(nodes)))
	}

	// 3. Logical routers (T0, T1, VRF) — inventory + BGP for T0/VRF
	routers, err := w.client.GetLogicalRouters(ctx)
	if err != nil {
		logger.Warn("logical routers failed", zap.Error(err))
		telemetry.CollectErrors.WithLabelValues(site, "logical_routers").Inc()
	} else {
		// Build T1→T0 name map using logical router ports
		t1ToT0Name := buildT1ToT0Map(ctx, w.client, routers, logger)

		for i := range routers {
			lr := &routers[i]
			parentT0 := t1ToT0Name[lr.ID]
			if lr.RouterType == "TIER1" && parentT0 == "" {
				parentT0 = "N/A"
			}
			points = append(points, influxpkg.LogicalRouterPoint(site, parentT0, lr, now))

			// Collect BGP for T0 and VRF routers
			if lr.RouterType == "TIER0" || lr.RouterType == "VRF" {
				bgp, err := w.client.GetBGPNeighborStatus(ctx, lr.ID)
				if err != nil {
					logger.Warn("BGP status failed",
						zap.String("router", lr.DisplayName),
						zap.Error(err),
					)
					telemetry.CollectErrors.WithLabelValues(site, "bgp").Inc()
					continue
				}
				for j := range bgp.Results {
					points = append(points, influxpkg.BGPNeighborPoint(site, lr.ID, lr.DisplayName, &bgp.Results[j], now))
				}
			}
		}
		logger.Debug("logical routers collected", zap.Int("count", len(routers)))
	}

	// Write all points
	if err := w.writer.WritePoints(ctx, points); err != nil {
		logger.Error("write failed", zap.Error(err))
		telemetry.CollectErrors.WithLabelValues(site, "write").Inc()
		return
	}
	telemetry.PointsWritten.WithLabelValues(site).Add(float64(len(points)))
	logger.Info("points written", zap.Int("count", len(points)))
}

// buildT1ToT0Map fetches all logical router ports and builds a map of
// T1 router UUID → T0 router display name, used to tag T1 points with their parent T0.
func buildT1ToT0Map(ctx context.Context, client *nsx.Client, routers []nsx.LogicalRouter, logger *zap.Logger) map[string]string {
	// routerIDToName: UUID → display name for all routers
	routerIDToName := make(map[string]string, len(routers))
	for _, r := range routers {
		routerIDToName[r.ID] = r.DisplayName
	}

	ports, err := client.GetLogicalRouterPorts(ctx)
	if err != nil {
		logger.Warn("logical router ports failed, T1→T0 mapping unavailable", zap.Error(err))
		return nil
	}

	// portIDToRouterID: port UUID → router UUID (for all ports)
	portIDToRouterID := make(map[string]string, len(ports))
	for _, p := range ports {
		portIDToRouterID[p.ID] = p.LogicalRouterID
	}

	// For each LogicalRouterLinkPortOnTIER1 (T1 side), resolve T1 → T0 name
	t1ToT0Name := make(map[string]string)
	for _, p := range ports {
		if p.ResourceType != "LogicalRouterLinkPortOnTIER1" || p.LinkedLogicalRouterPortID.TargetID == "" {
			continue
		}
		t1RouterID := p.LogicalRouterID
		t0RouterID := portIDToRouterID[p.LinkedLogicalRouterPortID.TargetID]
		if t0RouterID == "" {
			continue
		}
		if name, ok := routerIDToName[t0RouterID]; ok {
			t1ToT0Name[t1RouterID] = name
		}
	}
	return t1ToT0Name
}
