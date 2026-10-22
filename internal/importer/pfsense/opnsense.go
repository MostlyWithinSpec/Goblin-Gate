package pfsense

import (
	"encoding/xml"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

// OPNsense (task 115). OPNsense grew out of pfSense, so its config.xml
// keeps many pfSense sections as they were (system, interfaces, VLANs, NAT
// port forwards, schedules, ISC DHCP, older filter rules). Newer releases
// moved parts into "MVC" models under <OPNsense>: firewall rules, outbound
// NAT, aliases, gateways, Unbound, Kea, and dnsmasq for DHCP. normalizeOPN
// turns those into the pfSense shapes, so one conversion serves both.
//
// Never imported, as for pfSense: users' password hashes, API keys, OTP
// seeds, certificates and private keys; nor alias download credentials or
// Kea's DDNS key (they are not even kept in memory past parsing).

// opnMVC is the <OPNsense> element.
type opnMVC struct {
	Firewall struct {
		Alias struct {
			Aliases []opnAlias `xml:"aliases>alias"`
		} `xml:"Alias"`
		Filter struct {
			Settings struct {
				NAT struct {
					SNATMode string `xml:"snat_mode"`
				} `xml:"nat"`
			} `xml:"settings"`
			Rules     []opnRule `xml:"rules>rule"`
			SNATRules []opnSNAT `xml:"snatrules>rule"`
		} `xml:"Filter"`
	} `xml:"Firewall"`
	Gateways struct {
		Items []opnGateway `xml:"gateway_item"`
	} `xml:"Gateways"`
	Unbound *opnUnbound `xml:"unboundplus"`
	Kea     struct {
		DHCP4 *opnKea `xml:"dhcp4"`
	} `xml:"Kea"`
	// Modules lists every other model present (IDS, wireguard, ...), for
	// the report.
	Modules []opnModule `xml:",any"`
}

type opnModule struct {
	XMLName xml.Name
	Inner   []opnModule `xml:",any"`
}

type opnAlias struct {
	Enabled     string `xml:"enabled"`
	Name        string `xml:"name"`
	Type        string `xml:"type"`
	Content     string `xml:"content"` // one entry per line
	Description string `xml:"description"`
	// credentials of a URL alias: only whether they were set is kept
	Username secretFlag `xml:"username"`
	Password secretFlag `xml:"password"`
}

// secretFlag records whether an element had content and drops the content.
type secretFlag bool

func (f *secretFlag) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	var s string
	if err := d.DecodeElement(&s, &start); err != nil {
		return err
	}
	*f = strings.TrimSpace(s) != ""
	return nil
}

type opnRule struct {
	Enabled         string `xml:"enabled"`
	Sequence        string `xml:"sequence"`
	Action          string `xml:"action"` // pass | block | reject | match
	Quick           string `xml:"quick"`
	InterfaceNot    string `xml:"interfacenot"`
	Interface       string `xml:"interface"` // comma list
	Direction       string `xml:"direction"`
	IPProtocol      string `xml:"ipprotocol"`
	Protocol        string `xml:"protocol"`
	ICMPType        string `xml:"icmptype"`
	SourceNet       string `xml:"source_net"`
	SourceNot       string `xml:"source_not"`
	SourcePort      string `xml:"source_port"`
	DestinationNet  string `xml:"destination_net"`
	DestinationNot  string `xml:"destination_not"`
	DestinationPort string `xml:"destination_port"`
	Gateway         string `xml:"gateway"`
	Log             string `xml:"log"`
	Sched           string `xml:"sched"`
	Description     string `xml:"description"`
}

type opnSNAT struct {
	Enabled       string `xml:"enabled"`
	NoNAT         string `xml:"nonat"`
	Sequence      string `xml:"sequence"`
	Interface     string `xml:"interface"`
	SourceNet     string `xml:"source_net"`
	SourceNot     string `xml:"source_not"`
	Target        string `xml:"target"`
	StaticNATPort string `xml:"staticnatport"`
	Description   string `xml:"description"`
}

