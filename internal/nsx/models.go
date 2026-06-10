package nsx

import "encoding/json"

// NodeStatus represents GET /api/v1/cluster/nodes/<id>/status — appliance
// status of one Manager (or Controller) cluster node, returned as a
// ClusterNodeStatus by NSX. Uptime here is nested under system_status, unlike
// /api/v1/node/status which returns the same fields at the root.
type NodeStatus struct {
	SystemStatus struct {
		Uptime int64 `json:"uptime"` // milliseconds since last reboot
	} `json:"system_status"`
}

// ClusterStatus represents GET /api/v1/cluster/status
type ClusterStatus struct {
	ClusterID         string `json:"cluster_id"`
	MgmtClusterStatus struct {
		Status       string        `json:"status"`
		OnlineNodes  []ClusterNode `json:"online_nodes"`
		OfflineNodes []ClusterNode `json:"offline_nodes"`
	} `json:"mgmt_cluster_status"`
	ControlClusterStatus struct {
		Status string `json:"status"`
	} `json:"control_cluster_status"`
	DetailedClusterStatus struct {
		OverallStatus string `json:"overall_status"`
	} `json:"detailed_cluster_status"`
}

// ClusterNode is a node entry in cluster status.
type ClusterNode struct {
	UUID                       string `json:"uuid"`
	MgmtClusterListenIPAddress string `json:"mgmt_cluster_listen_ip_address"`
}

// TransportNodeList represents GET /api/v1/transport-nodes
type TransportNodeList struct {
	ResultCount int               `json:"result_count"`
	Cursor      string            `json:"cursor"`
	Results     []TransportNodeItem `json:"results"`
}

// TransportNodeItem is one entry in the transport nodes list.
type TransportNodeItem struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	NodeID      string `json:"node_id"`
	NodeDeploymentInfo struct {
		ResourceType string `json:"resource_type"` // EdgeNode | HostNode
		DisplayName  string `json:"display_name"`
		ID           string `json:"id"`
	} `json:"node_deployment_info"`
}

// TransportNodeStatus represents GET /api/v1/transport-nodes/{id}/status
type TransportNodeStatus struct {
	NodeUUID        string `json:"node_uuid"`
	NodeDisplayName string `json:"node_display_name"`
	Status          string `json:"status"` // UP | DOWN | DEGRADED

	PnicStatus struct {
		Status    string `json:"status"`
		UpCount   int    `json:"up_count"`
		DownCount int    `json:"down_count"`
	} `json:"pnic_status"`

	MgmtConnectionStatus string `json:"mgmt_connection_status"`

	ControlConnectionStatus struct {
		Status    string `json:"status"`
		UpCount   int    `json:"up_count"`
		DownCount int    `json:"down_count"`
	} `json:"control_connection_status"`

	TunnelStatus struct {
		Status    string `json:"status"`
		UpCount   int    `json:"up_count"`
		DownCount int    `json:"down_count"`
		BfdStatus struct {
			BfdAdminDownCount int `json:"bfd_admin_down_count"`
			BfdDownCount      int `json:"bfd_down_count"`
			BfdInitCount      int `json:"bfd_init_count"`
			BfdUpCount        int `json:"bfd_up_count"`
		} `json:"bfd_status"`
	} `json:"tunnel_status"`

	NodeStatus struct {
		HostNodeDeploymentStatus string `json:"host_node_deployment_status"`
		SoftwareVersion          string `json:"software_version"`
		MaintenanceMode          string `json:"maintenance_mode"`
		SystemStatus             struct {
			CPUCores    int       `json:"cpu_cores"`
			LoadAverage []float64 `json:"load_average"`
			MemTotal    int64     `json:"mem_total"`
			MemUsed     int64     `json:"mem_used"`
			DiskSpaceTotal int64  `json:"disk_space_total"`
			DiskSpaceUsed  int64  `json:"disk_space_used"`
			Uptime      int64     `json:"uptime"` // milliseconds
			CPUUsage    struct {
				HighestCPUCoreDPDK    float64 `json:"highest_cpu_core_usage_dpdk"`
				AvgCPUCoreDPDK        float64 `json:"avg_cpu_core_usage_dpdk"`
				HighestCPUCoreNonDPDK float64 `json:"highest_cpu_core_usage_non_dpdk"`
				AvgCPUCoreNonDPDK     float64 `json:"avg_cpu_core_usage_non_dpdk"`
			} `json:"cpu_usage"`
			EdgeMemUsage struct {
				SystemMemUsage    float64 `json:"system_mem_usage"`
				SwapUsage         float64 `json:"swap_usage"`
				CacheUsage        float64 `json:"cache_usage"`
				DatapathTotalUsage float64 `json:"datapath_total_usage"`
				DatapathMemUsageDetails struct {
					HighestDatapathMemPoolUsage float64 `json:"highest_datapath_mem_pool_usage"`
				} `json:"datapath_mem_usage_details"`
			} `json:"edge_mem_usage"`
		} `json:"system_status"`
	} `json:"node_status"`
}

