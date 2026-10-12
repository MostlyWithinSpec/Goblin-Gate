// Package tailscale drives tailscaled for task 041 through its LocalAPI
// (the unix socket the tailscale CLI uses), with no extra dependencies.
// goblind keeps tailscaled's netfilter mode off: the firewall stays
// Goblin's and the tailnet is the "tailscale" interface in policies.
package tailscale

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

// Paths and defaults.
const (
	Socket            = "/var/run/tailscale/tailscaled.sock"
	PrefsPath         = "/etc/goblingate/tailscale/prefs.json" // rendered desired state
	DefaultControlURL = "https://controlplane.tailscale.com"
	DirectPort        = 41641 // tailscaled's default UDP port
)

// ErrUnavailable means tailscaled is not running (or not installed).
var ErrUnavailable = errors.New("tailscale: tailscaled is not running")

// Client talks to tailscaled's LocalAPI.
type Client struct {
	Socket string // default Socket
	http   *http.Client
}

func (c *Client) sock() string {
	if c.Socket != "" {
		return c.Socket
	}
	return Socket
}

// Available reports whether the LocalAPI socket exists.
func (c *Client) Available() bool {
	_, err := os.Stat(c.sock())
	return err == nil
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	if c.http == nil {
		sock := c.sock()
		c.http = &http.Client{Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		}, Timeout: 30 * time.Second}
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://local-tailscaled.sock/localapi/v0/"+path, rd)
	if err != nil {
		return err
	}
	// tailscaled refuses state-changing LocalAPI calls without this header
	req.Header.Set("Sec-Tailscale", "localapi")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		var ne *net.OpError
		if errors.As(err, &ne) {
			return fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("tailscale: %s %s: HTTP %d: %s", method, path, resp.StatusCode, bytes.TrimSpace(data))
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Status is the part of tailscaled's status the API shows.
type Status struct {
	BackendState string                 `json:"BackendState"` // NeedsLogin, NeedsMachineAuth, Starting, Running, Stopped
	AuthURL      string                 `json:"AuthURL"`
	TailscaleIPs []string               `json:"TailscaleIPs"`
	Self         *PeerStatus            `json:"Self"`
	Peer         map[string]*PeerStatus `json:"Peer"`
}

// PeerStatus is one node.
type PeerStatus struct {
	HostName       string    `json:"HostName"`
	DNSName        string    `json:"DNSName"`
	OS             string    `json:"OS"`
	TailscaleIPs   []string  `json:"TailscaleIPs"`
	Online         bool      `json:"Online"`
	LastSeen       time.Time `json:"LastSeen"`
	ExitNode       bool      `json:"ExitNode"`
	ExitNodeOption bool      `json:"ExitNodeOption"`
	PrimaryRoutes  []string  `json:"PrimaryRoutes"` // subnet routes this node serves (approved)
}

// Status reads tailscaled's status.
func (c *Client) Status(ctx context.Context) (*Status, error) {
	var st Status
	if err := c.do(ctx, http.MethodGet, "status", nil, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// Prefs is the part of tailscaled's preferences goblind manages.
type Prefs struct {
	ControlURL      string   `json:"ControlURL"`
	WantRunning     bool     `json:"WantRunning"`
	RouteAll        bool     `json:"RouteAll"`
	CorpDNS         bool     `json:"CorpDNS"`
	Hostname        string   `json:"Hostname"`
	AdvertiseRoutes []string `json:"AdvertiseRoutes"`
	NetfilterMode   int      `json:"NetfilterMode"` // 0 off, 1 nodivert, 2 on
	NoSNAT          bool     `json:"NoSNAT"`
}

// GetPrefs reads the current preferences.
func (c *Client) GetPrefs(ctx context.Context) (*Prefs, error) {
	var p Prefs
	if err := c.do(ctx, http.MethodGet, "prefs", nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// maskedPrefs is the LocalAPI PATCH body: each field with its ...Set flag.
type maskedPrefs struct {
	Prefs
	ControlURLSet      bool `json:"ControlURLSet"`
	WantRunningSet     bool `json:"WantRunningSet"`
	RouteAllSet        bool `json:"RouteAllSet"`
	CorpDNSSet         bool `json:"CorpDNSSet"`
	HostnameSet        bool `json:"HostnameSet"`
	AdvertiseRoutesSet bool `json:"AdvertiseRoutesSet"`
	NetfilterModeSet   bool `json:"NetfilterModeSet"`
	NoSNATSet          bool `json:"NoSNATSet"`
}

// LoginInteractive starts a browser login; Status.AuthURL then holds the URL.
func (c *Client) LoginInteractive(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "login-interactive", nil, nil)
}

// LoginAuthKey logs in with a pre-authorized key (not stored anywhere).
func (c *Client) LoginAuthKey(ctx context.Context, key string) error {
	return c.do(ctx, http.MethodPost, "start", map[string]any{"AuthKey": key}, nil)
}

// Logout logs the node out of the tailnet.
func (c *Client) Logout(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "logout", nil, nil)
}

// Desired is the rendered desired state (PrefsPath), produced from the
// config by Render and applied by Apply.
type Desired struct {
	Enabled         bool     `json:"enabled"`
	Hostname        string   `json:"hostname,omitempty"`
	AdvertiseRoutes []string `json:"advertise_routes,omitempty"` // CIDRs
	ExitNode        bool     `json:"exit_node,omitempty"`
	AcceptRoutes    bool     `json:"accept_routes,omitempty"`
	AcceptDNS       bool     `json:"accept_dns,omitempty"`
	LoginServer     string   `json:"login_server,omitempty"`
}

// Apply makes tailscaled match d. Disabled: stop it (nothing to do when it
// is not running). Enabled: set the preferences, including netfilter off
// and no SNAT (subnet traffic keeps its tailnet source address; policies
// and the box's routes handle it), and want it running - it then waits
// for a login if it has none.
func Apply(ctx context.Context, c *Client, d Desired) error {
	if !d.Enabled {
		if !c.Available() {
			return nil
		}
		err := c.do(ctx, http.MethodPatch, "prefs", maskedPrefs{Prefs: Prefs{WantRunning: false}, WantRunningSet: true}, nil)
		if errors.Is(err, ErrUnavailable) {
			return nil
		}
		return err
	}
	if !c.Available() {
		return fmt.Errorf("%w: install tailscale and start tailscaled.service", ErrUnavailable)
	}
	routes := append([]string{}, d.AdvertiseRoutes...)
	if d.ExitNode {
		routes = append(routes, "0.0.0.0/0", "::/0")
	}
	control := d.LoginServer
	if control == "" {
		control = DefaultControlURL
	}
	mp := maskedPrefs{
		Prefs: Prefs{
			ControlURL: control, WantRunning: true, RouteAll: d.AcceptRoutes, CorpDNS: d.AcceptDNS,
			Hostname: d.Hostname, AdvertiseRoutes: routes, NetfilterMode: 0, NoSNAT: true,
		},
		ControlURLSet: true, WantRunningSet: true, RouteAllSet: true, CorpDNSSet: true,
		HostnameSet: true, AdvertiseRoutesSet: true, NetfilterModeSet: true, NoSNATSet: true,
	}
	return c.do(ctx, http.MethodPatch, "prefs", mp, nil)
}

// ApplyFile reads the rendered desired state and applies it (`goblind
// tailscale-apply`, the applier's activation command).
func ApplyFile(ctx context.Context, c *Client, path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var d Desired
	if err := json.Unmarshal(b, &d); err != nil {
		return fmt.Errorf("tailscale: %s: %w", path, err)
	}
	return Apply(ctx, c, d)
}
