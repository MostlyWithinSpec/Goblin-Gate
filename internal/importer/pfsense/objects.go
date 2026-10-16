package pfsense

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// aliasObjects converts host and network aliases to address objects (a
// group when an alias has several entries), URL aliases to threat feeds.
// Port aliases become services when a rule uses them (the protocol comes
// from the rule).
func (im *importer) aliasObjects() {
	for _, a := range im.pf.Aliases {
		im.aliases[a.Name] = a
		im.names[a.Name] = true // reserve: rules refer to aliases by name
	}
	for _, a := range im.pf.Aliases {
		switch a.Type {
		case "host", "network":
			im.addrAlias(a)
		case "port":
			im.add("aliases", a.Name, Converted, "port alias: becomes services where rules use it")
		case "url", "urltable":
			url := a.URL
			if url == "" && len(a.AliasURL) > 0 {
				url = a.AliasURL[0]
			}
			if url == "" {
				im.add("aliases", a.Name, Skipped, "URL alias without a URL")
				continue
			}
			name := unique(im.names, slug(a.Name, "feed")+"-feed")
			im.cfg.Feeds = append(im.cfg.Feeds, config.Feed{Name: name, Type: "ip", URL: url, RefreshMinutes: 1440, Enabled: true})
			im.feeds[a.Name] = name
			im.add("aliases", a.Name, Approximated, "URL alias → IP threat feed %q (refreshed daily); rules using it are converted to feed blocking where possible", name)
			if len(a.AliasURL) > 1 {
				im.add("aliases", a.Name, Skipped, "only the first of %d URLs was imported", len(a.AliasURL))
			}
		default:
			im.add("aliases", a.Name, Skipped, "alias type %q is not supported", a.Type)
		}
	}
}

// addrAlias converts one host/network alias. Entries naming other aliases
// become group members.
func (im *importer) addrAlias(a pfAlias) {
	entries := strings.Fields(a.Address)
	if len(entries) == 0 {
		im.add("aliases", a.Name, Skipped, "empty alias")
		return
	}
	var members []string
	for i, e := range entries {
		if _, isAlias := im.aliases[e]; isAlias {
			members = append(members, e)
			continue
		}
		obj, ok := addressValue(e)
		if !ok {
			im.add("aliases", a.Name, Skipped, "entry %q not understood", e)
			continue
		}
		if len(entries) == 1 {
			obj.Name = a.Name
			im.cfg.Addresses = append(im.cfg.Addresses, obj)
			im.add("aliases", a.Name, Converted, "%s %s", obj.Type, obj.Value)
			return
		}
		obj.Name = unique(im.names, fmt.Sprintf("%s-%d", a.Name, i+1))
		im.cfg.Addresses = append(im.cfg.Addresses, obj)
		members = append(members, obj.Name)
	}
	if len(members) == 0 {
		return
	}
	im.cfg.AddressGroups = append(im.cfg.AddressGroups, config.AddressGroup{Name: a.Name, Members: members})
	im.add("aliases", a.Name, Converted, "group of %d", len(members))
}

// addressValue turns an alias entry or rule address into an address object
// (without a name): host, subnet, range "a-b", or a hostname (fqdn).
func addressValue(v string) (config.Address, bool) {
	if p, err := netip.ParsePrefix(v); err == nil {
		if p.IsSingleIP() {
			return config.Address{Type: "host", Value: p.Addr().String()}, true
		}
		return config.Address{Type: "subnet", Value: p.Masked().String()}, true
	}
	if a, err := netip.ParseAddr(v); err == nil {
		return config.Address{Type: "host", Value: a.String()}, true
	}
	if lo, hi, ok := strings.Cut(v, "-"); ok {
		if a, err1 := netip.ParseAddr(lo); err1 == nil {
			if b, err2 := netip.ParseAddr(hi); err2 == nil && a.Is4() == b.Is4() {
				return config.Address{Type: "range", Value: a.String() + "-" + b.String()}, true
			}
		}
	}
	if validHostname(v) {
		return config.Address{Type: "fqdn", Value: strings.ToLower(v)}, true
	}
	return config.Address{}, false
}