// LogicalRouterList represents GET /api/v1/logical-routers
type LogicalRouterList struct {
	ResultCount int             `json:"result_count"`
	Cursor      string          `json:"cursor"`
	Results     []LogicalRouter `json:"results"`
}

// LogicalRouter is one logical router entry.
type LogicalRouter struct {
	ID                     string `json:"id"`
	DisplayName            string `json:"display_name"`
	RouterType             string `json:"router_type"` // TIER0 | TIER1 | VRF
	EdgeClusterID          string `json:"edge_cluster_id,omitempty"`
	HighAvailabilityMode   string `json:"high_availability_mode,omitempty"` // ACTIVE_STANDBY | ACTIVE_ACTIVE
	FailoverMode           string `json:"failover_mode,omitempty"`          // PREEMPTIVE | NON_PREEMPTIVE
}

// LogicalRouterStatus represents GET /api/v1/logical-routers/{id}/status.
// per_node_status carries the HA role of this router's Service Router on each
// edge transport node (ACTIVE/STANDBY/DOWN/SYNC/UNKNOWN).
type LogicalRouterStatus struct {
	LogicalRouterID     string             `json:"logical_router_id"`
	LastUpdateTimestamp int64              `json:"last_update_timestamp"` // epoch ms
	PerNodeStatus       []PerNodeHAStatus  `json:"per_node_status"`
}

// PerNodeHAStatus is one transport_node entry inside LogicalRouterStatus.
type PerNodeHAStatus struct {
	TransportNodeID        string `json:"transport_node_id"`
	ServiceRouterID        string `json:"service_router_id,omitempty"`
	HighAvailabilityStatus string `json:"high_availability_status"` // ACTIVE | STANDBY | DOWN | SYNC | UNKNOWN
	IsDefaultSubCluster    bool   `json:"is_default_sub_cluster,omitempty"`
}

// LogicalRouterPortList represents GET /api/v1/logical-router-ports
type LogicalRouterPortList struct {
	ResultCount int                  `json:"result_count"`
	Cursor      string               `json:"cursor"`
	Results     []LogicalRouterPort  `json:"results"`
}

// LogicalRouterPort is a port attached to a logical router.
// linked_logical_router_port_id is polymorphic: some NSX versions return a plain
// string UUID, others return a ResourceReference object with target_id.
type LogicalRouterPort struct {
	ID              string          `json:"id"`
	LogicalRouterID string          `json:"logical_router_id"`
	ResourceType    string          `json:"resource_type"`
	LinkedPortRaw   json.RawMessage `json:"linked_logical_router_port_id"`
}

