package nsx

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Client is an authenticated NSX Manager API client.
type Client struct {
	baseURL  string
	username string
	password string
	http     *http.Client
}

// NewClient creates a new NSX API client.
func NewClient(baseURL, username, password string, tlsSkipVerify bool) *Client {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: tlsSkipVerify}, //nolint:gosec
	}
	return &Client{
		baseURL:  baseURL,
		username: username,
		password: password,
		http: &http.Client{
			Timeout:   15 * time.Second,
			Transport: transport,
		},
	}
}

// doGet performs an authenticated GET request and decodes the JSON response.
// Retries on HTTP 429 with exponential backoff (honoring Retry-After when present)
// since the NSX Manager throttles bursts of requests.
func (c *Client) doGet(ctx context.Context, path string, dest interface{}) error {
	const maxAttempts = 4
	backoff := 500 * time.Millisecond

	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
		if err != nil {
			return fmt.Errorf("creating request: %w", err)
		}
		req.SetBasicAuth(c.username, c.password)
		req.Header.Set("Accept", "application/json")

		resp, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("executing request: %w", err)
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxAttempts {
			wait := parseRetryAfter(resp.Header.Get("Retry-After"), backoff)
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			backoff *= 2
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return fmt.Errorf("unexpected status %d for %s", resp.StatusCode, path)
		}

		err = json.NewDecoder(resp.Body).Decode(dest)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("decoding response: %w", err)
		}
		return nil
	}
}

// parseRetryAfter interprets the Retry-After header (delta-seconds form).
// Falls back to the supplied default when absent or unparseable.
func parseRetryAfter(header string, fallback time.Duration) time.Duration {
	if header == "" {
		return fallback
	}
	if secs, err := strconv.Atoi(header); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return fallback
}