type opnGateway struct {
	Disabled       string `xml:"disabled"`
	Name           string `xml:"name"`
	Descr          string `xml:"descr"`
	Interface      string `xml:"interface"`
	IPProtocol     string `xml:"ipprotocol"`
	Gateway        string `xml:"gateway"`
	DefaultGW      string `xml:"defaultgw"`
	MonitorDisable string `xml:"monitor_disable"`
	Monitor        string `xml:"monitor"`
}

type opnUnbound struct {
	General struct {
		Enabled string `xml:"enabled"`
		DNSSEC  string `xml:"dnssec"`
		RegDHCP string `xml:"regdhcp"`
	} `xml:"general"`
	Forwarding struct {
		Enabled string `xml:"enabled"`
	} `xml:"forwarding"`
	Hosts []struct {
		Enabled     string `xml:"enabled"`
		Hostname    string `xml:"hostname"`
		Domain      string `xml:"domain"`
		RR          string `xml:"rr"`
		Server      string `xml:"server"`
		Description string `xml:"description"`
	} `xml:"hosts>host"`
	Dots []struct {
		Enabled string `xml:"enabled"`
		Type    string `xml:"type"` // dot | forward
		Domain  string `xml:"domain"`
		Server  string `xml:"server"`
		Port    string `xml:"port"`
	} `xml:"dots>dot"`
}

type opnKea struct {
	General struct {
		Enabled       string `xml:"enabled"`
		Interfaces    string `xml:"interfaces"`
		ValidLifetime string `xml:"valid_lifetime"`
	} `xml:"general"`
	Subnets []struct {
		UUID          string `xml:"uuid,attr"`
		Subnet        string `xml:"subnet"`
		ValidLifetime string `xml:"valid_lifetime"`
		Pools         string `xml:"pools"`
		Description   string `xml:"description"`
		// ddns_domain_key_secret is never read
	} `xml:"subnets>subnet4"`
	Reservations []struct {
		Subnet      string `xml:"subnet"` // subnet4 uuid
		IPAddress   string `xml:"ip_address"`
		HWAddress   string `xml:"hw_address"`
		Hostname    string `xml:"hostname"`
		Description string `xml:"description"`
	} `xml:"reservations>reservation"`
}

// pfDNSMasq is dnsmasq: a DNS forwarder on pfSense, and on newer OPNsense
// also the default DHCP server.
type pfDNSMasq struct {
	Enable     *string `xml:"enable"`
	Port       string  `xml:"port"`
	DHCPRanges []struct {
		Interface string `xml:"interface"`
		StartAddr string `xml:"start_addr"`
		EndAddr   string `xml:"end_addr"`
		LeaseTime string `xml:"lease_time"`
	} `xml:"dhcp_ranges"`
	Hosts []struct {
		Host   string `xml:"host"`
		Domain string `xml:"domain"`
		IP     string `xml:"ip"`
		HWAddr string `xml:"hwaddr"`
		Descr  string `xml:"descr"`
	} `xml:"hosts"`
}

// on is OPNsense's boolean: "1" (and anything but "0" or empty) is true.
func on(s string) bool { s = strings.TrimSpace(s); return s != "" && s != "0" }

// off clears a pfSense presence flag that OPNsense wrote as "0".
func off(p **string) {
	if *p != nil && strings.TrimSpace(**p) == "0" {
		*p = nil
	}
}

// anyNet: newer OPNsense writes "any" as a network instead of <any/>, and
// "0"/"1" for <not/>.
func anyNet(a *pfAddr) {
	if a.Network == "any" {
		a.Network, a.Any = "", set(true)
	}
	off(&a.Any)
	off(&a.Not)
}

func set(b bool) *string {
	if !b {
		return nil
	}
	s := "1"
	return &s
}

