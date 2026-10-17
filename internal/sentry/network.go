package sentry

import (
	"net/netip"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Network is what Sentry knows about the box's networks (from the running
// config): which addresses are internal, and names of known hosts.
type Network struct {
	internal []namedPrefix
	box      map[netip.Addr]bool
	names    map[netip.Addr]string
}

type namedPrefix struct {
	name string
	p    netip.Prefix
}

// NetworkFrom reads the networks of a config: LAN-side interfaces and
// VLANs, WireGuard servers and the tailnet; host names from static DHCP
// leases and local DNS records.
func NetworkFrom(cfg *config.Config) *Network {
	n := &Network{box: map[netip.Addr]bool{}, names: map[netip.Addr]string{}}
	add := func(name string, p *netip.Prefix, v6 *config.IPv6, lan bool) {
		if p != nil {
			n.box[p.Addr()] = true
			if lan {
				n.internal = append(n.internal, namedPrefix{name, p.Masked()})
			}
		}
		if v6 != nil && v6.Address != nil {
			n.box[v6.Address.Addr()] = true
			if lan {
				n.internal = append(n.internal, namedPrefix{name, v6.Address.Masked()})
			}
		}
	}
	wan := map[string]bool{}
	for _, i := range cfg.Interfaces {
		wan[i.Name] = i.Role == "wan"
		add(i.Name, i.Address, i.IPv6, i.Role != "wan")
	}
	for _, v := range cfg.VLANs {
		add(v.Name, v.Address, v.IPv6, !wan[v.Parent])
	}
	for _, wg := range cfg.VPN.WireGuard {
		a := wg.Address
		add(wg.Name, &a, nil, true)
	}
	if cfg.VPN.Tailscale.Enabled {
		n.internal = append(n.internal, namedPrefix{"tailscale", netip.MustParsePrefix("100.64.0.0/10")})
	}
	for _, d := range cfg.DHCP {
		for _, l := range d.StaticLeases {
			if l.Hostname != "" {
				n.names[l.IP] = l.Hostname
			}
		}
	}
	for name, ip := range cfg.DNS.LocalRecords {
		if a, err := netip.ParseAddr(ip); err == nil {
			if _, ok := n.names[a]; !ok {
				n.names[a] = strings.TrimSuffix(name, ".")
			}
		}
	}
	return n
}

// where returns the internal network of an address, "" when it is outside.
func (n *Network) where(ip string) (string, bool) {
	a, err := netip.ParseAddr(ip)
	if err != nil || n == nil {
		return "", false
	}
	a = a.Unmap()
	if n.box[a] {
		return "box", true
	}
	for _, p := range n.internal {
		if p.p.Contains(a) {
			return p.name, true
		}
	}
	return "", false
}

// classify fills direction, remote and local of an alert.
func (n *Network) classify(a Alert, e *Enrichment) {
	srcNet, srcIn := n.where(a.SrcIP)
	dstNet, dstIn := n.where(a.DstIP)
	local := func(ip, netName string) {
		e.Local, e.LocalIntf = ip, netName
		if addr, err := netip.ParseAddr(ip); err == nil && n != nil {
			e.LocalName = n.names[addr.Unmap()]
		}
	}
	switch {
	case srcIn && dstIn:
		e.Direction = Lateral
		local(a.SrcIP, srcNet)
	case srcIn:
		e.Direction, e.Remote = Outbound, a.DstIP
		local(a.SrcIP, srcNet)
	case dstIn:
		e.Direction, e.Remote = Inbound, a.SrcIP
		local(a.DstIP, dstNet)
	default:
		e.Direction, e.Remote = External, a.SrcIP
	}
}
