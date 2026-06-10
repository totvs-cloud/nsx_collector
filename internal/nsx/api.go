package nsx

import (
	"context"
	"fmt"
	"net/url"
)

// GetClusterNodeStatus returns the appliance status of a specific cluster node
// via /api/v1/cluster/nodes/<id>/status (the request is proxied to that node by
// the Manager that fronts the VIP), so we can collect uptime per Manager.
func (c *Client) GetClusterNodeStatus(ctx context.Context, nodeID string) (*NodeStatus, error) {
	var result NodeStatus
	if err := c.doGet(ctx, "/api/v1/cluster/nodes/"+nodeID+"/status", &result); err != nil {
		return nil, fmt.Errorf("cluster node %s status: %w", nodeID, err)
	}
	return &result, nil
}

// GetClusterStatus returns the overall NSX cluster status.
func (c *Client) GetClusterStatus(ctx context.Context) (*ClusterStatus, error) {
	var result ClusterStatus
	if err := c.doGet(ctx, "/api/v1/cluster/status", &result); err != nil {
		return nil, fmt.Errorf("cluster status: %w", err)
	}
	return &result, nil
}

// GetTransportNodes returns all transport nodes, paginating automatically.
func (c *Client) GetTransportNodes(ctx context.Context) ([]TransportNodeItem, error) {
	var all []TransportNodeItem
	cursor := ""
	for {
		path := "/api/v1/transport-nodes?page_size=100"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page TransportNodeList
		if err := c.doGet(ctx, path, &page); err != nil {
			return nil, fmt.Errorf("transport nodes: %w", err)
		}
		all = append(all, page.Results...)
		if page.Cursor == "" || len(page.Results) == 0 {
			break
		}
		cursor = page.Cursor
	}
	return all, nil
}

// GetTransportNodeStatus returns the detailed status of a single transport node.
func (c *Client) GetTransportNodeStatus(ctx context.Context, nodeID string) (*TransportNodeStatus, error) {
	var result TransportNodeStatus
	if err := c.doGet(ctx, "/api/v1/transport-nodes/"+nodeID+"/status", &result); err != nil {
		return nil, fmt.Errorf("transport node %s status: %w", nodeID, err)
	}
	return &result, nil
}

// GetLogicalRouters returns all logical routers, paginating automatically.
func (c *Client) GetLogicalRouters(ctx context.Context) ([]LogicalRouter, error) {
	var all []LogicalRouter
	cursor := ""
	for {
		path := "/api/v1/logical-routers?page_size=100"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page LogicalRouterList
		if err := c.doGet(ctx, path, &page); err != nil {
			return nil, fmt.Errorf("logical routers: %w", err)
		}
		all = append(all, page.Results...)
		if page.Cursor == "" || len(page.Results) == 0 {
			break
		}
		cursor = page.Cursor
	}
	return all, nil
}

// GetLogicalRouterStatus returns the per-transport-node HA state of one
// logical router (T0/T1 with SR). The legacy /api/v1/logical-routers/<id>/status
// endpoint is the only HA-state source that works on NSX versions where
// /policy/.../locale-services/<ls>/state returns 404.
func (c *Client) GetLogicalRouterStatus(ctx context.Context, lrID string) (*LogicalRouterStatus, error) {
	var result LogicalRouterStatus
	if err := c.doGet(ctx, "/api/v1/logical-routers/"+lrID+"/status", &result); err != nil {
		return nil, fmt.Errorf("logical router %s status: %w", lrID, err)
	}
	return &result, nil
}

// GetLogicalRouterPorts returns all logical router ports, paginating automatically.
// Used to build the T1→T0 parent mapping via LinkedRouterPort entries.
func (c *Client) GetLogicalRouterPorts(ctx context.Context) ([]LogicalRouterPort, error) {
	var all []LogicalRouterPort
	cursor := ""
	for {
		path := "/api/v1/logical-router-ports?page_size=100"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page LogicalRouterPortList
		if err := c.doGet(ctx, path, &page); err != nil {
			return nil, fmt.Errorf("logical router ports: %w", err)
		}
		all = append(all, page.Results...)
		if page.Cursor == "" || len(page.Results) == 0 {
			break
		}
		cursor = page.Cursor
	}
	return all, nil
}

