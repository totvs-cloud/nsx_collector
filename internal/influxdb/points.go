package influxdb

import (
	"time"

	"github.com/influxdata/influxdb-client-go/v2/api/write"
	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"nsx-collector/internal/nsx"
)

func statusInt(s, expected string) int64 {
	if s == expected {
		return 1
	}
	return 0
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// ClusterStatusPoint converts NSX cluster status to an InfluxDB point.
func ClusterStatusPoint(site string, cs *nsx.ClusterStatus, now time.Time) *write.Point {
	return influxdb2.NewPoint(
		"nsx_cluster",
		map[string]string{
			"site":       site,
			"cluster_id": cs.ClusterID,
		},
		map[string]interface{}{
			"mgmt_status":    statusInt(cs.MgmtClusterStatus.Status, "STABLE"),
			"control_status": statusInt(cs.ControlClusterStatus.Status, "STABLE"),
			"overall_status": statusInt(cs.DetailedClusterStatus.OverallStatus, "STABLE"),
			"online_nodes":   int64(len(cs.MgmtClusterStatus.OnlineNodes)),
			"offline_nodes":  int64(len(cs.MgmtClusterStatus.OfflineNodes)),
		},
		now,
	)
}

// TransportNodeStatusPoints converts a transport node status to InfluxDB points.
// Returns one nsx_transport_node point, and optionally one nsx_edge_resource point for edge nodes.
func TransportNodeStatusPoints(site, nodeID, nodeName, nodeType string, ts *nsx.TransportNodeStatus, now time.Time) []*write.Point {
	baseTags := map[string]string{
		"site":      site,
		"node_id":   nodeID,
		"node_name": nodeName,
		"node_type": nodeType,
	}

	tnPoint := influxdb2.NewPoint(
		"nsx_transport_node",
		baseTags,
		map[string]interface{}{
			"status":        statusInt(ts.Status, "UP"),
			"pnic_up":       int64(ts.PnicStatus.UpCount),
			"pnic_down":     int64(ts.PnicStatus.DownCount),
			"tunnel_up":     int64(ts.TunnelStatus.UpCount),
			"tunnel_down":   int64(ts.TunnelStatus.DownCount),
			"bfd_up":        int64(ts.TunnelStatus.BfdStatus.BfdUpCount),
			"bfd_down":      int64(ts.TunnelStatus.BfdStatus.BfdDownCount),
			"bfd_admin_down": int64(ts.TunnelStatus.BfdStatus.BfdAdminDownCount),
			"mgmt_conn":     statusInt(ts.MgmtConnectionStatus, "UP"),
			"control_conn":  statusInt(ts.ControlConnectionStatus.Status, "UP"),
		},
		now,
	)

	points := []*write.Point{tnPoint}

	// Edge-specific resource metrics
	if nodeType == "EdgeNode" {
		sys := ts.NodeStatus.SystemStatus
		var loadAvg1m, loadAvg5m, loadAvg15m float64
		if len(sys.LoadAverage) >= 3 {
			loadAvg1m = sys.LoadAverage[0]
			loadAvg5m = sys.LoadAverage[1]
			loadAvg15m = sys.LoadAverage[2]
		}

		diskUsedPct := 0.0
		if sys.DiskSpaceTotal > 0 {
			diskUsedPct = float64(sys.DiskSpaceUsed) / float64(sys.DiskSpaceTotal) * 100.0
		}

		edgePoint := influxdb2.NewPoint(
			"nsx_edge_resource",
			baseTags,
			map[string]interface{}{
				"cpu_dpdk_avg":           sys.CPUUsage.AvgCPUCoreDPDK,
				"cpu_dpdk_peak":          sys.CPUUsage.HighestCPUCoreDPDK,
				"cpu_non_dpdk_avg":       sys.CPUUsage.AvgCPUCoreNonDPDK,
				"cpu_non_dpdk_peak":      sys.CPUUsage.HighestCPUCoreNonDPDK,
				"mem_system_pct":         sys.EdgeMemUsage.SystemMemUsage,
				"mem_datapath_pct":       sys.EdgeMemUsage.DatapathTotalUsage,
				"mem_datapath_pool_peak": sys.EdgeMemUsage.DatapathMemUsageDetails.HighestDatapathMemPoolUsage,
				"mem_total_kb":           sys.MemTotal,
				"mem_used_kb":            sys.MemUsed,
				"disk_total_kb":          sys.DiskSpaceTotal,
				"disk_used_kb":           sys.DiskSpaceUsed,
				"disk_used_pct":          diskUsedPct,
				"load_avg_1m":            loadAvg1m,
				"load_avg_5m":            loadAvg5m,
				"load_avg_15m":           loadAvg15m,
				"uptime_ms":              sys.Uptime,
				"cpu_cores":              int64(sys.CPUCores),
			},
			now,
		)
		points = append(points, edgePoint)
	}

	return points
}

// LogicalRouterPoint converts a logical router to an InfluxDB point.
// Used for counting and tracking T0/T1/VRF inventory.
func LogicalRouterPoint(site string, lr *nsx.LogicalRouter, now time.Time) *write.Point {
	return influxdb2.NewPoint(
		"nsx_logical_router",
		map[string]string{
			"site":        site,
			"router_id":   lr.ID,
			"router_name": lr.DisplayName,
			"router_type": lr.RouterType,
		},
		map[string]interface{}{
			"up": int64(1),
		},
		now,
	)
}

// BGPNeighborPoint converts a BGP neighbor status to an InfluxDB point.
func BGPNeighborPoint(site, routerID, routerName string, n *nsx.BGPNeighborStatus, now time.Time) *write.Point {
	return influxdb2.NewPoint(
		"nsx_bgp_neighbor",
		map[string]string{
			"site":        site,
			"router_id":   routerID,
			"router_name": routerName,
			"neighbor_ip": n.NeighborAddress,
		},
		map[string]interface{}{
			"established":  statusInt(n.ConnectionState, "ESTABLISHED"),
			"prefixes_rx":  n.TotalInPrefixCount,
			"prefixes_tx":  n.TotalOutPrefixCount,
			"uptime_s":     n.TimeEstablished,
		},
		now,
	)
}
