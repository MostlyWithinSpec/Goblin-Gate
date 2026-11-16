package homelab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Guest is one running VM or container.
type Guest struct {
	Source string // "proxmox" or the Docker endpoint
	Kind   string // qemu, lxc, container
	Name   string
	Tags   []string // lower-case Proxmox tags or Docker label keys
	Addrs  []netip.Addr
	// MACs are the guest's NICs, used to find its addresses in the box's
	// device inventory (DHCP leases) when the guest does not report them.
	MACs []string
}

// Source lists the running guests of one system.
type Source interface {
	Name() string
	Guests(ctx context.Context) ([]Guest, error)
}

// Proxmox reads a Proxmox VE cluster with an API token. The token needs
// VM.Audit for the guests and their LXC addresses and, for VM addresses from
// the QEMU guest agent, VM.GuestAgent.Audit (Proxmox VE 9) or VM.Monitor
// (Proxmox VE 8). Without the agent the guest's MACs are looked up in the
// box's DHCP leases.
type Proxmox struct {
	Settings ProxmoxSettings
	HTTP     *http.Client
}

// NewProxmox returns a client for s.
func NewProxmox(s ProxmoxSettings) *Proxmox {
	return &Proxmox{Settings: s, HTTP: &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: s.TLS}}}
}

func (p *Proxmox) Name() string { return "proxmox" }

// pveError is an API answer that is not 200.
type pveError struct {
	Status int
	Msg    string
}

func (e *pveError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Status, e.Msg) }

func (p *Proxmox) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", p.Settings.URL+"/api2/json"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "PVEAPIToken="+p.Settings.TokenID+"="+p.Settings.TokenSecret)
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		// Proxmox puts the reason in the status line
		msg := strings.TrimSpace(strings.TrimPrefix(resp.Status, strconv.Itoa(resp.StatusCode)))
		if resp.StatusCode == http.StatusUnauthorized {
			msg += " (check proxmox_token_id and proxmox_token_secret)"
		}
		return &pveError{Status: resp.StatusCode, Msg: msg}
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return json.Unmarshal(env.Data, out)
}

type pveResource struct {
	ID       string `json:"id"` // qemu/100
	VMID     int    `json:"vmid"`
	Name     string `json:"name"`
	Node     string `json:"node"`
	Type     string `json:"type"` // qemu, lxc
	Status   string `json:"status"`
	Tags     string `json:"tags"` // "gg-web;prod"
	Template int    `json:"template"`
}

// pveIP is one address as the guest agent (and newer LXC answers) report it.
type pveIP struct {
	Type   string `json:"ip-address-type"`
	Addr   string `json:"ip-address"`
	Prefix int    `json:"prefix"`
}

// Guests lists the cluster's running VMs and containers with their addresses.
func (p *Proxmox) Guests(ctx context.Context) ([]Guest, error) {
	var res []pveResource
	if err := p.get(ctx, "/cluster/resources?type=vm", &res); err != nil {
		return nil, fmt.Errorf("proxmox: guests: %w", err)
	}
	var out []Guest
	for _, r := range res {
		if r.Status != "running" || r.Template == 1 || (r.Type != "qemu" && r.Type != "lxc") {
			continue
		}
		g := Guest{Source: "proxmox", Kind: r.Type, Name: r.Name, Tags: splitTags(r.Tags)}
		base := fmt.Sprintf("/nodes/%s/%s/%d", url.PathEscape(r.Node), r.Type, r.VMID)
		var err error
		if r.Type == "qemu" {
			g.Addrs, err = p.agentAddrs(ctx, base)
		} else {
			g.Addrs, err = p.lxcAddrs(ctx, base)
		}
		if err != nil {
			var pe *pveError
			if !errors.As(err, &pe) || pe.Status == http.StatusUnauthorized {
				// the node or the token is the problem, not the guest
				return nil, fmt.Errorf("proxmox: %s %d: %w", r.Type, r.VMID, err)
			}
			// no agent, agent not running, or no permission for it: fall
			// back to the DHCP leases
		}
		if len(g.Addrs) == 0 {
			var cfg map[string]any
			if err := p.get(ctx, base+"/config", &cfg); err != nil {
				return nil, fmt.Errorf("proxmox: %s %d config: %w", r.Type, r.VMID, err)
			}
			g.MACs = configMACs(cfg)
		}
		out = append(out, g)
	}
	return out, nil
}

func (p *Proxmox) agentAddrs(ctx context.Context, base string) ([]netip.Addr, error) {
	var ans struct {
		Result []struct {
			Name string  `json:"name"`
			IPs  []pveIP `json:"ip-addresses"`
		} `json:"result"`
	}
	if err := p.get(ctx, base+"/agent/network-get-interfaces", &ans); err != nil {
		return nil, err
	}
	var out []netip.Addr
	for _, nic := range ans.Result {
		if nic.Name == "lo" {
			continue
		}
		for _, ip := range nic.IPs {
			if a, err := netip.ParseAddr(ip.Addr); err == nil {
				out = append(out, a)
			}
		}
	}
	return out, nil
}

func (p *Proxmox) lxcAddrs(ctx context.Context, base string) ([]netip.Addr, error) {
	var nics []struct {
		Name  string  `json:"name"`
		Inet  string  `json:"inet"`  // 10.0.0.5/24
		Inet6 string  `json:"inet6"` // fd00::5/64
		IPs   []pveIP `json:"ip-addresses"`
	}
	if err := p.get(ctx, base+"/interfaces", &nics); err != nil {
		return nil, err
	}
	var out []netip.Addr
	for _, nic := range nics {
		if nic.Name == "lo" {
			continue
		}
		for _, s := range append(strings.Fields(nic.Inet), strings.Fields(nic.Inet6)...) {
			if p, err := netip.ParsePrefix(s); err == nil {
				out = append(out, p.Addr())
			} else if a, err := netip.ParseAddr(s); err == nil {
				out = append(out, a)
			}
		}
		for _, ip := range nic.IPs {
			if a, err := netip.ParseAddr(ip.Addr); err == nil {
				out = append(out, a)
			}
		}
	}
	return out, nil
}

var (
	netKeyRe = regexp.MustCompile(`^net[0-9]+$`)
	macRe    = regexp.MustCompile(`(?i)^([0-9a-f]{2}:){5}[0-9a-f]{2}$`)
)

// configMACs reads the MACs of a guest config's netN entries: QEMU writes
// "virtio=BC:24:11:..,bridge=vmbr0", LXC "name=eth0,hwaddr=BC:24:11:..,...".
func configMACs(cfg map[string]any) []string {
	var out []string
	for k, v := range cfg {
		s, ok := v.(string)
		if !ok || !netKeyRe.MatchString(k) {
			continue
		}
		for _, part := range strings.Split(s, ",") {
			_, val, ok := strings.Cut(part, "=")
			if ok && macRe.MatchString(val) {
				out = append(out, strings.ToLower(val))
				break
			}
		}
	}
	return out
}

// splitTags splits Proxmox's tag list (";" between tags; older versions also
// "," or spaces).
func splitTags(s string) []string {
	var out []string
	for _, t := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == ',' || r == ' ' }) {
		out = append(out, strings.ToLower(t))
	}
	return out
}
