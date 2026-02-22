package nsx

import "encoding/json"

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
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	RouterType  string `json:"router_type"` // TIER0 | TIER1 | VRF
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
	VirtualServerStatus string `json:"virtual_server_status"` // UP | DOWN | ERROR | NO_ALARM
}

// LBPoolStatus is the per-pool status block inside LBServiceStatus.
type LBPoolStatus struct {
	PoolID     string           `json:"pool_id"`
	PoolStatus string           `json:"pool_status"` // UP | DOWN | PARTIALLY_UP | UNKNOWN
	Members    []LBMemberStatus `json:"members"`
}

// LBMemberStatus is an individual server member inside a pool.
type LBMemberStatus struct {
	IPAddress string `json:"ip_address"`
	Port      string `json:"port"`
	Status    string `json:"status"` // UP | DOWN | DISABLED | GRACEFUL_DISABLED
}
