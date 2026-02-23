package collector

import (
	"context"
	"strings"
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
	manager      config.Manager
	client       *nsx.Client
	writer       *influxpkg.Writer
	logger       *zap.Logger
	slowInterval time.Duration
	lastSlow     time.Time
}

// NewWorker creates a new collector worker for the given manager.
func NewWorker(mgr config.Manager, writer *influxpkg.Writer, intervals config.IntervalConfig) *Worker {
	client := nsx.NewClient(mgr.URL, mgr.Username, mgr.Password, mgr.TLSSkipVerify)
	return &Worker{
		manager:      mgr,
		client:       client,
		writer:       writer,
		logger:       zap.L().Named(mgr.Site),
		slowInterval: intervals.Slow,
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

	// Determine whether to run slow-path sections (alarms, capacity, LB).
	// On the very first cycle lastSlow is zero, so runSlow is true.
	runSlow := w.lastSlow.IsZero() || time.Since(w.lastSlow) >= w.slowInterval
	if runSlow {
		w.lastSlow = now
	}

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

			// Collect physical uplink stats for Edge nodes
			if isEdgeNodeType(nodeType) {
				ifaces, err := w.client.GetTransportNodeInterfaces(ctx, nodeID)
				if err != nil {
					logger.Warn("interface list failed",
						zap.String("node", nodeName),
						zap.Error(err),
					)
					telemetry.CollectErrors.WithLabelValues(site, "edge_interfaces").Inc()
				} else {
					uplinkCandidates := 0
					for _, iface := range ifaces {
						if !isEdgeUplinkInterface(&iface) {
							continue
						}
						uplinkCandidates++
						ifStats, err := w.client.GetTransportNodeInterfaceStats(ctx, nodeID, iface.InterfaceID)
						if err != nil {
							logger.Warn("interface stats failed",
								zap.String("node", nodeName),
								zap.String("interface", iface.InterfaceID),
								zap.String("interface_type", iface.InterfaceType),
								zap.Error(err),
							)
							telemetry.CollectErrors.WithLabelValues(site, "edge_interface_stats").Inc()
							continue
						}
						points = append(points, influxpkg.EdgeUplinkStatsPoint(site, nodeID, nodeName, &iface, ifStats, now))
					}
					logger.Debug("edge interfaces evaluated",
						zap.String("node", nodeName),
						zap.Int("interfaces_total", len(ifaces)),
						zap.Int("uplink_candidates", uplinkCandidates),
					)
				}
			}
		}
		logger.Debug("transport nodes collected", zap.Int("count", len(nodes)))
	}

	// 3. Logical routers (T0, T1, VRF) — inventory
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
		}
		logger.Debug("logical routers collected", zap.Int("count", len(routers)))
	}

	if runSlow {
		// 4. Active alarms (NSX faults)
		alarms, err := w.client.GetActiveAlarms(ctx)
		if err != nil {
			logger.Warn("alarms failed", zap.Error(err))
			telemetry.CollectErrors.WithLabelValues(site, "alarms").Inc()
		} else {
			for i := range alarms {
				points = append(points, influxpkg.AlarmPoint(site, &alarms[i], now))
			}
			logger.Debug("alarms collected", zap.Int("count", len(alarms)))
		}

		// 5. Capacity usage
		capacities, err := w.client.GetCapacityUsage(ctx)
		if err != nil {
			logger.Warn("capacity usage failed", zap.Error(err))
			telemetry.CollectErrors.WithLabelValues(site, "capacity").Inc()
		} else {
			for i := range capacities {
				points = append(points, influxpkg.CapacityPoint(site, &capacities[i], now))
			}
			logger.Debug("capacity collected", zap.Int("count", len(capacities)))
		}

		// 6. NS Services count
		if svcCount, err := w.client.GetNSServicesCount(ctx); err != nil {
			logger.Warn("ns-services count failed", zap.Error(err))
			telemetry.CollectErrors.WithLabelValues(site, "ns_services").Inc()
		} else {
			points = append(points, influxpkg.CapacityPoint(site, &nsx.CapacityUsageItem{
				UsageType:         "NUMBER_OF_NS_SERVICES",
				DisplayName:       "NS Services",
				CurrentUsageCount: svcCount,
			}, now))
			logger.Debug("ns-services collected", zap.Int64("count", svcCount))
		}

		// 7. Load Balancer — services, virtual servers, pool members
		lbServices, err := w.client.GetLBServices(ctx)
		if err != nil {
			logger.Warn("lb services list failed", zap.Error(err))
			telemetry.CollectErrors.WithLabelValues(site, "lb_services").Inc()
		} else if len(lbServices) > 0 {
			// Build resolution maps: UUID → metadata (for name/IP/port tags on status points)
			lbVServers, err := w.client.GetLBVirtualServers(ctx)
			if err != nil {
				logger.Warn("lb virtual servers list failed", zap.Error(err))
				telemetry.CollectErrors.WithLabelValues(site, "lb_services").Inc()
			}
			vsMap := make(map[string]nsx.LBVirtualServer, len(lbVServers))
			for _, vs := range lbVServers {
				vsMap[vs.ID] = vs
			}

			lbPools, err := w.client.GetLBPools(ctx)
			if err != nil {
				logger.Warn("lb pools list failed", zap.Error(err))
				telemetry.CollectErrors.WithLabelValues(site, "lb_services").Inc()
			}
			poolMap := make(map[string]nsx.LBPool, len(lbPools))
			for _, p := range lbPools {
				poolMap[p.ID] = p
			}

			for i := range lbServices {
				svc := &lbServices[i]
				status, err := w.client.GetLBServiceStatus(ctx, svc.ID)
				if err != nil {
					logger.Warn("lb service status failed",
						zap.String("service", svc.DisplayName),
						zap.Error(err),
					)
					telemetry.CollectErrors.WithLabelValues(site, "lb_service_status").Inc()
					continue
				}

				points = append(points, influxpkg.LBServicePoint(site, svc, status, now))

				for _, vs := range status.VirtualServers {
					points = append(points, influxpkg.LBVirtualServerPoint(site, svc.ID, vsMap, vs, now))
				}

				for _, pool := range status.Pools {
					points = append(points, influxpkg.LBPoolPoint(site, poolMap, pool, now))
				}
			}
			logger.Debug("lb collected",
				zap.Int("services", len(lbServices)),
			)
		}
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
		linkedID := p.LinkedPortID()
		if p.ResourceType != "LogicalRouterLinkPortOnTIER1" || linkedID == "" {
			continue
		}
		t1RouterID := p.LogicalRouterID
		t0RouterID := portIDToRouterID[linkedID]
		if t0RouterID == "" {
			continue
		}
		if name, ok := routerIDToName[t0RouterID]; ok {
			t1ToT0Name[t1RouterID] = name
		}
	}
	return t1ToT0Name
}

