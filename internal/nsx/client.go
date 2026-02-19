package nsx

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
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
func (c *Client) doGet(ctx context.Context, path string, dest interface{}) error {
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
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d for %s", resp.StatusCode, path)
	}

	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}