// normalizeOPN rewrites an OPNsense config into the pfSense shapes. Notes
// for the report are returned and added once the report starts.
func (im *importer) normalizeOPN() {
	pf := im.pf
	for i := range pf.Interfaces.Items {
		it := &pf.Interfaces.Items[i]
		off(&it.Enable)
		off(&it.BlockPriv)
		off(&it.BlockBogons)
	}
	for i := range pf.Filter {
		off(&pf.Filter[i].Disabled)
		off(&pf.Filter[i].Log)
	}
	for i := range pf.NAT.Rules {
		off(&pf.NAT.Rules[i].Disabled)
		off(&pf.NAT.Rules[i].NoRDR)
		anyNet(&pf.NAT.Rules[i].Source)
		anyNet(&pf.NAT.Rules[i].Destination)
	}
	for i := range pf.Filter {
		anyNet(&pf.Filter[i].Source)
		anyNet(&pf.Filter[i].Destination)
	}
	for i := range pf.DHCPD.Items {
		off(&pf.DHCPD.Items[i].Enable)
	}
	off(&pf.Unbound.Enable)
	off(&pf.Unbound.DNSSEC)
	off(&pf.Unbound.Forwarding)
	for i := range pf.Gateways.Items {
		off(&pf.Gateways.Items[i].Disabled)
		off(&pf.Gateways.Items[i].DefaultGW)
		off(&pf.Gateways.Items[i].MonitorOff)
	}
	for i := range pf.Routes {
		r := &pf.Routes[i]
		off(&r.Disabled)
		if r.Enabled != "" && !on(r.Enabled) {
			r.Disabled = set(true)
		}
	}
	if pf.DNSMasq != nil {
		off(&pf.DNSMasq.Enable)
	}
	if pf.OPN == nil {
		return
	}
	o := pf.OPN

	// aliases: content is one entry per line, which strings.Fields reads
	for _, a := range o.Firewall.Alias.Aliases {
		if a.Enabled != "" && !on(a.Enabled) {
			im.add("aliases", a.Name, Skipped, "disabled in OPNsense")
			continue
		}
		pa := pfAlias{Name: a.Name, Type: a.Type, Address: a.Content, Descr: a.Description}
		switch a.Type {
		case "host", "network", "networkgroup":
			pa.Type = "host"
		case "url", "urltable":
			pa.URL = firstLine(a.Content)
			if a.Username || a.Password {
				im.opnNotes = append(im.opnNotes, Entry{Section: "aliases", Item: a.Name, Status: Approximated,
					Detail: "the download's username/password were not imported: add them to the threat feed as an auth header"})
			}
		}
		pf.Aliases = append(pf.Aliases, pa)
	}

	// filter rules, in OPNsense's order
	rules := slicesSortedBySeq(o.Firewall.Filter.Rules, func(r opnRule) string { return r.Sequence })
	for _, r := range rules {
		if r.Action == "match" {
			im.opnNotes = append(im.opnNotes, Entry{Section: "rules", Item: ruleLabel(r.Description, r.Interface), Status: Skipped,
				Detail: "\"match\" rules (tagging, shaping) have no counterpart"})
			continue
		}
		pr := pfRule{
			Type: r.Action, Interface: r.Interface, IPProtocol: r.IPProtocol, Protocol: opnProto(r.Protocol),
			ICMPType: r.ICMPType, Descr: r.Description, Sched: r.Sched, Direction: r.Direction, Gateway: r.Gateway,
			Source:      im.opnAddr(r.SourceNet, r.SourcePort, on(r.SourceNot)),
			Destination: im.opnAddr(r.DestinationNet, r.DestinationPort, on(r.DestinationNot)),
			Disabled:    set(r.Enabled != "" && !on(r.Enabled)),
			Log:         set(on(r.Log)),
		}
		if r.Quick != "" && !on(r.Quick) {
			pr.Quick = "no"
		}
		if strings.Contains(r.Interface, ",") || on(r.InterfaceNot) || r.Interface == "" {
			pr.Floating = "yes"
		}
		pf.Filter = append(pf.Filter, pr)
	}

	// outbound NAT
	if m := o.Firewall.Filter.Settings.NAT.SNATMode; m != "" {
		pf.NAT.Outbound.Mode = m
	}
	for _, r := range slicesSortedBySeq(o.Firewall.Filter.SNATRules, func(r opnSNAT) string { return r.Sequence }) {
		if on(r.NoNAT) {
			im.opnNotes = append(im.opnNotes, Entry{Section: "nat", Item: ruleLabel(r.Description, r.Interface), Status: Skipped,
				Detail: "\"do not NAT\" exceptions are not supported: traffic is translated by the policy's NAT switch"})
			continue
		}
		var nr struct {
			Interface     string  `xml:"interface"`
			Source        pfAddr  `xml:"source"`
			Target        string  `xml:"target"`
			StaticNATPort *string `xml:"staticnatport"`
			Disabled      *string `xml:"disabled"`
			Descr         string  `xml:"descr"`
		}
		nr.Interface, nr.Target, nr.Descr = r.Interface, r.Target, r.Description
		nr.Source = im.opnAddr(r.SourceNet, "", on(r.SourceNot))
		nr.StaticNATPort = set(on(r.StaticNATPort))
		nr.Disabled = set(r.Enabled != "" && !on(r.Enabled))
		pf.NAT.Outbound.Rules = append(pf.NAT.Outbound.Rules, nr)
	}

	// gateways
	for _, g := range o.Gateways.Items {
		item := pfGatewayItem{Interface: g.Interface, Gateway: g.Gateway, Name: g.Name, IPProtocol: g.IPProtocol,
			Descr: g.Descr, DefaultGW: set(on(g.DefaultGW)), Monitor: g.Monitor, MonitorOff: set(on(g.MonitorDisable)),
			Disabled: set(on(g.Disabled))}
		pf.Gateways.Items = append(pf.Gateways.Items, item)
		if item.DefaultGW != nil && g.IPProtocol != "inet6" && pf.Gateways.DefaultGW4 == "" {
			pf.Gateways.DefaultGW4 = g.Name
		}
	}

	// Unbound
	if u := o.Unbound; u != nil {
		pf.Unbound.Enable = set(on(u.General.Enabled))
		pf.Unbound.DNSSEC = set(on(u.General.DNSSEC))
		pf.Unbound.RegDHCP = set(on(u.General.RegDHCP))
		pf.Unbound.Forwarding = set(on(u.Forwarding.Enabled))
		for _, h := range u.Hosts {
			if h.Enabled != "" && !on(h.Enabled) {
				continue
			}
			if h.RR != "" && h.RR != "A" && h.RR != "AAAA" {
				im.opnNotes = append(im.opnNotes, Entry{Section: "dns", Item: h.Hostname + "." + h.Domain, Status: Skipped,
					Detail: h.RR + " records are not supported (A and AAAA only)"})
				continue
			}
			var x struct {
				Host    string `xml:"host"`
				Domain  string `xml:"domain"`
				IP      string `xml:"ip"`
				Descr   string `xml:"descr"`
				Aliases struct {
					Items []struct {
						Host   string `xml:"host"`
						Domain string `xml:"domain"`
					} `xml:"item"`
				} `xml:"aliases"`
			}
			x.Host, x.Domain, x.IP, x.Descr = h.Hostname, h.Domain, h.Server, h.Description
			pf.Unbound.Hosts = append(pf.Unbound.Hosts, x)
		}
		for _, d := range u.Dots {
			if d.Enabled != "" && !on(d.Enabled) {
				continue
			}
			server := d.Server
			if d.Port != "" && d.Port != "53" && d.Port != "853" {
				server += "@" + d.Port
			}
			switch {
			case d.Domain != "":
				pf.Unbound.DomainOverrides = append(pf.Unbound.DomainOverrides, struct {
					Domain string `xml:"domain"`
					IP     string `xml:"ip"`
					Descr  string `xml:"descr"`
				}{Domain: d.Domain, IP: server})
			default:
				// a global forwarder: Unbound forwards every query to these
				// (and not to the system DNS servers)
				if a, err := netip.ParseAddr(d.Server); err == nil && a.Is4() {
					im.forwardTo = append(im.forwardTo, a)
				}
				if d.Type == "dot" {
					im.opnNotes = append(im.opnNotes, Entry{Section: "dns", Item: "DNS over TLS " + d.Server, Status: Approximated,
						Detail: "forwarded over plain DNS: DNS over TLS forwarding is not supported yet"})
				}
			}
		}
	}

	// DHCP from Kea or dnsmasq, when the ISC server isn't in use
	if len(pf.DHCPD.Items) == 0 {
		im.keaDHCP()
		im.dnsmasqDHCP()
	}

	// every other model: listed in the report
	handled := map[string]bool{"Firewall": true, "Gateways": true, "unboundplus": true, "Kea": true}
	var other []string
	for _, m := range o.Modules {
		if handled[m.XMLName.Local] {
			continue
		}
		other = append(other, m.XMLName.Local)
	}
	sort.Strings(other)
	for _, m := range other {
		im.opnNotes = append(im.opnNotes, Entry{Section: "packages", Item: m, Status: Skipped,
			Detail: "OPNsense's " + m + " settings have no counterpart that the importer knows"})
	}
}

