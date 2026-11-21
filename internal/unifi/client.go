package unifi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Client reads a UniFi Network application with a local API key.
type Client struct {
	Settings Settings
	HTTP     *http.Client // nil: one with Settings.TLS

	once   sync.Once
	mu     sync.Mutex
	prefix string // "/proxy/network" on UniFi OS, "" self-hosted; found on first use
	found  bool
}

// Station is one client as stat/sta reports it (the fields used).
type Station struct {
	MAC            string   `json:"mac"`
	IP             string   `json:"ip"`
	IPv6           []string `json:"ipv6_addresses"`
	Hostname       string   `json:"hostname"`
	Name           string   `json:"name"`
	Note           string   `json:"note"`
	Wired          bool     `json:"is_wired"`
	Network        string   `json:"network"`
	VLAN           int      `json:"vlan"`
	SwitchMAC      string   `json:"sw_mac"`
	SwitchPort     int      `json:"sw_port"`
	WiredRateMbps  int      `json:"wired_rate_mbps"`
	APMAC          string   `json:"ap_mac"`
	ESSID          string   `json:"essid"`
	Radio          string   `json:"radio"` // ng, na, 6e
	LastUplinkName string   `json:"last_uplink_name"`
}

// Device is one UniFi device as stat/device-basic reports it.
type Device struct {
	MAC  string `json:"mac"`
	Name string `json:"name"`
	Type string `json:"type"` // usw, uap, ugw, udm
}

func (c *Client) http() *http.Client {
	c.once.Do(func() {
		if c.HTTP == nil {
			c.HTTP = &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{TLSClientConfig: c.Settings.TLS}}
		}
	})
	return c.HTTP
}

// errKey explains a refused key: the common mistake is a Site Manager key
// from unifi.ui.com, which only works against Ubiquiti's cloud.
const errKey = "the console refused the API key (HTTP %d): create it in UniFi Network on the console itself, Settings → Control Plane → Integrations; keys made on unifi.ui.com don't work locally"

// get fetches a classic API path under the site (stat/sta) into out's data.
func (c *Client) get(ctx context.Context, path string, out any) error {
	c.mu.Lock()
	prefixes := []string{c.prefix}
	if !c.found {
		prefixes = []string{"/proxy/network", ""}
	}
	c.mu.Unlock()
	var last error
	for _, p := range prefixes {
		status, err := c.fetch(ctx, p+"/api/s/"+url.PathEscape(c.Settings.Site)+"/"+path, out)
		if status == http.StatusNotFound && len(prefixes) > 1 {
			last = err
			continue // not UniFi OS (or the other way round)
		}
		if err == nil {
			c.mu.Lock()
			c.prefix, c.found = p, true
			c.mu.Unlock()
		}
		return err
	}
	return last
}

func (c *Client) fetch(ctx context.Context, path string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.Settings.URL+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("X-API-KEY", c.Settings.APIKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http().Do(req)
	if err != nil {
		return 0, fmt.Errorf("UniFi console: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return resp.StatusCode, fmt.Errorf("UniFi console: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return resp.StatusCode, fmt.Errorf(errKey, resp.StatusCode)
	case resp.StatusCode == http.StatusNotFound:
		return resp.StatusCode, fmt.Errorf("UniFi console: %s: not found (is site %q right?)", path, c.Settings.Site)
	case resp.StatusCode != http.StatusOK:
		return resp.StatusCode, fmt.Errorf("UniFi console: %s: HTTP %d", path, resp.StatusCode)
	}
	var env struct {
		Meta struct {
			RC  string `json:"rc"`
			Msg string `json:"msg"`
		} `json:"meta"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return resp.StatusCode, fmt.Errorf("UniFi console: %s: not a UniFi answer: %w", path, err)
	}
	if env.Meta.RC != "ok" {
		return resp.StatusCode, fmt.Errorf("UniFi console: %s: %s", path, env.Meta.Msg)
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return resp.StatusCode, fmt.Errorf("UniFi console: %s: %w", path, err)
	}
	return resp.StatusCode, nil
}

// Stations lists the connected clients.
func (c *Client) Stations(ctx context.Context) ([]Station, error) {
	var out []Station
	return out, c.get(ctx, "stat/sta", &out)
}

// Devices lists the UniFi devices (switches, access points).
func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	var out []Device
	return out, c.get(ctx, "stat/device-basic", &out)
}
