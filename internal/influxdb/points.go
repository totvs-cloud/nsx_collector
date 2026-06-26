package influxdb

import (
	"strings"
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

// ManagerStatusPoint writes the appliance uptime of one NSX Manager from a
// cluster. Tagged with site, manager_id (UUID) and manager_ip so each Manager
// can be plotted as a separate series.
func ManagerStatusPoint(site, managerID, managerIP string, ns *nsx.NodeStatus, now time.Time) *write.Point {
	if managerIP == "" {
		managerIP = "-"
	}
	return influxdb2.NewPoint(
		"nsx_manager",
		map[string]string{
			"site":       site,
			"manager_id": managerID,
			"manager_ip": managerIP,
		},
		map[string]interface{}{
			"uptime_ms": ns.SystemStatus.Uptime,
		},
		now,
	)
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

// severityNum maps an alarm severity to a sortable integer (higher = more severe).
// WARNING is an operational-only level introduced by the reclassification
// (see nsx.Alarm.OperationalSeverity); it sits just below HIGH.
func severityNum(s string) int64 {
	switch s {
	case "CRITICAL":
		return 4
	case "HIGH":
		return 3
	case "WARNING":
		return 2
	case "MEDIUM":
		return 2
	case "LOW":
		return 1
	}
	return 0
}

// AlarmPoint converts an NSX active alarm to an InfluxDB point.
// measurement: nsx_alarm
// tags: site, alarm_id, severity, severity_vendor, feature_name, node_name, event_type, summary
// fields: severity_num (int only — avoids pivot on string fields in Flux)
//
// The "severity" tag carries the operational severity from the approved
// reclassification (see nsx.Alarm.OperationalSeverity) so Grafana panels show the
// urgency the NOC actually wants; the raw vendor severity is preserved in
// "severity_vendor" for traceability. severity_num is derived from the operational
// severity so colour thresholds and sorting follow the reclassified level.
//
// event_type and summary are stored as tags so queries need no pivot:
//   filter _field == "severity_num" → last() → sort → keep tags for display.
// node_name defaults to "-" when empty so the tag is never dropped by InfluxDB.
func AlarmPoint(site string, alarm *nsx.Alarm, now time.Time) *write.Point {
	nodeName := alarm.NodeDisplayName
	if nodeName == "" {
		nodeName = "-"
	}
	severity := alarm.OperationalSeverity()
	return influxdb2.NewPoint(
		"nsx_alarm",
		map[string]string{
			"site":            site,
			"alarm_id":        alarm.ID,
			"severity":        severity,
			"severity_vendor": alarm.Severity,
			"feature_name":    alarm.FeatureName,
			"node_name":       nodeName,
			"event_type":      alarm.EventTypeDisplayName,
			"summary":         alarm.Summary,
		},
		map[string]interface{}{
			"severity_num": severityNum(severity),
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

// ---------------------------------------------------------------------------
// Load Balancer
// ---------------------------------------------------------------------------

// lbPoolStatusInt converts NSX pool status to a sortable integer.
// UP=2, PARTIALLY_UP=1, DOWN/UNKNOWN/other=0 — enables green/yellow/red thresholds.
func lbPoolStatusInt(s string) int64 {
	switch s {
	case "UP":
		return 2
	case "PARTIALLY_UP":
		return 1
	}
	return 0
}

// lbStatusInt maps NSX LB service/VS status to a sortable integer.
// UP/SUCCESS/NO_ALARM=2, DEGRADED/PARTIALLY_UP=1, DOWN/ERROR/DETACHED/other=0.
// Case-insensitive. VS statuses confirmed from API: UP, DOWN, PARTIALLY_UP.
func lbStatusInt(s string) int64 {
	switch strings.ToUpper(s) {
	case "UP", "SUCCESS", "NO_ALARM":
		return 2
	case "DEGRADED", "PARTIALLY_UP":
		return 1
	}
	return 0
}

// LBServicePoint converts an LB service and its status to an InfluxDB point.
// measurement: nsx_lb_service
// tags: site, service_id, service_name
// fields: status (UP/SUCCESS/NO_ALARM=2, DEGRADED=1, else=0), size (string)
// Note: size is a field, not a tag, to avoid series cardinality explosion when
// size is absent from older NSX API responses.
func LBServicePoint(site string, svc *nsx.LBService, status *nsx.LBServiceStatus, now time.Time) *write.Point {
	size := svc.Size
	if size == "" {
		size = "-"
	}
	return influxdb2.NewPoint(
		"nsx_lb_service",
		map[string]string{
			"site":         site,
			"service_id":   svc.ID,
			"service_name": svc.DisplayName,
		},
		map[string]interface{}{
			"status": lbStatusInt(status.ServiceStatus),
			"size":   size,
		},
		now,
	)
}

// LBVirtualServerPoint converts one VS status entry to an InfluxDB point.
// vsMap maps VS UUID → LBVirtualServer (for name/IP/port/protocol lookup).
// If the VS ID is not in the map, name/IP/port/protocol are left as "-".
// measurement: nsx_lb_virtual_server
// tags: site, service_id, vs_id, vs_name, ip_address, port, protocol
// fields: status (UP=2, DEGRADED=1, else=0)
func LBVirtualServerPoint(site, serviceID string, vsMap map[string]nsx.LBVirtualServer, vs nsx.LBVSStatus, now time.Time) *write.Point {
	vsName, ipAddr, port, proto := "-", "-", "-", "-"
	if meta, ok := vsMap[vs.VirtualServerID]; ok {
		vsName = meta.DisplayName
		ipAddr = meta.IPAddress
		proto = meta.IPProtocol
		if len(meta.Ports) > 0 {
			port = meta.Ports[0]
		}
	}
	return influxdb2.NewPoint(
		"nsx_lb_virtual_server",
		map[string]string{
			"site":       site,
			"service_id": serviceID,
			"vs_id":      vs.VirtualServerID,
			"vs_name":    vsName,
			"ip_address": ipAddr,
			"port":       port,
			"protocol":   proto,
		},
		map[string]interface{}{
			"status": lbStatusInt(vs.VirtualServerStatus),
		},
		now,
	)
}

// LBPoolPoint converts one pool status entry (with its members) to an InfluxDB point.
// poolMap maps pool UUID → LBPool (for name lookup).
// measurement: nsx_lb_pool
// tags: site, pool_id, pool_name
// fields: status (UP=2, PARTIALLY_UP=1, else=0), members_up, members_down, members_disabled
func LBPoolPoint(site string, poolMap map[string]nsx.LBPool, pool nsx.LBPoolStatus, now time.Time) *write.Point {
	poolName := "-"
	if meta, ok := poolMap[pool.PoolID]; ok {
		poolName = meta.DisplayName
	}

	var membersUp, membersDown, membersDisabled int64
	for _, m := range pool.Members {
		switch m.Status {
		case "UP":
			membersUp++
		case "DISABLED", "GRACEFUL_DISABLED":
			membersDisabled++
		default:
			membersDown++
		}
	}

	return influxdb2.NewPoint(
		"nsx_lb_pool",
		map[string]string{
			"site":      site,
			"pool_id":   pool.PoolID,
			"pool_name": poolName,
		},
		map[string]interface{}{
			"status":            lbPoolStatusInt(pool.PoolStatus),
			"members_up":        membersUp,
			"members_down":      membersDown,
			"members_disabled":  membersDisabled,
		},
		now,
	)
}

// ---------------------------------------------------------------------------
// HA state (T0/T1 Service Router role per transport node)
// ---------------------------------------------------------------------------

// haStateInt maps NSX HA status string to a sortable integer (higher = healthier).
// ACTIVE=2, STANDBY=1, anything else (DOWN/SYNC/UNKNOWN/empty)=0.
func haStateInt(s string) int64 {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "ACTIVE":
		return 2
	case "STANDBY":
		return 1
	}
	return 0
}

// HAStatePoint records the HA role of one (T1, transport_node) pair at a
// point in time. Tagging by t0_cluster_id allows aggregations per T0 cluster.
// tnName is the resolved edge appliance display_name (best-effort) so that
// dashboards can show human-readable edge identity instead of UUIDs.
// measurement: nsx_ha_state
// tags: site, t0_cluster_id, t0_name, t1_id, t1_name, transport_node_id,
//       transport_node_name, ha_state
// fields: state_num (int)
func HAStatePoint(site, t0ClusterID, t0Name, t1ID, t1Name, tnID, tnName, haState string, now time.Time) *write.Point {
	if t0Name == "" {
		t0Name = "-"
	}
	if tnID == "" {
		tnID = "-"
	}
	if tnName == "" {
		tnName = "-"
	}
	return influxdb2.NewPoint(
		"nsx_ha_state",
		map[string]string{
			"site":                site,
			"t0_cluster_id":       t0ClusterID,
			"t0_name":             t0Name,
			"t1_id":               t1ID,
			"t1_name":             t1Name,
			"transport_node_id":   tnID,
			"transport_node_name": tnName,
			"ha_state":            strings.ToUpper(strings.TrimSpace(haState)),
		},
		map[string]interface{}{
			"state_num": haStateInt(haState),
		},
		now,
	)
}

// HAClusterSummaryPoint records the consensus ACTIVE edge for one T0 cluster
// per cycle (how many of the N observed T1s share that ACTIVE node).
// consensusNodeName is the resolved edge display_name (best-effort).
// measurement: nsx_ha_cluster_summary
// tags: site, t0_cluster_id, t0_name, consensus_node_id, consensus_node_name
// fields: observed, consensus_count, outliers (= observed - consensus_count)
func HAClusterSummaryPoint(site, t0ClusterID, t0Name, consensusNodeID, consensusNodeName string, observed, consensusCount int, now time.Time) *write.Point {
	if t0Name == "" {
		t0Name = "-"
	}
	if consensusNodeID == "" {
		consensusNodeID = "-"
	}
	if consensusNodeName == "" {
		consensusNodeName = "-"
	}
	outliers := observed - consensusCount
	if outliers < 0 {
		outliers = 0
	}
	return influxdb2.NewPoint(
		"nsx_ha_cluster_summary",
		map[string]string{
			"site":                site,
			"t0_cluster_id":       t0ClusterID,
			"t0_name":             t0Name,
			"consensus_node_id":   consensusNodeID,
			"consensus_node_name": consensusNodeName,
		},
		map[string]interface{}{
			"observed":        int64(observed),
			"consensus_count": int64(consensusCount),
			"outliers":        int64(outliers),
		},
		now,
	)
}

// HAChangeEventPoint records a detected HA shift on a T0 cluster: the
// majority (>= ceil(observed/2)) of the observed T1s moved ACTIVE from
// from_active to to_active between two consecutive HA polls.
// fromActiveName/toActiveName are resolved edge display_names (best-effort)
// so the alert text and dashboard panels can show human-readable edges.
// measurement: nsx_ha_change
// tags: site, t0_cluster_id, t0_name, from_active, to_active,
//       from_active_name, to_active_name
// fields: changed_count, observed_count, changed_names (csv)
func HAChangeEventPoint(site, t0ClusterID, t0Name, fromActive, toActive, fromActiveName, toActiveName string, changedCount, observedCount int, changedNames []string, now time.Time) *write.Point {
	if t0Name == "" {
		t0Name = "-"
	}
	if fromActive == "" {
		fromActive = "-"
	}
	if toActive == "" {
		toActive = "-"
	}
	if fromActiveName == "" {
		fromActiveName = "-"
	}
	if toActiveName == "" {
		toActiveName = "-"
	}
	// Cap CSV at 500 chars to keep the point bounded.
	csv := strings.Join(changedNames, ",")
	if len(csv) > 500 {
		csv = csv[:500] + "…"
	}
	return influxdb2.NewPoint(
		"nsx_ha_change",
		map[string]string{
			"site":             site,
			"t0_cluster_id":    t0ClusterID,
			"t0_name":          t0Name,
			"from_active":      fromActive,
			"to_active":        toActive,
			"from_active_name": fromActiveName,
			"to_active_name":   toActiveName,
		},
		map[string]interface{}{
			"changed_count":  int64(changedCount),
			"observed_count": int64(observedCount),
			"changed_names":  csv,
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

// EdgeUplinkRatePoint writes pre-calculated bandwidth rates for an Edge uplink.
// Unlike EdgeUplinkStatsPoint (cumulative counters), these are ready-to-display
// rate values — no derivative() needed in Grafana.
func EdgeUplinkRatePoint(site, nodeID, nodeName, ifaceID string, rxBps, txBps, rxUtilPct, txUtilPct float64, linkSpeedMbps int64, now time.Time) *write.Point {
	return influxdb2.NewPoint(
		"nsx_edge_bandwidth",
		map[string]string{
			"site":         site,
			"node_id":      nodeID,
			"node_name":    nodeName,
			"interface_id": ifaceID,
		},
		map[string]interface{}{
			"rx_bps":              rxBps,
			"tx_bps":              txBps,
			"rx_utilization_pct":  rxUtilPct,
			"tx_utilization_pct":  txUtilPct,
			"link_speed_mbps":     linkSpeedMbps,
		},
		now,
	)
}

// ---------------------------------------------------------------------------
// LB credits (Policy API) — feeds the "LB credits" section of Capacity-NSX
// ---------------------------------------------------------------------------

// lbSeverityNum converts NSX LB severity strings (GREEN/ORANGE/RED) to a sortable
// integer for Grafana color thresholds. Distinct from the alarm severityNum
// scale (CRITICAL/HIGH/MEDIUM/LOW), which uses higher numbers for worse states
// — here we keep the same direction (higher = worse) but for a different domain.
func lbSeverityNum(s string) int64 {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "RED":
		return 3
	case "ORANGE":
		return 2
	case "GREEN":
		return 1
	}
	return 0
}

// LBCreditsManagerPoint records the manager-wide LB credit aggregate.
// measurement: nsx_lb_credits
// tags: site, scope=manager
// fields: used_credits, credit_capacity, available_credits, usage_pct,
//         severity_num, pool_members_used, pool_members_capacity,
//         nodes_green, nodes_orange, nodes_red
func LBCreditsManagerPoint(site string, s *nsx.LBNodeUsageSummary, now time.Time) *write.Point {
	var green, orange, red int64
	for _, nc := range s.NodeCounts {
		switch strings.ToUpper(strings.TrimSpace(nc.Severity)) {
		case "GREEN":
			green = int64(nc.NodeCount)
		case "ORANGE":
			orange = int64(nc.NodeCount)
		case "RED":
			red = int64(nc.NodeCount)
		}
	}
	avail := s.CreditCapacity - s.CurrentCredits
	if avail < 0 {
		avail = 0
	}
	return influxdb2.NewPoint(
		"nsx_lb_credits",
		map[string]string{
			"site":  site,
			"scope": "manager",
		},
		map[string]interface{}{
			"used_credits":          s.CurrentCredits,
			"credit_capacity":       s.CreditCapacity,
			"available_credits":     avail,
			"usage_pct":             s.UsagePercentage,
			"severity_num":          lbSeverityNum(s.Severity),
			"pool_members_used":     s.CurrentPoolMembers,
			"pool_members_capacity": s.PoolMemberCapacity,
			"nodes_green":           green,
			"nodes_orange":          orange,
			"nodes_red":             red,
		},
		now,
	)
}

// LBCreditsNodePoint records LB consumption for one edge node.
// measurement: nsx_lb_credits
// tags: site, scope=edge_node, node_id, edge_cluster_id, form_factor
// fields: used_credits, credit_capacity, available_credits, usage_pct,
//         severity_num, pool_members_used, virtual_servers, pools, pool_members_capacity,
//         small/medium/large/xlarge counts (current + remaining)
func LBCreditsNodePoint(site string, n *nsx.LBNodeUsage, now time.Time) *write.Point {
	nodeID := nsx.LastPathSegment(n.NodePath)
	edgeClusterID := nsx.LastPathSegment(n.EdgeClusterPath)
	if nodeID == "" {
		nodeID = "-"
	}
	if edgeClusterID == "" {
		edgeClusterID = "-"
	}
	formFactor := n.FormFactor
	if formFactor == "" {
		formFactor = "-"
	}
	avail := n.CreditCapacity - n.CurrentCredits
	if avail < 0 {
		avail = 0
	}
	return influxdb2.NewPoint(
		"nsx_lb_credits",
		map[string]string{
			"site":            site,
			"scope":           "edge_node",
			"node_id":         nodeID,
			"edge_cluster_id": edgeClusterID,
			"form_factor":     formFactor,
		},
		map[string]interface{}{
			"used_credits":           n.CurrentCredits,
			"credit_capacity":        n.CreditCapacity,
			"available_credits":      avail,
			"usage_pct":              n.UsagePercentage,
			"severity_num":           lbSeverityNum(n.Severity),
			"pool_members_used":      n.CurrentPoolMembers,
			"virtual_servers":        n.CurrentVirtualServers,
			"pools":                  n.CurrentPools,
			"pool_members_capacity":  n.PoolMemberCapacity,
			"small_lb_count":         n.CurrentSmall,
			"medium_lb_count":        n.CurrentMedium,
			"large_lb_count":         n.CurrentLarge,
			"xlarge_lb_count":        n.CurrentXLarge,
			"small_lb_remaining":     n.RemainingSmall,
			"medium_lb_remaining":    n.RemainingMedium,
			"large_lb_remaining":     n.RemainingLarge,
			"xlarge_lb_remaining":    n.RemainingXLarge,
		},
		now,
	)
}

// ---------------------------------------------------------------------------
// T1 aggregation per VRF / per T0 — fed by Policy API tier-1 inventory
// ---------------------------------------------------------------------------

// T1PerVRFPoint records the number of T1s under one VRF (T0 with vrf_config),
// along with the configured soft limit and computed usage_pct.
// measurement: nsx_t1_per_vrf
// tags: site, vrf_name, vrf_id, t0_parent
// fields: t1_count, limit, usage_pct, available
func T1PerVRFPoint(site, vrfName, vrfID, t0ParentName string, t1Count, limit int64, now time.Time) *write.Point {
	if vrfName == "" {
		vrfName = "-"
	}
	if vrfID == "" {
		vrfID = "-"
	}
	if t0ParentName == "" {
		t0ParentName = "-"
	}
	avail := limit - t1Count
	if avail < 0 {
		avail = 0
	}
	var pct float64
	if limit > 0 {
		pct = float64(t1Count) / float64(limit) * 100
	}
	return influxdb2.NewPoint(
		"nsx_t1_per_vrf",
		map[string]string{
			"site":      site,
			"vrf_name":  vrfName,
			"vrf_id":    vrfID,
			"t0_parent": t0ParentName,
		},
		map[string]interface{}{
			"t1_count":  t1Count,
			"limit":     limit,
			"usage_pct": pct,
			"available": avail,
		},
		now,
	)
}

// T1PerT0Point records the number of T1s directly attached to one T0 (non-VRF).
// measurement: nsx_t1_per_t0
// tags: site, t0_name, t0_id
// fields: t1_count, limit, usage_pct, available
func T1PerT0Point(site, t0Name, t0ID string, t1Count, limit int64, now time.Time) *write.Point {
	if t0Name == "" {
		t0Name = "-"
	}
	if t0ID == "" {
		t0ID = "-"
	}
	avail := limit - t1Count
	if avail < 0 {
		avail = 0
	}
	var pct float64
	if limit > 0 {
		pct = float64(t1Count) / float64(limit) * 100
	}
	return influxdb2.NewPoint(
		"nsx_t1_per_t0",
		map[string]string{
			"site":   site,
			"t0_name": t0Name,
			"t0_id":   t0ID,
		},
		map[string]interface{}{
			"t1_count":  t1Count,
			"limit":     limit,
			"usage_pct": pct,
			"available": avail,
		},
		now,
	)
}

// ---------------------------------------------------------------------------
// Segments aggregation per VRF / per T0 — fed by Policy API segments
// ---------------------------------------------------------------------------

// SegmentsPerParentPoint records segment counts grouped by parent (T1 or VRF or T0).
// measurement: nsx_segments_per_parent
// tags: site, parent_kind=t1|vrf|t0|overlay, parent_name, parent_id
// fields: segment_count
func SegmentsPerParentPoint(site, parentKind, parentName, parentID string, count int64, now time.Time) *write.Point {
	if parentName == "" {
		parentName = "-"
	}
	if parentID == "" {
		parentID = "-"
	}
	return influxdb2.NewPoint(
		"nsx_segments_per_parent",
		map[string]string{
			"site":        site,
			"parent_kind": parentKind,
			"parent_name": parentName,
			"parent_id":   parentID,
		},
		map[string]interface{}{
			"segment_count": count,
		},
		now,
	)
}

// ---------------------------------------------------------------------------
// NAT rules per T1 / per VRF
// ---------------------------------------------------------------------------

// NATPerT1Point records the NAT rule count of one Tier-1.
// measurement: nsx_nat_per_t1
// tags: site, t1_id, t1_name, parent_kind=vrf|t0, parent_name
// fields: nat_rules
func NATPerT1Point(site, t1ID, t1Name, parentKind, parentName string, count int64, now time.Time) *write.Point {
	if t1Name == "" {
		t1Name = "-"
	}
	if parentKind == "" {
		parentKind = "-"
	}
	if parentName == "" {
		parentName = "-"
	}
	return influxdb2.NewPoint(
		"nsx_nat_per_t1",
		map[string]string{
			"site":        site,
			"t1_id":       t1ID,
			"t1_name":     t1Name,
			"parent_kind": parentKind,
			"parent_name": parentName,
		},
		map[string]interface{}{
			"nat_rules": count,
		},
		now,
	)
}

// ---------------------------------------------------------------------------
// Gateway firewall rules per T1 / per VRF
// ---------------------------------------------------------------------------

// FWPerGatewayPoint records the firewall rule count attributed to one gateway
// (T1 or T0/VRF) via gateway-policies.scope. One series per (scope_path).
// measurement: nsx_fw_per_gateway
// tags: site, gateway_kind=t1|vrf|t0, gateway_name, gateway_id
// fields: fw_rules, fw_policies
func FWPerGatewayPoint(site, gatewayKind, gatewayName, gatewayID string, ruleCount, policyCount int64, now time.Time) *write.Point {
	if gatewayName == "" {
		gatewayName = "-"
	}
	if gatewayID == "" {
		gatewayID = "-"
	}
	return influxdb2.NewPoint(
		"nsx_fw_per_gateway",
		map[string]string{
			"site":         site,
			"gateway_kind": gatewayKind,
			"gateway_name": gatewayName,
			"gateway_id":   gatewayID,
		},
		map[string]interface{}{
			"fw_rules":    ruleCount,
			"fw_policies": policyCount,
		},
		now,
	)
}

// ---------------------------------------------------------------------------
// Groups inventory (for waste/orphan analysis) — fed by Policy API groups
// ---------------------------------------------------------------------------

// GroupsInventoryPoint records the total + orphan groups (no expression).
// measurement: nsx_groups_inventory
// tags: site
// fields: total, empty (no expression), with_expression
func GroupsInventoryPoint(site string, total, empty int64, now time.Time) *write.Point {
	withExpr := total - empty
	if withExpr < 0 {
		withExpr = 0
	}
	return influxdb2.NewPoint(
		"nsx_groups_inventory",
		map[string]string{"site": site},
		map[string]interface{}{
			"total":           total,
			"empty":           empty,
			"with_expression": withExpr,
		},
		now,
	)
}

// ---------------------------------------------------------------------------
// T1 lifecycle events (create / delete) — fed by t1watch detector
// ---------------------------------------------------------------------------

// T1EventPoint records one T1 lifecycle event detected by the t1watch
// diff between consecutive collection cycles.
// measurement: nsx_t1_event
// tags: site, event=created|deleted, t1_id, t1_name, vrf_name, edge_cluster_name
// fields: vrf_t1_count (post-event), vrf_limit, site_t1_total, count=1
func T1EventPoint(site, event, t1ID, t1Name, vrfName, edgeClusterName string, vrfT1Count, vrfLimit, siteT1Total int64, now time.Time) *write.Point {
	if t1Name == "" {
		t1Name = "-"
	}
	if vrfName == "" {
		vrfName = "-"
	}
	if edgeClusterName == "" {
		edgeClusterName = "-"
	}
	return influxdb2.NewPoint(
		"nsx_t1_event",
		map[string]string{
			"site":              site,
			"event":             event,
			"t1_id":             t1ID,
			"t1_name":           t1Name,
			"vrf_name":          vrfName,
			"edge_cluster_name": edgeClusterName,
		},
		map[string]interface{}{
			"count":         int64(1),
			"vrf_t1_count":  vrfT1Count,
			"vrf_limit":     vrfLimit,
			"site_t1_total": siteT1Total,
		},
		now,
	)
}

// SiteT1TotalsPoint records the site-level T1 inventory snapshot per cycle.
// Used by Grafana stat panels and by the t1watch notifier output ("de um total
// de XXXX t1 no <site>"). Lower cost than scanning nsx_logical_router for sums.
// measurement: nsx_t1_totals
// tags: site
// fields: total, on_vrf, on_t0
func SiteT1TotalsPoint(site string, total, onVRF, onT0 int64, now time.Time) *write.Point {
	return influxdb2.NewPoint(
		"nsx_t1_totals",
		map[string]string{"site": site},
		map[string]interface{}{
			"total":  total,
			"on_vrf": onVRF,
			"on_t0":  onT0,
		},
		now,
	)
}