// opnAddr turns a rule side (an alias, a network, an interface net like
// "lan" or "lanip", or "any") into the pfSense shape.
func (im *importer) opnAddr(net, port string, not bool) pfAddr {
	a := pfAddr{Port: port, Not: set(not)}
	net = strings.TrimSpace(net)
	switch {
	case net == "" || net == "any":
		a.Any = set(true)
	case strings.Contains(net, ","):
		// several entries: an alias of its own, so the alias code converts it
		name := unique(im.names, "opn-list")
		im.pf.Aliases = append(im.pf.Aliases, pfAlias{Name: name, Type: "host", Address: strings.ReplaceAll(net, ",", " ")})
		a.Address = name
	case im.isOPNInterfaceNet(net):
		a.Network = net
	default:
		a.Address = net
	}
	return a
}

// isOPNInterfaceNet reports whether s names an interface's net or address
// ("lan", "lanip", "opt1", "(self)").
func (im *importer) isOPNInterfaceNet(s string) bool {
	if s == "(self)" {
		return true
	}
	base := strings.TrimSuffix(s, "ip")
	for _, it := range im.pf.Interfaces.Items {
		if it.XMLName.Local == s || it.XMLName.Local == base {
			return true
		}
	}
	return false
}

// keaDHCP maps Kea subnets to the interfaces whose network they are.
func (im *importer) keaDHCP() {
	k := im.pf.OPN.Kea.DHCP4
	if k == nil || !on(k.General.Enabled) {
		return
	}
	for _, s := range k.Subnets {
		subnet, err := netip.ParsePrefix(strings.TrimSpace(s.Subnet))
		key := im.opnIfaceFor(subnet)
		if err != nil || key == "" {
			im.opnNotes = append(im.opnNotes, Entry{Section: "dhcp", Item: "Kea subnet " + s.Subnet, Status: Skipped,
				Detail: "no interface has this network"})
			continue
		}
		from, to, ok := firstPool(s.Pools)
		if !ok {
			im.opnNotes = append(im.opnNotes, Entry{Section: "dhcp", Item: "Kea subnet " + s.Subnet, Status: Skipped, Detail: "no address pool"})
			continue
		}
		d := pfDHCP{XMLName: xml.Name{Local: key}, Enable: set(true)}
		d.Range.From, d.Range.To = from, to
		d.DefaultLeaseTime = s.ValidLifetime
		if d.DefaultLeaseTime == "" {
			d.DefaultLeaseTime = k.General.ValidLifetime
		}
		for _, r := range k.Reservations {
			if r.Subnet != s.UUID {
				continue
			}
			d.StaticMaps = append(d.StaticMaps, struct {
				MAC      string `xml:"mac"`
				IPAddr   string `xml:"ipaddr"`
				Hostname string `xml:"hostname"`
				Descr    string `xml:"descr"`
			}{MAC: r.HWAddress, IPAddr: r.IPAddress, Hostname: r.Hostname, Descr: r.Description})
		}
		im.pf.DHCPD.Items = append(im.pf.DHCPD.Items, d)
	}
}

