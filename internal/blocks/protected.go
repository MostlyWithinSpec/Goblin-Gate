package blocks

import (
	"net/netip"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Check describes a block request for Protected.
type Check struct {
	// Requester is the address of the admin asking (never blockable); zero
	// for plugins.
	Requester netip.Addr
	// AllowInternal lets an admin block an address inside the box's own
	// networks (LAN side, VPN). Plugins never may.
	AllowInternal bool
	// Live are addresses learned at run time that must stay reachable:
	// the box's own (also DHCP ones) and its current gateways.
	Live []netip.Addr
}

// Protected returns why ip must not be blocked, or "" when it may be.
// Protected: special addresses, the box itself, gateways, DNS and NTP
// servers it uses, the requesting admin, and (unless AllowInternal) every
// address inside the box's own networks.
func Protected(cfg *config.Config, ip netip.Addr, c Check) string {
	ip = ip.Unmap()
	switch {
	case !ip.IsValid():
		return "not an IP address"
	case ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast():
		return "a special-purpose address"
	case ip == netip.AddrFrom4([4]byte{255, 255, 255, 255}):
		return "a special-purpose address"
	}
	if c.Requester.IsValid() && c.Requester.Unmap() == ip {
		return "your own address"
	}
	for _, a := range c.Live {
		if a.Unmap() == ip {
			return "an address of the box or one of its gateways"
		}
	}
	reason := ""
	is := func(a netip.Addr, why string) {
		if reason == "" && a.IsValid() && a.Unmap() == ip {
			reason = why
		}
	}
	internal := false
	inside := func(p netip.Prefix) {
		if p.IsValid() && p.Masked().Contains(ip) {
			internal = true
		}
	}
	addr := func(p *netip.Prefix, gw *netip.Addr, v6 *config.IPv6, lan bool) {
		if p != nil {
			is(p.Addr(), "an address of the box")
			if lan {
				inside(*p)
			}
		}
		if gw != nil {
			is(*gw, "a gateway")
		}
		if v6 != nil {
			if v6.Address != nil {
				is(v6.Address.Addr(), "an address of the box")
				if lan {
					inside(*v6.Address)
				}
			}
			if v6.Gateway != nil {
				is(*v6.Gateway, "a gateway")
			}
		}
	}
	wan := map[string]bool{}
	for _, i := range cfg.Interfaces {
		wan[i.Name] = i.Role == "wan"
		addr(i.Address, i.Gateway, i.IPv6, i.Role != "wan")
	}
	for _, v := range cfg.VLANs {
		addr(v.Address, v.Gateway, v.IPv6, !wan[v.Parent])
	}
	for _, g := range cfg.Gateways {
		if a, err := netip.ParseAddr(g.Address); err == nil {
			is(a, "a gateway")
		}
	}
	for _, wg := range cfg.VPN.WireGuard {
		is(wg.Address.Addr(), "an address of the box")
		inside(wg.Address)
	}
	for _, wc := range cfg.VPN.WireGuardClients {
		is(wc.Address.Addr(), "an address of the box")
	}
	for _, a := range cfg.DNS.Forwarders {
		is(a, "a DNS server the box uses")
	}
	for _, f := range cfg.DNS.DomainForwards {
		for _, a := range f.Servers {
			is(a, "a DNS server the box uses")
		}
	}
	for _, a := range cfg.System.DNSServers {
		is(a, "a DNS server the box uses")
	}
	for _, s := range cfg.System.NTPServers {
		if a, err := netip.ParseAddr(s); err == nil {
			is(a, "an NTP server the box uses")
		}
	}
	if cfg.VPN.Tailscale.Enabled {
		inside(netip.MustParsePrefix("100.64.0.0/10")) // the tailnet
	}
	if reason != "" {
		return reason
	}
	if internal && !c.AllowInternal {
		return "inside one of the box's own networks (an admin can allow this explicitly)"
	}
	return ""
}