// LinkedPortID extracts the peer port UUID regardless of whether the field
// was serialized as a plain string or as a {"target_id": "..."} object.
func (p *LogicalRouterPort) LinkedPortID() string {
	if len(p.LinkedPortRaw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(p.LinkedPortRaw, &s); err == nil {
		return s
	}
	var ref struct {
		TargetID string `json:"target_id"`
	}
	if err := json.Unmarshal(p.LinkedPortRaw, &ref); err == nil {
		return ref.TargetID
	}
	return ""
}

// NetworkInterfaceList represents GET /api/v1/transport-nodes/{id}/network/interfaces
type NetworkInterfaceList struct {
	ResultCount int                `json:"result_count"`
	Results     []NetworkInterface `json:"results"`
}

// NetworkInterface is one interface entry on a transport node.
type NetworkInterface struct {
	InterfaceID   string `json:"interface_id"`   // e.g. fp-eth0, eth0
	InterfaceType string `json:"interface_type"` // PHYSICAL | MANAGEMENT | VIRTUAL
	AdminStatus   string `json:"admin_status"`
	LinkStatus    string `json:"link_status"`
	LinkSpeed     int64  `json:"link_speed"` // Mbps (0 = unknown/not connected)
}

// InterfaceStats represents GET /api/v1/transport-nodes/{id}/network/interfaces/{ifId}/stats
// All fields are cumulative counters since the last reboot.
type InterfaceStats struct {
	RxBytes   int64 `json:"rx_bytes"`
	TxBytes   int64 `json:"tx_bytes"`
	RxPackets int64 `json:"rx_packets"`
	TxPackets int64 `json:"tx_packets"`
	RxDropped int64 `json:"rx_dropped"`
	TxDropped int64 `json:"tx_dropped"`
	RxErrors  int64 `json:"rx_errors"`
	TxErrors  int64 `json:"tx_errors"`
}

// CapacityUsageResponse represents GET /api/v1/capacity/usage
type CapacityUsageResponse struct {
	CapacityUsage []CapacityUsageItem `json:"capacity_usage"`
}

// CapacityUsageItem is one capacity metric entry.
type CapacityUsageItem struct {
	UsageType              string  `json:"usage_type"`   // e.g. NUMBER_OF_GROUPS
	DisplayName            string  `json:"display_name"` // human-readable label
	CurrentUsageCount      int64   `json:"current_usage_count"`
	MaxSupportedCount      int64   `json:"max_supported_count"`
	CurrentUsagePercentage float64 `json:"current_usage_percentage"`
}

// AlarmList represents GET /api/v1/alarms
type AlarmList struct {
	ResultCount int     `json:"result_count"`
	Cursor      string  `json:"cursor"`
	Results     []Alarm `json:"results"`
}

// Alarm is a single NSX platform alarm/fault.
type Alarm struct {
	ID                   string `json:"id"`
	FeatureName          string `json:"feature_name"`
	FeatureDisplayName   string `json:"feature_display_name"`
	EventTypeDisplayName string `json:"event_type_display_name"`
	Severity             string `json:"severity"`         // CRITICAL | HIGH | MEDIUM | LOW
	NodeDisplayName      string `json:"node_display_name"`
	EntityID             string `json:"entity_id"`
	LastReportedTime     int64  `json:"last_reported_time"` // epoch ms
	Status               string `json:"status"`             // OPEN | ACKNOWLEDGED | SUPPRESSED | RESOLVED
	Summary              string `json:"summary"`
}

// ---------------------------------------------------------------------------
// Load Balancer
// ---------------------------------------------------------------------------

// LBServiceList represents GET /api/v1/loadbalancer/services
type LBServiceList struct {
	ResultCount int         `json:"result_count"`
	Cursor      string      `json:"cursor"`
	Results     []LBService `json:"results"`
}

// LBService is one LB service entry (name + ID only; status fetched separately).
type LBService struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Size        string `json:"size"` // SMALL | MEDIUM | LARGE | XLARGE | DLB
}

// LBVirtualServerList represents GET /api/v1/loadbalancer/virtual-servers
type LBVirtualServerList struct {
	ResultCount int               `json:"result_count"`
	Cursor      string            `json:"cursor"`
	Results     []LBVirtualServer `json:"results"`
}

// LBVirtualServer carries the metadata needed to tag VS status points.
type LBVirtualServer struct {
	ID          string   `json:"id"`
	DisplayName string   `json:"display_name"`
	IPAddress   string   `json:"ip_address"`
	Ports       []string `json:"ports"`       // e.g. ["80", "443"]
	IPProtocol  string   `json:"ip_protocol"` // TCP | UDP
}

// LBPoolList represents GET /api/v1/loadbalancer/pools
type LBPoolList struct {
	ResultCount int      `json:"result_count"`
	Cursor      string   `json:"cursor"`
	Results     []LBPool `json:"results"`
}

// LBPool is one server pool entry.
type LBPool struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

// LBServiceStatus represents GET /api/v1/loadbalancer/services/{id}/status
// It carries the health of the service itself plus all its VS and pool members.
type LBServiceStatus struct {
	ServiceID      string          `json:"service_id"`
	ServiceStatus  string          `json:"service_status"` // UP | DOWN | ERROR | NO_ALARM | DETACHED
	VirtualServers []LBVSStatus    `json:"virtual_servers"`
	Pools          []LBPoolStatus  `json:"pools"`
}

// LBVSStatus is the per-virtual-server status block inside LBServiceStatus.
type LBVSStatus struct {
	VirtualServerID     string `json:"virtual_server_id"`
	VirtualServerStatus string `json:"status"` // UP | DOWN | ERROR | NO_ALARM
}

// LBPoolStatus is the per-pool status block inside LBServiceStatus.
type LBPoolStatus struct {
	PoolID     string           `json:"pool_id"`
	PoolStatus string           `json:"status"` // UP | DOWN | PARTIALLY_UP | UNKNOWN
	Members    []LBMemberStatus `json:"members"`
}