// dnsmasqDHCP maps dnsmasq's DHCP ranges and static hosts.
func (im *importer) dnsmasqDHCP() {
	m := im.pf.DNSMasq
	if m == nil || m.Enable == nil {
		return
	}
	for _, r := range m.DHCPRanges {
		from, err := netip.ParseAddr(r.StartAddr)
		if err != nil || !from.Is4() {
			continue // DHCPv6 / RA ranges: IPv6 follows router advertisements
		}
		d := pfDHCP{XMLName: xml.Name{Local: r.Interface}, Enable: set(true), DefaultLeaseTime: r.LeaseTime}
		d.Range.From, d.Range.To = r.StartAddr, r.EndAddr
		prefix, _ := im.opnIfacePrefix(r.Interface)
		for _, h := range m.Hosts {
			ip, err := netip.ParseAddr(h.IP)
			if err != nil || h.HWAddr == "" || !prefix.Contains(ip) {
				continue
			}
			d.StaticMaps = append(d.StaticMaps, struct {
				MAC      string `xml:"mac"`
				IPAddr   string `xml:"ipaddr"`
				Hostname string `xml:"hostname"`
				Descr    string `xml:"descr"`
			}{MAC: h.HWAddr, IPAddr: h.IP, Hostname: h.Host, Descr: h.Descr})
		}
		im.pf.DHCPD.Items = append(im.pf.DHCPD.Items, d)
	}
	// dnsmasq answering DNS on another port is only the DHCP server here
	if m.Port != "" && m.Port != "53" {
		m.Enable = nil
	}
}