func validHostname(s string) bool {
	if len(s) == 0 || len(s) > 253 || !strings.Contains(s, ".") {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

// addrRef is a rule side resolved to address object names.
type addrRef struct {
	refs  []string // "all" or object names
	self  bool     // the firewall itself (input, not forwarding)
	feed  string   // a URL alias: threat feed name
	why   string   // set when the side cannot be converted
	iface string   // interface-net reference: the interface name (for nat decisions)
}

// ruleAddr resolves a pfSense rule side.
func (im *importer) ruleAddr(a pfAddr) addrRef {
	switch {
	case a.Not != nil:
		return addrRef{why: "negated addresses (\"not\") are not supported"}
	case a.Any != nil || (a.Network == "" && a.Address == ""):
		return addrRef{refs: []string{"all"}}
	case a.Network != "":
		n := a.Network
		switch {
		case n == "(self)" || n == "self":
			return addrRef{self: true}
		case strings.HasSuffix(n, "ip"): // lanip, wanip, opt1ip: the box's own address
			return addrRef{self: true}
		}
		name, ok := im.ifName(n)
		if !ok {
			return addrRef{why: fmt.Sprintf("network %q (interface group, VPN or virtual IP) is not supported", n)}
		}
		p, ok := im.ifPrefix(name)
		if !ok {
			return addrRef{why: fmt.Sprintf("interface %q has no static IPv4 subnet to use as \"%s net\"", name, n)}
		}
		obj := name + "-net"
		if !im.names[obj] {
			im.names[obj] = true
			im.cfg.Addresses = append(im.cfg.Addresses, config.Address{Name: obj, Type: "subnet", Value: p.Masked().String()})
		}
		return addrRef{refs: []string{obj}, iface: name}
	}
	v := a.Address
	if al, ok := im.aliases[v]; ok {
		switch al.Type {
		case "host", "network":
			return addrRef{refs: []string{v}}
		case "url", "urltable":
			if f, ok := im.feeds[v]; ok {
				return addrRef{feed: f}
			}
		}
		return addrRef{why: fmt.Sprintf("alias %q (%s) cannot be used as an address", v, al.Type)}
	}
	obj, ok := addressValue(v)
	if !ok {
		return addrRef{why: fmt.Sprintf("address %q not understood", v)}
	}
	name := strings.NewReplacer("/", "_", ":", "_").Replace(obj.Value)
	if !im.names[name] {
		im.names[name] = true
		obj.Name = name
		im.cfg.Addresses = append(im.cfg.Addresses, obj)
	}
	return addrRef{refs: []string{name}}
}

// services returns service object names for a protocol and destination
// port ("" = any port; a number, "a-b", or a port alias name). ok is false
// when the protocol is not supported.
func (im *importer) services(proto, port string) ([]string, string) {
	var protos []string
	switch proto {
	case "", "any":
		if port != "" {
			protos = []string{"tcp", "udp"}
		} else {
			return []string{"all"}, ""
		}
	case "tcp", "udp":
		protos = []string{proto}
	case "tcp/udp":
		protos = []string{"tcp", "udp"}
	case "icmp", "ipv6-icmp", "icmp6":
		if !im.names["icmp"] {
			im.names["icmp"] = true
			im.cfg.Services = append(im.cfg.Services, config.Service{Name: "icmp", Protocol: "icmp"})
		}
		return []string{"icmp"}, ""
	default:
		return nil, fmt.Sprintf("protocol %q is not supported yet", proto)
	}
	ports := port
	label := port
	if al, ok := im.aliases[port]; ok {
		if al.Type != "port" {
			return nil, fmt.Sprintf("alias %q is not a port alias", port)
		}
		var list []string
		for _, p := range strings.Fields(al.Address) {
			list = append(list, strings.ReplaceAll(p, ":", "-"))
		}
		ports, label = strings.Join(list, ","), slug(port, "ports")
	} else {
		ports = strings.ReplaceAll(ports, ":", "-")
		label = ports
	}
	allPorts := ports == ""
	if allPorts {
		ports = "1-65535" // a tcp/udp service always names its ports
	}
	var out []string
	for _, p := range protos {
		name := p + "-all"
		if !allPorts {
			name = p + "-" + strings.NewReplacer(",", "_").Replace(label)
		}
		if !im.names[name] {
			im.names[name] = true
			im.cfg.Services = append(im.cfg.Services, config.Service{Name: name, Protocol: p, Ports: ports})
		}
		out = append(out, name)
	}
	return out, ""
}