// LBMemberStatus is an individual server member inside a pool.
type LBMemberStatus struct {
	IPAddress string `json:"ip_address"`
	Port      string `json:"port"`
	Status    string `json:"status"` // UP | DOWN | DISABLED | GRACEFUL_DISABLED
}

// ---------------------------------------------------------------------------
// LB Node Usage Summary (Policy API)
// GET /policy/api/v1/infra/lb-node-usage-summary?include_usages=true
// ---------------------------------------------------------------------------

// LBNodeUsageSummaryResponse wraps the results array of the LB usage summary.
type LBNodeUsageSummaryResponse struct {
	Results []LBNodeUsageSummary `json:"results"`
}

// LBNodeUsageSummary is the manager-wide aggregate plus per-edge-node detail.
type LBNodeUsageSummary struct {
	CurrentCredits      int64                  `json:"current_load_balancer_credits"`
	CreditCapacity      int64                  `json:"load_balancer_credit_capacity"`
	UsagePercentage     float64                `json:"usage_percentage"`
	Severity            string                 `json:"severity"` // GREEN | ORANGE | RED
	CurrentPoolMembers  int64                  `json:"current_pool_member_count"`
	PoolMemberCapacity  int64                  `json:"pool_member_capacity"`
	NodeCounts          []LBNodeSeverityCount  `json:"node_counts"`
	NodeUsages          []LBNodeUsage          `json:"node_usages"`
}

// LBNodeSeverityCount carries the number of edge nodes per severity bucket.
type LBNodeSeverityCount struct {
	Severity  string `json:"severity"` // GREEN | ORANGE | RED
	NodeCount int    `json:"node_count"`
}

// LBNodeUsage is one edge node's LB consumption snapshot.
type LBNodeUsage struct {
	FormFactor              string  `json:"form_factor"`        // VIRTUAL_MACHINE | PHYSICAL_MACHINE
	EdgeClusterPath         string  `json:"edge_cluster_path"`
	NodePath                string  `json:"node_path"`
	CurrentCredits          int64   `json:"current_load_balancer_credits"`
	CreditCapacity          int64   `json:"load_balancer_credit_capacity"`
	UsagePercentage         float64 `json:"usage_percentage"`
	Severity                string  `json:"severity"`
	CurrentPoolMembers      int64   `json:"current_pool_member_count"`
	CurrentVirtualServers   int64   `json:"current_virtual_server_count"`
	CurrentPools            int64   `json:"current_pool_count"`
	PoolMemberCapacity      int64   `json:"pool_member_capacity"`
	CurrentSmall            int64   `json:"current_small_load_balancer_count"`
	CurrentMedium           int64   `json:"current_medium_load_balancer_count"`
	CurrentLarge            int64   `json:"current_large_load_balancer_count"`
	CurrentXLarge           int64   `json:"current_xlarge_load_balancer_count"`
	RemainingSmall          int64   `json:"remaining_small_load_balancer_count"`
	RemainingMedium         int64   `json:"remaining_medium_load_balancer_count"`
	RemainingLarge          int64   `json:"remaining_large_load_balancer_count"`
	RemainingXLarge         int64   `json:"remaining_xlarge_load_balancer_count"`
}

// LastPathSegment returns the trailing UUID/identifier from a Policy API path
// (e.g. ".../edge-clusters/abc-123" → "abc-123").
func LastPathSegment(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}

// ---------------------------------------------------------------------------
// Policy API — Tier-0 / Tier-1 / Segments
// Distinguishes VRFs (T0 with vrf_config) from regular T0s.
// ---------------------------------------------------------------------------

// PolicyTier0List represents GET /policy/api/v1/infra/tier-0s
type PolicyTier0List struct {
	ResultCount int           `json:"result_count"`
	Cursor      string        `json:"cursor"`
	Results     []PolicyTier0 `json:"results"`
}

// PolicyTier0 carries enough to detect VRFs and resolve display names.
// VRFConfig is left as RawMessage — we only need to know if it's set.
type PolicyTier0 struct {
	ID          string          `json:"id"`
	DisplayName string          `json:"display_name"`
	Path        string          `json:"path"`         // /infra/tier-0s/<id>
	UniqueID    string          `json:"unique_id"`    // matches legacy logical-router UUID
	VRFConfig   json.RawMessage `json:"vrf_config,omitempty"`
}

// IsVRF returns true when the T0 is a VRF gateway (has vrf_config set).
func (t *PolicyTier0) IsVRF() bool { return len(t.VRFConfig) > 0 }

// PolicyTier1List represents GET /policy/api/v1/infra/tier-1s
type PolicyTier1List struct {
	ResultCount int           `json:"result_count"`
	Cursor      string        `json:"cursor"`
	Results     []PolicyTier1 `json:"results"`
}

