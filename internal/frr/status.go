// Package frr reads the state of FRR's routing daemons through vtysh: BGP
// sessions, OSPF adjacencies and the routes they learned.
package frr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// Runner runs vtysh with args and returns its standard output. It returns
// ErrNotRunning when vtysh is missing or reaches no daemon.
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// ErrNotRunning: FRR is not installed or not running.
var ErrNotRunning = errors.New("frr is not running")

// Commands are the vtysh commands the status is read from, by name.
var Commands = map[string]string{
	"bgp_summary":    "show bgp summary json",
	"ospf_neighbors": "show ip ospf neighbor json",
	"routes4_bgp":    "show ip route bgp json",
	"routes4_ospf":   "show ip route ospf json",
	"routes6_bgp":    "show ipv6 route bgp json",
}

// Neighbor is one BGP session.
type Neighbor struct {
	Address          string `json:"address"`
	Family           string `json:"family"` // ipv4 | ipv6
	RemoteAS         uint32 `json:"remote_as"`
	Description      string `json:"description,omitempty"`
	State            string `json:"state"` // Established, Active, Connect, Idle, ...
	Up               bool   `json:"up"`
	UptimeSeconds    int64  `json:"uptime_seconds"` // in the current state when up
	PrefixesReceived int    `json:"prefixes_received"`
	PrefixesSent     int    `json:"prefixes_sent"`
}

// OSPFNeighbor is one OSPF adjacency.
type OSPFNeighbor struct {
	RouterID  string `json:"router_id"`
	Address   string `json:"address"`
	Interface string `json:"interface"`
	State     string `json:"state"` // Full/DR, 2-Way/DROther, Init, ...
	Full      bool   `json:"full"`
}

// Route is one learned route.
type Route struct {
	Prefix    string   `json:"prefix"`
	Protocol  string   `json:"protocol"` // bgp | ospf
	Selected  bool     `json:"selected"` // the best route to the prefix
	Installed bool     `json:"installed"`
	Distance  int      `json:"distance"`
	Metric    int      `json:"metric"`
	NextHops  []string `json:"next_hops"`
	Interface string   `json:"interface,omitempty"`
}

// Status is what FRR reports.
type Status struct {
	Running bool           `json:"running"`
	BGP     []Neighbor     `json:"bgp"`
	OSPF    []OSPFNeighbor `json:"ospf"`
	Routes  []Route        `json:"routes"`
}

// Read asks FRR for its state. FRR not running is not an error: Running is
// false and the lists are empty.
func Read(ctx context.Context, run Runner) (Status, error) {
	st := Status{BGP: []Neighbor{}, OSPF: []OSPFNeighbor{}, Routes: []Route{}}
	get := func(name string) ([]byte, error) {
		return run(ctx, "-c", Commands[name])
	}
	sum, err := get("bgp_summary")
	if err != nil {
		if errors.Is(err, ErrNotRunning) {
			return st, nil
		}
		return st, fmt.Errorf("frr: %s: %w", Commands["bgp_summary"], err)
	}
	st.Running = true
	if st.BGP, err = ParseBGPSummary(sum); err != nil {
		return st, err
	}
	if out, err := get("ospf_neighbors"); err == nil {
		if st.OSPF, err = ParseOSPFNeighbors(out); err != nil {
			return st, err
		}
	}
	for _, name := range []string{"routes4_bgp", "routes4_ospf", "routes6_bgp"} {
		out, err := get(name)
		if err != nil {
			continue // that daemon is not running
		}
		rs, err := ParseRoutes(out)
		if err != nil {
			return st, err
		}
		st.Routes = append(st.Routes, rs...)
	}
	slices.SortFunc(st.Routes, func(a, b Route) int {
		return strings.Compare(a.Protocol+" "+a.Prefix, b.Protocol+" "+b.Prefix)
	})
	return st, nil
}

// unmarshal tolerates vtysh's empty answer (a daemon without config).
func unmarshal(data []byte, v any) error {
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	return json.Unmarshal(data, v)
}

