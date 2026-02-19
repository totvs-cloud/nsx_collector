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

// GetBGPNeighborStatus returns BGP neighbor status for a logical router.
// Returns empty list (not error) when no BGP is configured.
func (c *Client) GetBGPNeighborStatus(ctx context.Context, routerID string) (*BGPNeighborStatusList, error) {
	var result BGPNeighborStatusList
	path := "/api/v1/logical-routers/" + routerID + "/routing/bgp/neighbors/status?source=realtime"
	if err := c.doGet(ctx, path, &result); err != nil {
		return nil, fmt.Errorf("BGP neighbors for %s: %w", routerID, err)
	}
	return &result, nil
}
