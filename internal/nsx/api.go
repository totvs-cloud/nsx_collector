package nsx

import (
	"context"
	"fmt"
)

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
			path += "&cursor=" + cursor
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
			path += "&cursor=" + cursor
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

// GetLogicalRouterPorts returns all logical router ports, paginating automatically.
// Used to build the T1→T0 parent mapping via LinkedRouterPort entries.
func (c *Client) GetLogicalRouterPorts(ctx context.Context) ([]LogicalRouterPort, error) {
	var all []LogicalRouterPort
	cursor := ""
	for {
		path := "/api/v1/logical-router-ports?page_size=100"
		if cursor != "" {
			path += "&cursor=" + cursor
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
			path += "&cursor=" + cursor
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