// isEdgeUplinkInterface classifies edge interfaces that likely carry dataplane uplink traffic.
// NSX versions may vary interface_type values, so this accepts common physical/uplink variants
// and excludes management/loopback/tunnel styles.
func isEdgeUplinkInterface(iface *nsx.NetworkInterface) bool {
	t := strings.ToUpper(strings.TrimSpace(iface.InterfaceType))
	id := strings.ToLower(strings.TrimSpace(iface.InterfaceID))

	switch t {
	case "PHYSICAL", "UPLINK", "DATA", "DATAPATH", "FABRIC":
		return true
	case "MANAGEMENT", "MGMT", "LOOPBACK", "VIRTUAL", "TUNNEL":
		return false
	}

	// Fallback when interface_type is inconsistent/missing: keep common dataplane names.
	if strings.HasPrefix(id, "fp-") ||
		strings.HasPrefix(id, "eth") ||
		strings.HasPrefix(id, "vmnic") ||
		strings.HasPrefix(id, "pnic") ||
		strings.Contains(id, "uplink") ||
		strings.Contains(id, "dpdk") {
		return true
	}
	return false
}

func isEdgeNodeType(nodeType string) bool {
	nt := strings.ToLower(strings.TrimSpace(nodeType))
	return nt == "edgenode" || strings.Contains(nt, "edge")
}