// ParseBGPSummary reads `show bgp summary json`.
func ParseBGPSummary(data []byte) ([]Neighbor, error) {
	var sum map[string]struct {
		Peers map[string]struct {
			RemoteAS       uint32 `json:"remoteAs"`
			Desc           string `json:"desc"`
			State          string `json:"state"`
			PeerUptimeMsec int64  `json:"peerUptimeMsec"`
			PfxRcd         int    `json:"pfxRcd"`
			PfxSnt         int    `json:"pfxSnt"`
		} `json:"peers"`
	}
	if err := unmarshal(data, &sum); err != nil {
		return nil, fmt.Errorf("frr: bgp summary: %w", err)
	}
	out := []Neighbor{}
	for af, s := range sum {
		fam := map[string]string{"ipv4Unicast": "ipv4", "ipv6Unicast": "ipv6"}[af]
		if fam == "" {
			continue
		}
		for addr, p := range s.Peers {
			n := Neighbor{Address: addr, Family: fam, RemoteAS: p.RemoteAS, Description: p.Desc, State: p.State,
				Up: p.State == "Established", PrefixesReceived: p.PfxRcd, PrefixesSent: p.PfxSnt}
			if n.Up {
				n.UptimeSeconds = p.PeerUptimeMsec / 1000
			}
			out = append(out, n)
		}
	}
	slices.SortFunc(out, func(a, b Neighbor) int { return strings.Compare(a.Family+a.Address, b.Family+b.Address) })
	return out, nil
}

// ParseOSPFNeighbors reads `show ip ospf neighbor json` (FRR 8 and later
// name some fields differently; both are read).
func ParseOSPFNeighbors(data []byte) ([]OSPFNeighbor, error) {
	var v struct {
		Neighbors map[string][]struct {
			State         string `json:"state"`
			NbrState      string `json:"nbrState"`
			Address       string `json:"address"`
			IfaceAddress  string `json:"ifaceAddress"`
			IfaceName     string `json:"ifaceName"`
			InterfaceName string `json:"interfaceName"`
		} `json:"neighbors"`
	}
	if err := unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("frr: ospf neighbors: %w", err)
	}
	out := []OSPFNeighbor{}
	for rid, list := range v.Neighbors {
		for _, n := range list {
			state := cmp(n.NbrState, n.State)
			ifc := cmp(n.InterfaceName, n.IfaceName)
			ifc, _, _ = strings.Cut(ifc, ":") // "eth0:10.99.0.2"
			out = append(out, OSPFNeighbor{RouterID: rid, Address: cmp(n.IfaceAddress, n.Address), Interface: ifc,
				State: state, Full: strings.HasPrefix(state, "Full")})
		}
	}
	slices.SortFunc(out, func(a, b OSPFNeighbor) int { return strings.Compare(a.RouterID+a.Interface, b.RouterID+b.Interface) })
	return out, nil
}

func cmp(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ParseRoutes reads `show ip route <protocol> json`.
func ParseRoutes(data []byte) ([]Route, error) {
	var v map[string][]struct {
		Prefix    string `json:"prefix"`
		Protocol  string `json:"protocol"`
		Selected  bool   `json:"selected"`
		Installed bool   `json:"installed"`
		Distance  int    `json:"distance"`
		Metric    int    `json:"metric"`
		NextHops  []struct {
			IP            string `json:"ip"`
			InterfaceName string `json:"interfaceName"`
			Active        bool   `json:"active"`
		} `json:"nexthops"`
	}
	if err := unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("frr: routes: %w", err)
	}
	out := []Route{}
	for prefix, list := range v {
		for _, r := range list {
			rt := Route{Prefix: cmp(r.Prefix, prefix), Protocol: r.Protocol, Selected: r.Selected, Installed: r.Installed,
				Distance: r.Distance, Metric: r.Metric, NextHops: []string{}}
			for _, nh := range r.NextHops {
				if nh.IP != "" {
					rt.NextHops = append(rt.NextHops, nh.IP)
				}
				if rt.Interface == "" {
					rt.Interface = nh.InterfaceName
				}
			}
			out = append(out, rt)
		}
	}
	return out, nil
}

// VTYSH is the Runner for the box: vtysh as root. A missing vtysh or one that
// reaches no daemon is ErrNotRunning.
func VTYSH(ctx context.Context, args ...string) ([]byte, error) {
	path, err := exec.LookPath("vtysh")
	if err != nil {
		return nil, ErrNotRunning
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if strings.Contains(stderr.String(), "failed to connect") {
			return nil, ErrNotRunning
		}
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}
