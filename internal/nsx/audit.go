package nsx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ErrLogNotFound is returned when the requested log file does not exist on the
// node (e.g. nsx-audit.log.1 before the first rotation).
var ErrLogNotFound = errors.New("log file not found")

// GetClusterNodeLog fetches the raw contents of a log file from a specific
// cluster node via /api/v1/cluster/<node-id>/node/logs/<name>/data. The audit
// log (nsx-audit.log) records Src/username/Operation of every audited API call
// and is the data source for the auditwatch module. Uses a longer timeout than
// the JSON client since audit files can be several MB near rotation.
func (c *Client) GetClusterNodeLog(ctx context.Context, nodeID, logName string) (string, error) {
	path := "/api/v1/cluster/" + nodeID + "/node/logs/" + logName + "/data"
	logHTTP := &http.Client{Timeout: 60 * time.Second, Transport: c.http.Transport}

	const maxAttempts = 3
	backoff := 1 * time.Second

	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
		if err != nil {
			return "", fmt.Errorf("creating request: %w", err)
		}
		req.SetBasicAuth(c.username, c.password)
		req.Header.Set("Accept", "application/octet-stream")

		resp, err := logHTTP.Do(req)
		if err != nil {
			return "", fmt.Errorf("executing request: %w", err)
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxAttempts {
			wait := parseRetryAfter(resp.Header.Get("Retry-After"), backoff)
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(wait):
			}
			backoff *= 2
			continue
		}

		if resp.StatusCode == http.StatusNotFound {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			return "", ErrLogNotFound
		}
		if resp.StatusCode != http.StatusOK {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			return "", fmt.Errorf("unexpected status %d for %s", resp.StatusCode, path)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("reading log body: %w", err)
		}
		return string(body), nil
	}
}