// opnIfacePrefix returns an interface's IPv4 network from its static address.
func (im *importer) opnIfacePrefix(key string) (netip.Prefix, bool) {
	for _, it := range im.pf.Interfaces.Items {
		if it.XMLName.Local != key {
			continue
		}
		a, err := netip.ParseAddr(it.IPAddr)
		bits, err2 := strconv.Atoi(it.Subnet)
		if err != nil || err2 != nil || !a.Is4() {
			return netip.Prefix{}, false
		}
		return netip.PrefixFrom(a, bits).Masked(), true
	}
	return netip.Prefix{}, false
}

func (im *importer) opnIfaceFor(subnet netip.Prefix) string {
	for _, it := range im.pf.Interfaces.Items {
		if p, ok := im.opnIfacePrefix(it.XMLName.Local); ok && p == subnet.Masked() {
			return it.XMLName.Local
		}
	}
	return ""
}

// firstPool reads Kea's pools ("a - b" or a CIDR, one per line).
func firstPool(pools string) (string, string, bool) {
	for _, line := range strings.Split(pools, "\n") {
		line = strings.TrimSpace(line)
		if lo, hi, ok := strings.Cut(line, "-"); ok {
			a, err1 := netip.ParseAddr(strings.TrimSpace(lo))
			b, err2 := netip.ParseAddr(strings.TrimSpace(hi))
			if err1 == nil && err2 == nil {
				return a.String(), b.String(), true
			}
		}
		if p, err := netip.ParsePrefix(line); err == nil && p.Addr().Is4() && p.Bits() < 31 {
			b := p.Masked().Addr().As4()
			n := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
			last := n + (uint32(1) << (32 - p.Bits())) - 2 // the broadcast address is not handed out
			ip := func(v uint32) string {
				return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}).String()
			}
			return ip(n + 1), ip(last), true
		}
	}
	return "", "", false
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(s)
}

func opnProto(p string) string {
	p = strings.ToLower(strings.TrimSpace(p))
	if p == "any" {
		return ""
	}
	return p
}

func ruleLabel(descr, iface string) string {
	if descr != "" {
		return descr
	}
	return "rule on " + iface
}

func slicesSortedBySeq[T any](in []T, seq func(T) string) []T {
	out := append([]T(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		a, _ := strconv.Atoi(seq(out[i]))
		b, _ := strconv.Atoi(seq(out[j]))
		return a < b
	})
	return out
}