// GetTransportNodeInterfaces returns all network interfaces of a transport node.
func (c *Client) GetTransportNodeInterfaces(ctx context.Context, nodeID string) ([]NetworkInterface, error) {
	var result NetworkInterfaceList
	if err := c.doGet(ctx, "/api/v1/transport-nodes/"+nodeID+"/network/interfaces", &result); err != nil {
		return nil, fmt.Errorf("interfaces for node %s: %w", nodeID, err)
	}
	return result.Results, nil
}

// GetTransportNodeInterfaceStats returns cumulative byte/packet counters for one interface.
func (c *Client) GetTransportNodeInterfaceStats(ctx context.Context, nodeID, ifID string) (*InterfaceStats, error) {
	var result InterfaceStats
	path := "/api/v1/transport-nodes/" + nodeID + "/network/interfaces/" + ifID + "/stats"
	if err := c.doGet(ctx, path, &result); err != nil {
		return nil, fmt.Errorf("interface stats for node %s iface %s: %w", nodeID, ifID, err)
	}
	return &result, nil
}

// GetCapacityUsage returns all capacity usage entries from GET /api/v1/capacity/usage.
func (c *Client) GetCapacityUsage(ctx context.Context) ([]CapacityUsageItem, error) {
	var result CapacityUsageResponse
	if err := c.doGet(ctx, "/api/v1/capacity/usage", &result); err != nil {
		return nil, fmt.Errorf("capacity usage: %w", err)
	}
	return result.CapacityUsage, nil
}

// GetNSServicesCount returns the total count of NS service objects via GET /api/v1/ns-services.
func (c *Client) GetNSServicesCount(ctx context.Context) (int64, error) {
	var result struct {
		ResultCount int64 `json:"result_count"`
	}
	if err := c.doGet(ctx, "/api/v1/ns-services?page_size=1", &result); err != nil {
		return 0, fmt.Errorf("ns-services count: %w", err)
	}
	return result.ResultCount, nil
}

// GetActiveAlarms returns all OPEN alarms from the NSX Manager, paginating automatically.
func (c *Client) GetActiveAlarms(ctx context.Context) ([]Alarm, error) {
	var all []Alarm
	cursor := ""
	for {
		path := "/api/v1/alarms?status=OPEN&page_size=100"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page AlarmList
		if err := c.doGet(ctx, path, &page); err != nil {
			return nil, fmt.Errorf("alarms: %w", err)
		}
		all = append(all, page.Results...)
		if page.Cursor == "" || len(page.Results) == 0 {
			break
		}
		cursor = page.Cursor
	}
	return all, nil
}

// ---------------------------------------------------------------------------
// Load Balancer
// ---------------------------------------------------------------------------

// GetLBServices returns all LB service entries (ID + name), paginating automatically.
func (c *Client) GetLBServices(ctx context.Context) ([]LBService, error) {
	var all []LBService
	cursor := ""
	for {
		path := "/api/v1/loadbalancer/services?page_size=100"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page LBServiceList
		if err := c.doGet(ctx, path, &page); err != nil {
			return nil, fmt.Errorf("lb services: %w", err)
		}
		all = append(all, page.Results...)
		if page.Cursor == "" || len(page.Results) == 0 {
			break
		}
		cursor = page.Cursor
	}
	return all, nil
}

