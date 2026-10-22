package main

import (
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// ifaceNet is the network of an interface or VLAN with a static address.
type ifaceNet struct {
	name string
	net  netip.Prefix
}

// interfaceNets lists the enabled interfaces' and VLANs' networks, longest
// prefix first.
func interfaceNets(cfg *config.Config) []ifaceNet {
	var out []ifaceNet
	add := func(name string, enabled bool, p *netip.Prefix) {
		if enabled && p != nil {
			out = append(out, ifaceNet{name, p.Masked()})
		}
	}
	for _, i := range cfg.Interfaces {
		add(i.Name, i.Enabled, i.Address)
	}
	for _, v := range cfg.VLANs {
		add(v.Name, v.Enabled, v.Address)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].net.Bits() > out[j].net.Bits() })
	return out
}

// lookupInterface names the network in nets that holds ip ("" = none).
func lookupInterface(nets []ifaceNet, ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	a = a.Unmap()
	for _, n := range nets {
		if n.net.Contains(a) {
			return n.name
		}
	}
	return ""
}

// interfaceForAddr names the interface or VLAN whose network holds ip ("" =
// an outside address). Traffic events from the IPS's flow records come
// without an interface; their source address tells which network they began
// on. The networks are re-read from the running config every 10 s.
func interfaceForAddr(store *config.Store) func(string) string {
	var (
		mu     sync.Mutex
		nets   []ifaceNet
		loaded time.Time
	)
	return func(ip string) string {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(loaded) > 10*time.Second {
			if cfg, err := store.Running(); err == nil {
				nets = interfaceNets(cfg)
			}
			loaded = time.Now()
		}
		return lookupInterface(nets, ip)
	}
}
