package main

import (
	"net"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render"
)

// liveIPv4 returns the IPv4 networks a device has now.
func liveIPv4(dev string) []netip.Prefix {
	ifc, err := net.InterfaceByName(dev)
	if err != nil {
		return nil
	}
	addrs, _ := ifc.Addrs()
	var out []netip.Prefix
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(a.String()); err == nil && p.Addr().Is4() {
			out = append(out, p)
		}
	}
	return out
}

// ifaceNet is the network of an interface or VLAN with a static address.
type ifaceNet struct {
	name string
	net  netip.Prefix
}

// interfaceNets lists the enabled interfaces' and VLANs' networks, longest
// prefix first. An interface without a static address (a WAN by DHCP) uses
// the addresses its device has now (live; nil = none).
func interfaceNets(cfg *config.Config, live func(dev string) []netip.Prefix) []ifaceNet {
	var out []ifaceNet
	devs := render.Devices(cfg)
	add := func(name string, enabled bool, p *netip.Prefix) {
		switch {
		case !enabled:
		case p != nil:
			out = append(out, ifaceNet{name, p.Masked()})
		case live != nil:
			for _, lp := range live(devs[name]) {
				out = append(out, ifaceNet{name, lp.Masked()})
			}
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
				nets = interfaceNets(cfg, liveIPv4)
			}
			loaded = time.Now()
		}
		return lookupInterface(nets, ip)
	}
}
