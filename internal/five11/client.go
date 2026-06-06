// Package five11 is a small client for the 511.org SF Bay transit API.
package five11

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Client talks to the 511 transit API.
type Client struct {
	apiKey   string
	operator string
	baseURL  string
	http     *http.Client
}

// New returns a Client. baseURL is the API root, e.g.
// "https://api.511.org/transit".
func New(apiKey, operator, baseURL string) *Client {
	return &Client{
		apiKey:   apiKey,
		operator: operator,
		baseURL:  baseURL,
		http:     &http.Client{Timeout: 20 * time.Second},
	}
}

// utf8BOM is prepended by 511 to its JSON responses and must be stripped
// before decoding.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

func (c *Client) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	q.Set("api_key", c.apiKey)
	q.Set("format", "json")
	u := fmt.Sprintf("%s/%s?%s", c.baseURL, path, q.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		snippet := body
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, fmt.Errorf("511 %s: HTTP %d: %s", path, resp.StatusCode, bytes.TrimSpace(snippet))
	}
	return bytes.TrimPrefix(body, utf8BOM), nil
}

// StopMonitoring returns the predicted arrivals for a single stop code.
func (c *Client) StopMonitoring(ctx context.Context, stopCode string) ([]MonitoredStopVisit, error) {
	q := url.Values{}
	q.Set("agency", c.operator)
	q.Set("stopcode", stopCode)

	body, err := c.get(ctx, "StopMonitoring", q)
	if err != nil {
		return nil, err
	}
	var out StopMonitoringResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode StopMonitoring: %w", err)
	}
	return out.ServiceDelivery.StopMonitoringDelivery.MonitoredStopVisit, nil
}

// Stops returns the operator's stop listing (used by the discover command).
func (c *Client) Stops(ctx context.Context) ([]ScheduledStopPoint, error) {
	q := url.Values{}
	q.Set("operator_id", c.operator)

	body, err := c.get(ctx, "stops", q)
	if err != nil {
		return nil, err
	}
	var out StopsResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode stops: %w", err)
	}
	return out.Contents.DataObjects.ScheduledStopPoint, nil
}