// PolicyTier1 carries the link to its parent T0 (or VRF), plus identifiers
// needed to cross-reference with the legacy logical-routers list.
type PolicyTier1 struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Path        string `json:"path"`
	Tier0Path   string `json:"tier0_path"`   // /infra/tier-0s/<t0_or_vrf_id>
	UniqueID    string `json:"unique_id"`    // matches legacy logical-router UUID
	CreateTime  int64  `json:"_create_time"` // epoch ms — used to detect freshness
	CreateUser  string `json:"_create_user"`
}

// PolicySegmentList represents GET /policy/api/v1/infra/segments
type PolicySegmentList struct {
	ResultCount int             `json:"result_count"`
	Cursor      string          `json:"cursor"`
	Results     []PolicySegment `json:"results"`
}

// PolicySegment carries connectivity (tier-1 or tier-0 path) for per-tenant aggregation.
type PolicySegment struct {
	ID               string `json:"id"`
	DisplayName      string `json:"display_name"`
	Path             string `json:"path"`
	ConnectivityPath string `json:"connectivity_path,omitempty"` // /infra/tier-1s/<id> or /infra/tier-0s/<id>
}

// ---------------------------------------------------------------------------
// Policy API — NAT rules per Tier-1
// GET /policy/api/v1/infra/tier-1s/{id}/nat/{policy}/nat-rules
// Or aggregated:
// GET /policy/api/v1/infra/tier-1s/{id}/nat/USER/nat-rules
// We use the cheaper count-only form via include_mark_for_delete_objects=false.
// ---------------------------------------------------------------------------

// PolicyNATRuleList represents GET nat-rules — we only need the count.
type PolicyNATRuleList struct {
	ResultCount int `json:"result_count"`
}

// ---------------------------------------------------------------------------
// Policy API — Gateway Firewall policies per Tier-1
// GET /policy/api/v1/infra/domains/default/gateway-policies?include_rule_count=true
// ---------------------------------------------------------------------------

// PolicyGatewayPolicyList represents GET gateway-policies with rule counts.
type PolicyGatewayPolicyList struct {
	ResultCount int                   `json:"result_count"`
	Cursor      string                `json:"cursor"`
	Results     []PolicyGatewayPolicy `json:"results"`
}

// PolicyGatewayPolicy is one gateway firewall policy with rule_count populated
// when the request includes include_rule_count=true.
type PolicyGatewayPolicy struct {
	ID          string   `json:"id"`
	DisplayName string   `json:"display_name"`
	Category    string   `json:"category,omitempty"`
	Scope       []string `json:"scope,omitempty"` // /infra/tier-1s/<id> or /infra/tier-0s/<id>
	RuleCount   int      `json:"rule_count"`
}

// ---------------------------------------------------------------------------
// Policy API — Edge Clusters (resolves edge_cluster_id → display_name)
// GET /policy/api/v1/infra/sites/default/enforcement-points/default/edge-clusters
// ---------------------------------------------------------------------------

// PolicyEdgeClusterList represents the Policy API edge clusters list.
type PolicyEdgeClusterList struct {
	ResultCount int                 `json:"result_count"`
	Cursor      string              `json:"cursor"`
	Results     []PolicyEdgeCluster `json:"results"`
}

// PolicyEdgeCluster carries display name + nsx_id (= legacy edge_cluster_id).
type PolicyEdgeCluster struct {
	ID          string `json:"id"`
	NSXID       string `json:"nsx_id"`       // matches legacy logical-router.edge_cluster_id
	DisplayName string `json:"display_name"`
	Path        string `json:"path"`
}

// ---------------------------------------------------------------------------
// Policy API — Groups with effective member counts (for waste/orphan analysis)
// GET /policy/api/v1/infra/domains/default/groups
// Effective members fetched per-group via /members/virtual-machines etc.
// To keep cost low we just expose the list endpoint here; the worker will
// sample effective counts on demand (top-N or rotating sample).
// ---------------------------------------------------------------------------

// PolicyGroupList represents GET groups.
type PolicyGroupList struct {
	ResultCount int           `json:"result_count"`
	Cursor      string        `json:"cursor"`
	Results     []PolicyGroup `json:"results"`
}

// PolicyGroup is one logical group; we only need the path + name + expression presence.
type PolicyGroup struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Path        string `json:"path"`
	// Expression set indicates the group has matching criteria (vs an empty group).
	Expression []json.RawMessage `json:"expression,omitempty"`
}
