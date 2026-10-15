package netif

import (
	"fmt"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render"
)

// IPv6 (task 045) in systemd-networkd terms (Debian 12 ships systemd 252):
//
//   - dhcp6 (WAN): DHCP=ipv6 (or yes with IPv4 DHCP), router advertisements
//     accepted (SLAAC address, default route), DHCPv6 always solicited -
//     also when the ISP's RA lacks the M flag - asking for a /pd_size prefix.
//   - delegated (LAN side): DHCPPrefixDelegation=yes takes /64 number
//     prefix_id (SubnetId) from the uplink's delegated prefix; the box is
//     ::1 in it (Token) and announces it with router advertisements.
//   - static: the address (and gateway on WAN); LAN-side static /64s are
//     announced with an [IPv6Prefix] section.
//
// Router advertisements carry the box's link-local address as DNS server
// (RDNSS), except on interfaces with a DNS or web filter: their clients must
// use the box over IPv4, where Unbound knows which filter applies (the
// delegated prefix is not known when Unbound's tags are rendered).

// v6Info resolves uplinks and DNS filtering for the IPv6 links.
type v6Info struct {
	devs     map[string]string
	dhcp6    []string // names of enabled dhcp6 interfaces/VLANs
	filtered map[string]bool
}

func newV6Info(cfg *config.Config, devs map[string]string) *v6Info {
	in := &v6Info{devs: devs, filtered: map[string]bool{}}
	add := func(name string, enabled bool, x *config.IPv6) {
		if enabled && x != nil && x.Mode == "dhcp6" {
			in.dhcp6 = append(in.dhcp6, name)
		}
	}
	for _, i := range cfg.Interfaces {
		add(i.Name, i.Enabled, i.IPv6)
	}
	for _, v := range cfg.VLANs {
		add(v.Name, v.Enabled, v.IPv6)
	}
	for _, list := range [][]render.ProfileAssignment{render.DNSFilterAssignments(cfg), render.WebFilterAssignments(cfg), render.AppCtrlAssignments(cfg)} {
		for _, a := range list {
			in.filtered[a.Interface] = true
		}
	}
	return in
}

// attach sets a link's IPv6 fields.
func (in *v6Info) attach(l *link, x *config.IPv6) error {
	l.v6 = x
	if x == nil || x.Mode != "delegated" {
		l.raDNS = x != nil && !in.filtered[l.name]
		return nil
	}
	up := x.Uplink
	if up == "" {
		if len(in.dhcp6) != 1 {
			return fmt.Errorf("ipv6: delegated needs an uplink (%d dhcp6 interfaces)", len(in.dhcp6))
		}
		up = in.dhcp6[0]
	}
	dev, ok := in.devs[up]
	if !ok {
		return fmt.Errorf("ipv6: unknown uplink %q", up)
	}
	l.v6uplink = dev
	l.raDNS = !in.filtered[l.name]
	return nil
}

// ipv6Network writes the IPv6 lines of the [Network] section and returns the
// extra sections that follow it.
func ipv6Network(b *strings.Builder, l *link) (string, error) {
	x := l.v6
	var s strings.Builder
	b.WriteString("LinkLocalAddressing=ipv6\n")
	switch x.Mode {
	case "dhcp6":
		size := x.PDSize
		if size == 0 {
			size = config.DefaultPDSize
		}
		b.WriteString("IPv6AcceptRA=yes\n")
		fmt.Fprintf(&s, "\n[DHCPv6]\nPrefixDelegationHint=::/%d\nWithoutRA=solicit\n", size)
		s.WriteString("\n[IPv6AcceptRA]\nDHCPv6Client=always\n")
		return s.String(), nil
	case "static":
		if x.Address == nil || !x.Address.Addr().Is6() {
			return "", fmt.Errorf("ipv6: static mode needs an IPv6 address")
		}
		fmt.Fprintf(b, "Address=%s\n", x.Address)
		if l.wan { // no router advertisements of its own; without a gateway, learn the route from RAs
			if x.Gateway != nil {
				fmt.Fprintf(b, "Gateway=%s\nIPv6AcceptRA=no\n", x.Gateway)
			} else {
				b.WriteString("IPv6AcceptRA=yes\n")
			}
			return "", nil
		}
		b.WriteString("IPv6AcceptRA=no\n")
		ra := x.EffectiveRA()
		if !ra.Enabled {
			return "", nil
		}
		b.WriteString("IPv6SendRA=yes\n")
		s.WriteString(raSection(ra, l.raDNS))
		fmt.Fprintf(&s, "\n[IPv6Prefix]\nPrefix=%s\n", x.Address.Masked())
		return s.String(), nil
	case "delegated":
		ra := x.EffectiveRA()
		b.WriteString("IPv6AcceptRA=no\nDHCPPrefixDelegation=yes\n")
		if ra.Enabled {
			b.WriteString("IPv6SendRA=yes\n")
		}
		fmt.Fprintf(&s, "\n[DHCPPrefixDelegation]\nUplinkInterface=%s\nSubnetId=0x%x\nAnnounce=%s\nToken=::1\n",
			l.v6uplink, x.PrefixID, yesNo(ra.Enabled))
		if ra.Enabled {
			s.WriteString(raSection(ra, l.raDNS))
		}
		return s.String(), nil
	}
	return "", fmt.Errorf("ipv6: unknown mode %q", x.Mode)
}

func raSection(ra config.RA, dns bool) string {
	s := fmt.Sprintf("\n[IPv6SendRA]\nManaged=%s\nOtherInformation=%s\n", yesNo(ra.Managed), yesNo(ra.Other))
	if ra.DNS && dns {
		return s + "EmitDNS=yes\nDNS=_link_local\n"
	}
	return s + "EmitDNS=no\n"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
