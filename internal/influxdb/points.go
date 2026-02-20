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
// parentT0Name should be set for TIER1 routers; empty string is fine for T0/VRF.
func LogicalRouterPoint(site, parentT0Name string, lr *nsx.LogicalRouter, now time.Time) *write.Point {
	tags := map[string]string{
		"site":        site,
		"router_id":   lr.ID,
		"router_name": lr.DisplayName,
		"router_type": lr.RouterType,
		"parent_t0":   parentT0Name,
	}
	return influxdb2.NewPoint(
		"nsx_logical_router",
		tags,
		map[string]interface{}{
			"up": int64(1),
		},
		now,
	)
}

// severityNum maps NSX alarm severity to a sortable integer (higher = more severe).
func severityNum(s string) int64 {
	switch s {
	case "CRITICAL":
		return 4
	case "HIGH":
		return 3
	case "MEDIUM":
		return 2
	case "LOW":
		return 1
	}
	return 0
}

// AlarmPoint converts an NSX active alarm to an InfluxDB point.
// measurement: nsx_alarm
// tags: site, alarm_id, severity, feature_name, node_name, event_type, summary
// fields: severity_num (int only — avoids pivot on string fields in Flux)
//
// event_type and summary are stored as tags so queries need no pivot:
//   filter _field == "severity_num" → last() → sort → keep tags for display.
// node_name defaults to "-" when empty so the tag is never dropped by InfluxDB.
func AlarmPoint(site string, alarm *nsx.Alarm, now time.Time) *write.Point {
	nodeName := alarm.NodeDisplayName
	if nodeName == "" {
		nodeName = "-"
	}
	return influxdb2.NewPoint(
		"nsx_alarm",
		map[string]string{
			"site":         site,
			"alarm_id":     alarm.ID,
			"severity":     alarm.Severity,
			"feature_name": alarm.FeatureName,
			"node_name":    nodeName,
			"event_type":   alarm.EventTypeDisplayName,
			"summary":      alarm.Summary,
		},
		map[string]interface{}{
			"severity_num": severityNum(alarm.Severity),
		},
		now,
	)
}

// CapacityPoint converts one NSX capacity usage entry to an InfluxDB point.
// measurement: nsx_capacity
// tags: site, usage_type, display_name
// fields: current_usage, max_supported, usage_pct
func CapacityPoint(site string, item *nsx.CapacityUsageItem, now time.Time) *write.Point {
	return influxdb2.NewPoint(
		"nsx_capacity",
		map[string]string{
			"site":         site,
			"usage_type":   item.UsageType,
			"display_name": item.DisplayName,
		},
		map[string]interface{}{
			"current_usage": item.CurrentUsageCount,
			"max_supported": item.MaxSupportedCount,
			"usage_pct":     item.CurrentUsagePercentage,
		},
		now,
	)
}

// EdgeUplinkStatsPoint converts interface stats for a physical Edge uplink to an InfluxDB point.
// All fields are cumulative counters — use derivative() in Flux to compute throughput rates.
// link_speed_mbps is the negotiated link speed in Mbps (0 = unknown/not connected).
func EdgeUplinkStatsPoint(site, nodeID, nodeName string, iface *nsx.NetworkInterface, stats *nsx.InterfaceStats, now time.Time) *write.Point {
	return influxdb2.NewPoint(
		"nsx_edge_uplink",
		map[string]string{
			"site":         site,
			"node_id":      nodeID,
			"node_name":    nodeName,
			"interface_id": iface.InterfaceID,
		},
		map[string]interface{}{
			"rx_bytes":        stats.RxBytes,
			"tx_bytes":        stats.TxBytes,
			"rx_packets":      stats.RxPackets,
			"tx_packets":      stats.TxPackets,
			"rx_dropped":      stats.RxDropped,
			"tx_dropped":      stats.TxDropped,
			"rx_errors":       stats.RxErrors,
			"tx_errors":       stats.TxErrors,
			"link_speed_mbps": iface.LinkSpeed,
		},
		now,
	)
}
