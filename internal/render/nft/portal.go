package nft

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Captive portal sets in table inet goblin. goblind fills them at run time
// (a ruleset load empties them, so they are refilled after every apply):
// PortalSet holds the let-in clients' MAC . IPv4 with the session's time
// left, PortalMACSet their MACs for IPv6, PortalSeenSet the clients that sent
// traffic within the idle timeout (updated by the forward rules).
const (
	PortalSet     = "portal_ok"
	PortalMACSet  = "portal_mac"
	PortalSeenSet = "portal_seen"
	portalNAT     = "portal_redirect"
)

// PortalClient is one let-in client for PortalScript.
type PortalClient struct {
	MAC     string
	IP      string
	Timeout time.Duration
}

// PortalScript is an `nft -f` script that adds clients to the portal sets.
func PortalScript(clients []PortalClient) []byte {
	var b strings.Builder
	for _, c := range clients {
		t := int(c.Timeout.Seconds())
		if t < 1 {
			continue
		}
		fmt.Fprintf(&b, "add element inet %s %s { %s . %s timeout %ds }\n", FilterTable, PortalSet, c.MAC, c.IP, t)
		fmt.Fprintf(&b, "add element inet %s %s { %s timeout %ds }\n", FilterTable, PortalMACSet, c.MAC, t)
	}
	return []byte(b.String())
}

// PortalRemoveScript removes one client from the portal sets. Each element
// gets its own command so a missing one does not stop the others (the caller
// runs each line separately).
func PortalRemoveScript(mac, ip string) []string {
	out := []string{
		fmt.Sprintf("delete element inet %s %s { %s . %s }", FilterTable, PortalSet, mac, ip),
		fmt.Sprintf("delete element inet %s %s { %s . %s }", FilterTable, PortalSeenSet, mac, ip),
	}
	return append(out, fmt.Sprintf("delete element inet %s %s { %s }", FilterTable, PortalMACSet, mac))
}

type portalRules struct {
	// forward, before the established accept: a removed guest is cut off at
	// once (no conntrack flush) and the idle tracker sees every packet
	top  []string
	in   []string // input, early
	nat  []string // chain portal_redirect (nat prerouting)
	sets []string // extra set declarations, already laid out
	idle bool
}

// portal adds the rules of the active captive portals.
func (r *ruleset) portal() {
	active := r.cfg.ActivePortals()
	if len(active) == 0 {
		return
	}
	addrs := r.cfg.PortalAddrs()
	names := make([]string, 0, len(active))
	for n := range active {
		names = append(names, n)
	}
	slices.Sort(names)
	p := &portalRules{}
	key := "ether saddr . ip saddr"
	for i, name := range names {
		pt := active[name]
		dev, ok := r.devs[name]
		if !ok {
			continue
		}
		in := "iifname " + quote(dev)
		box := addrs[name].Prefix.Addr()
		c := func(s string) string { return quote("portal " + name + ": " + s) }
		p.in = append(p.in,
			fmt.Sprintf("%s udp dport 67 accept comment %s", in, c("dhcp")),
			fmt.Sprintf("%s ip daddr %s meta l4proto { tcp, udp } th dport 53 accept comment %s", in, box, c("dns")),
			fmt.Sprintf("%s ip daddr %s tcp dport %d accept comment %s", in, box, config.PortalPort, c("portal page")),
			fmt.Sprintf("%s icmpv6 type { nd-neighbor-solicit, nd-neighbor-advert, nd-router-solicit } accept comment %s", in, c("ipv6 neighbour discovery")),
			fmt.Sprintf("%s %s != @%s drop comment %s", in, key, PortalSet, c("not let in yet")),
			fmt.Sprintf("%s meta nfproto ipv6 ether saddr != @%s drop comment %s", in, PortalMACSet, c("not let in yet (ipv6)")),
		)
		p.top = append(p.top,
			fmt.Sprintf("%s %s != @%s tcp dport 443 reject with tcp reset comment %s", in, key, PortalSet, c("https refused until let in")),
			fmt.Sprintf("%s %s != @%s reject with icmpx type admin-prohibited comment %s", in, key, PortalSet, c("not let in yet")),
			fmt.Sprintf("%s meta nfproto ipv6 ether saddr != @%s reject with icmpx type admin-prohibited comment %s", in, PortalMACSet, c("not let in yet (ipv6)")),
		)
		if pt.IdleMinutes > 0 {
			p.idle = true
			p.top = append(p.top, fmt.Sprintf("%s %s @%s update @%s { %s timeout %dm } comment %s", in, key, PortalSet, PortalSeenSet, key, pt.IdleMinutes, c("traffic seen")))
		}
		if pt.RateLimitMbps > 0 {
			kbytes := pt.RateLimitMbps * 125
			up, down := fmt.Sprintf("portal_up_%d", i), fmt.Sprintf("portal_down_%d", i)
			for _, s := range []string{up, down} {
				p.sets = append(p.sets, fmt.Sprintf("set %s {\n\t\ttype ipv4_addr\n\t\tsize 65535\n\t\tflags dynamic,timeout\n\t\ttimeout 1m\n\t\tcomment %s\n\t}", s, c("rate per client")))
			}
			p.top = append(p.top,
				fmt.Sprintf("%s update @%s { ip saddr limit rate over %d kbytes/second } drop comment %s", in, up, kbytes, c("upload limit")),
				fmt.Sprintf("oifname %s update @%s { ip daddr limit rate over %d kbytes/second } drop comment %s", quote(dev), down, kbytes, c("download limit")),
			)
		}
		p.nat = append(p.nat, fmt.Sprintf("%s tcp dport 80 %s != @%s dnat ip to %s:%d comment %s", in, key, PortalSet, box, config.PortalPort, c("http to the portal")))
	}
	r.portalR = p
}

// writePortalSets lays out the portal sets (inside table inet goblin).
func (r *ruleset) writePortalSets(p func(int, string, ...any)) {
	if r.portalR == nil {
		return
	}
	p(1, "set %s {", PortalSet)
	p(2, "type ether_addr . ipv4_addr")
	p(2, "flags timeout")
	p(2, "comment %s", quote("captive portal: let-in clients"))
	p(1, "}")
	p(0, "")
	p(1, "set %s {", PortalMACSet)
	p(2, "type ether_addr")
	p(2, "flags timeout")
	p(2, "comment %s", quote("captive portal: let-in clients (ipv6)"))
	p(1, "}")
	p(0, "")
	if r.portalR.idle {
		p(1, "set %s {", PortalSeenSet)
		p(2, "type ether_addr . ipv4_addr")
		p(2, "size 65535")
		p(2, "flags dynamic,timeout")
		p(2, "comment %s", quote("captive portal: clients with recent traffic"))
		p(1, "}")
		p(0, "")
	}
	for _, s := range r.portalR.sets {
		p(1, "%s", s)
		p(0, "")
	}
}

// writePortalNAT lays out the HTTP redirect chain (inside table inet goblin).
func (r *ruleset) writePortalNAT(p func(int, string, ...any)) {
	if r.portalR == nil {
		return
	}
	p(0, "")
	p(1, "chain %s {", portalNAT)
	p(2, "type nat hook prerouting priority dstnat; policy accept;")
	for _, l := range r.portalR.nat {
		p(2, "%s", l)
	}
	p(1, "}")
}

func (r *ruleset) portalLines(which string) []string {
	if r.portalR == nil {
		return nil
	}
	switch which {
	case "top":
		return r.portalR.top
	case "in":
		return r.portalR.in
	}
	return nil
}