// GetLBVirtualServers returns all LB virtual server entries (ID, name, IP, ports, protocol),
// paginating automatically. Used to build a name-resolution map for status points.
func (c *Client) GetLBVirtualServers(ctx context.Context) ([]LBVirtualServer, error) {
	var all []LBVirtualServer
	cursor := ""
	for {
		path := "/api/v1/loadbalancer/virtual-servers?page_size=100"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page LBVirtualServerList
		if err := c.doGet(ctx, path, &page); err != nil {
			return nil, fmt.Errorf("lb virtual servers: %w", err)
		}
		all = append(all, page.Results...)
		if page.Cursor == "" || len(page.Results) == 0 {
			break
		}
		cursor = page.Cursor
	}
	return all, nil
}

// GetLBPools returns all LB server pool entries (ID + name), paginating automatically.
// Used to build a name-resolution map for pool status points.
func (c *Client) GetLBPools(ctx context.Context) ([]LBPool, error) {
	var all []LBPool
	cursor := ""
	for {
		path := "/api/v1/loadbalancer/pools?page_size=100"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page LBPoolList
		if err := c.doGet(ctx, path, &page); err != nil {
			return nil, fmt.Errorf("lb pools: %w", err)
		}
		all = append(all, page.Results...)
		if page.Cursor == "" || len(page.Results) == 0 {
			break
		}
		cursor = page.Cursor
	}
	return all, nil
}

// GetLBServiceStatus returns the full health status for one LB service, including
// the status of all its virtual servers and pool members.
func (c *Client) GetLBServiceStatus(ctx context.Context, serviceID string) (*LBServiceStatus, error) {
	var result LBServiceStatus
	path := "/api/v1/loadbalancer/services/" + serviceID + "/status"
	if err := c.doGet(ctx, path, &result); err != nil {
		return nil, fmt.Errorf("lb service status %s: %w", serviceID, err)
	}
	return &result, nil
}

// ---------------------------------------------------------------------------
// Policy API — LB credits, Tier-0/1, Segments, NAT, GW Policies, Edge Clusters, Groups
// ---------------------------------------------------------------------------

// GetLBNodeUsageSummary returns LB credit usage aggregated (manager scope) plus
// per-edge-node detail (when include_usages=true). One JSON, one round-trip.
// Required for the "Capacity NSX" panel — not exposed by /api/v1/capacity/usage.
func (c *Client) GetLBNodeUsageSummary(ctx context.Context) (*LBNodeUsageSummary, error) {
	var result LBNodeUsageSummaryResponse
	if err := c.doGet(ctx, "/policy/api/v1/infra/lb-node-usage-summary?include_usages=true", &result); err != nil {
		return nil, fmt.Errorf("lb node usage summary: %w", err)
	}
	if len(result.Results) == 0 {
		return nil, nil
	}
	r := result.Results[0]
	return &r, nil
}

// GetPolicyTier0s lists all Policy API Tier-0 gateways (regular + VRF).
// VRFs are distinguished by PolicyTier0.IsVRF() (vrf_config presence).
func (c *Client) GetPolicyTier0s(ctx context.Context) ([]PolicyTier0, error) {
	var all []PolicyTier0
	cursor := ""
	for {
		path := "/policy/api/v1/infra/tier-0s?page_size=200"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page PolicyTier0List
		if err := c.doGet(ctx, path, &page); err != nil {
			return nil, fmt.Errorf("policy tier-0s: %w", err)
		}
		all = append(all, page.Results...)
		if page.Cursor == "" || len(page.Results) == 0 {
			break
		}
		cursor = page.Cursor
	}
	return all, nil
}

// GetPolicyTier1s lists all Policy API Tier-1 gateways with their tier0_path
// (which points to either a regular T0 or a VRF — see PolicyTier0.IsVRF).
func (c *Client) GetPolicyTier1s(ctx context.Context) ([]PolicyTier1, error) {
	var all []PolicyTier1
	cursor := ""
	for {
		path := "/policy/api/v1/infra/tier-1s?page_size=200"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page PolicyTier1List
		if err := c.doGet(ctx, path, &page); err != nil {
			return nil, fmt.Errorf("policy tier-1s: %w", err)
		}
		all = append(all, page.Results...)
		if page.Cursor == "" || len(page.Results) == 0 {
			break
		}
		cursor = page.Cursor
	}
	return all, nil
}

