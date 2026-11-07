package homeassistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Client calls goblind on the plugin socket (docs/dev/plugins.md). Only the
// answers' fields the bridge uses are decoded.
type Client struct {
	HTTP *http.Client
	Base string // "http://goblind" for the socket; a test server's URL in tests
}

// NewClient talks to goblind's plugin socket.
func NewClient(socket string) *Client {
	return &Client{Base: "http://goblind", HTTP: &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}},
	}}
}

// APIError is a non-2xx answer from goblind.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("goblind: %d %s", e.Status, e.Message) }

// Denied reports whether err is goblind refusing a capability the admin did
// not approve.
func Denied(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusForbidden
}

func (c *Client) call(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		return &APIError{Status: resp.StatusCode, Message: e.Error.Message}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

// Self is GET /api/v1/plugin/self.
func (c *Client) Self(ctx context.Context) (caps []string, settings map[string]any, err error) {
	var out struct {
		Capabilities []string       `json:"capabilities"`
		Settings     map[string]any `json:"settings"`
	}
	err = c.call(ctx, "GET", "/api/v1/plugin/self", nil, &out)
	return out.Capabilities, out.Settings, err
}

// WAN is one entry of GET /api/v1/wan/status.
type WAN struct {
	Name      string   `json:"name"`
	Device    string   `json:"device"`
	Up        bool     `json:"up"`
	Addresses []string `json:"addresses"` // CIDRs
	Gateway   string   `json:"gateway"`
}

// WANs is GET /api/v1/wan/status (stats:read).
func (c *Client) WANs(ctx context.Context) ([]WAN, error) {
	var out struct {
		WANs []WAN `json:"wans"`
	}
	err := c.call(ctx, "GET", "/api/v1/wan/status", nil, &out)
	return out.WANs, err
}

// Gateway is one monitored gateway of GET /api/v1/gateways/status.
type Gateway struct {
	Name        string `json:"name"`
	State       string `json:"state"` // up | down | unknown
	LossPercent int    `json:"loss_percent"`
	LatencyMs   int    `json:"latency_ms"`
}

// Gateways is GET /api/v1/gateways/status (stats:read).
func (c *Client) Gateways(ctx context.Context) ([]Gateway, error) {
	var out struct {
		Gateways []Gateway `json:"gateways"`
	}
	err := c.call(ctx, "GET", "/api/v1/gateways/status", nil, &out)
	return out.Gateways, err
}

// System is GET /api/v1/stats/system (stats:read).
type System struct {
	CPUPercent    float64 `json:"cpu_percent"`
	MemTotalBytes uint64  `json:"mem_total_bytes"`
	MemUsedBytes  uint64  `json:"mem_used_bytes"`
	UptimeSeconds int64   `json:"uptime_seconds"`
	Sessions      int64   `json:"sessions"`
}

// System reads the box's load.
func (c *Client) System(ctx context.Context) (System, error) {
	var out System
	err := c.call(ctx, "GET", "/api/v1/stats/system", nil, &out)
	return out, err
}

// Device is one entry of GET /api/v1/devices.
type Device struct {
	MAC       string    `json:"mac"`
	IPs       []string  `json:"ips"`
	Hostname  string    `json:"hostname"`
	Name      string    `json:"name"`
	Interface string    `json:"interface"`
	LastSeen  time.Time `json:"last_seen"`
}

// Devices is GET /api/v1/devices (devices:read).
func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	var out struct {
		Devices []Device `json:"devices"`
	}
	err := c.call(ctx, "GET", "/api/v1/devices", nil, &out)
	return out.Devices, err
}

// Policy is one policy the plugin may switch.
type Policy struct {
	ID      uint32 `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// Policies is GET /api/v1/plugin/policies (policies:toggle).
func (c *Client) Policies(ctx context.Context) ([]Policy, error) {
	var out struct {
		Policies []Policy `json:"policies"`
	}
	err := c.call(ctx, "GET", "/api/v1/plugin/policies", nil, &out)
	return out.Policies, err
}

// SwitchPolicy is PUT /api/v1/plugin/policies/{id}; it returns the state
// afterwards.
func (c *Client) SwitchPolicy(ctx context.Context, id uint32, enabled bool) (Policy, error) {
	var out Policy
	err := c.call(ctx, "PUT", "/api/v1/plugin/policies/"+strconv.FormatUint(uint64(id), 10), map[string]bool{"enabled": enabled}, &out)
	return out, err
}