// GetPolicySegments lists all Policy API segments. connectivity_path is the
// link to either a T1 or T0 — used for "segments per VRF/T0".
func (c *Client) GetPolicySegments(ctx context.Context) ([]PolicySegment, error) {
	var all []PolicySegment
	cursor := ""
	for {
		path := "/policy/api/v1/infra/segments?page_size=500"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page PolicySegmentList
		if err := c.doGet(ctx, path, &page); err != nil {
			return nil, fmt.Errorf("policy segments: %w", err)
		}
		all = append(all, page.Results...)
		if page.Cursor == "" || len(page.Results) == 0 {
			break
		}
		cursor = page.Cursor
	}
	return all, nil
}

// GetTier1NATRuleCount returns the number of USER NAT rules on one Tier-1.
// Cheap because page_size=1 still populates result_count.
func (c *Client) GetTier1NATRuleCount(ctx context.Context, tier1ID string) (int, error) {
	var result PolicyNATRuleList
	path := "/policy/api/v1/infra/tier-1s/" + tier1ID + "/nat/USER/nat-rules?page_size=1"
	if err := c.doGet(ctx, path, &result); err != nil {
		return 0, fmt.Errorf("nat rules count for t1 %s: %w", tier1ID, err)
	}
	return result.ResultCount, nil
}

// GetGatewayPolicies lists all gateway firewall policies with rule_count populated.
// Used to attribute firewall rules to specific T1/T0 gateways via Scope.
func (c *Client) GetGatewayPolicies(ctx context.Context) ([]PolicyGatewayPolicy, error) {
	var all []PolicyGatewayPolicy
	cursor := ""
	for {
		path := "/policy/api/v1/infra/domains/default/gateway-policies?include_rule_count=true&page_size=200"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page PolicyGatewayPolicyList
		if err := c.doGet(ctx, path, &page); err != nil {
			return nil, fmt.Errorf("gateway policies: %w", err)
		}
		all = append(all, page.Results...)
		if page.Cursor == "" || len(page.Results) == 0 {
			break
		}
		cursor = page.Cursor
	}
	return all, nil
}

// GetPolicyEdgeClusters lists all Policy API edge clusters under the default
// site/enforcement-point. nsx_id matches the legacy logical-router.edge_cluster_id
// — needed to resolve T1.edge_cluster_id → human-readable cluster display_name
// for the Slack T1-created message ("criado no CLUSTER <name>").
func (c *Client) GetPolicyEdgeClusters(ctx context.Context) ([]PolicyEdgeCluster, error) {
	var all []PolicyEdgeCluster
	cursor := ""
	for {
		path := "/policy/api/v1/infra/sites/default/enforcement-points/default/edge-clusters?page_size=100"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page PolicyEdgeClusterList
		if err := c.doGet(ctx, path, &page); err != nil {
			return nil, fmt.Errorf("policy edge clusters: %w", err)
		}
		all = append(all, page.Results...)
		if page.Cursor == "" || len(page.Results) == 0 {
			break
		}
		cursor = page.Cursor
	}
	return all, nil
}

// GetPolicyGroups lists all groups in the default domain. Used to count
// groups with empty Expression (potential orphans).
func (c *Client) GetPolicyGroups(ctx context.Context) ([]PolicyGroup, error) {
	var all []PolicyGroup
	cursor := ""
	for {
		path := "/policy/api/v1/infra/domains/default/groups?page_size=500"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page PolicyGroupList
		if err := c.doGet(ctx, path, &page); err != nil {
			return nil, fmt.Errorf("policy groups: %w", err)
		}
		all = append(all, page.Results...)
		if page.Cursor == "" || len(page.Results) == 0 {
			break
		}
		cursor = page.Cursor
	}
	return all, nil
}

