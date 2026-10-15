package nft

import (
	"fmt"
	"sort"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// setDecl is one named set in table inet goblin.
type setDecl struct {
	name     string
	typ      string // "ipv4_addr", "ipv6_addr" or "inet_proto . inet_service"
	elements []string
	autoMrg  bool
	comment  string
}

// addrObj is a resolved address object or group. IPv4 and IPv6 members go
// to separate sets (task 045): set (addr_<name>) and set6 (addr6_<name>).
type addrObj struct {
	set    string // IPv4 set; "" when the object has only IPv6 members
	set6   string // IPv6 set; "" when the object has no IPv6 (or fqdn) members
	spans  []span
	spans6 []span6
	fqdns  []string // fqdn members; resolved at runtime by a later task
}

// has4 and has6 report whether the object can match that family; fqdn
// members may resolve to either.
func (o *addrObj) has4() bool { return len(o.spans) > 0 || len(o.fqdns) > 0 }
func (o *addrObj) has6() bool { return len(o.spans6) > 0 || len(o.fqdns) > 0 }

// declare names and declares the object's sets: the IPv4 set unless the
// object is IPv6-only (an empty object keeps an empty IPv4 set, as before
// IPv6), the IPv6 set when it has IPv6 or fqdn members.
func (o *objects) declare(obj *addrObj, prefix, name, comment string) {
	if obj.has4() || len(obj.spans6) == 0 {
		obj.set = o.names.get(prefix, name)
		o.sets = append(o.sets, addrSetDecl(obj.set, obj.spans, comment))
	}
	if obj.has6() {
		obj.set6 = o.names.get(strings.Replace(prefix, "addr", "addr6", 1), name)
		o.sets = append(o.sets, addrSetDecl6(obj.set6, obj.spans6, comment))
	}
}

// svcObj is a resolved service object or group.
type svcObj struct {
	set       string            // nft set name; "" when the object has no port pairs
	any       bool              // matches every protocol
	protoOnly map[string]bool   // protocols matched on all ports (tcp, udp, icmp)
	pairs     map[string][]span // per-protocol port spans
}

// objects holds every resolved firewall object plus the sets to declare.
type objects struct {
	addrs map[string]*addrObj
	svcs  map[string]*svcObj
	sets  []setDecl
	names *namer
}

// isAll reports whether a reference means "match everything".
func isAll(ref string) bool {
	switch strings.ToLower(ref) {
	case "all", "any":
		return true
	}
	return false
}

// namer hands out nft-safe, collision-free identifiers. Object names may
// contain '.' and '-', which are rewritten to '_'; clashes get a _2, _3 suffix.
type namer struct{ used map[string]bool }

func newNamer() *namer { return &namer{used: map[string]bool{}} }

func (n *namer) get(prefix, name string) string {
	var b strings.Builder
	b.WriteString(prefix)
	for _, r := range name {
		if r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	base := b.String()
	id := base
	for i := 2; n.used[id]; i++ {
		id = fmt.Sprintf("%s_%d", base, i)
	}
	n.used[id] = true
	return id
}

// resolveObjects resolves addresses, groups and services into sets.
func resolveObjects(cfg *config.Config, names *namer) (*objects, error) {
	o := &objects{addrs: map[string]*addrObj{}, svcs: map[string]*svcObj{}, names: names}
	if err := o.resolveAddrs(cfg); err != nil {
		return nil, err
	}
	if err := o.resolveSvcs(cfg); err != nil {
		return nil, err
	}
	return o, nil
}

func (o *objects) resolveAddrs(cfg *config.Config) error {
	addrs := append([]config.Address(nil), cfg.Addresses...)
	sort.Slice(addrs, func(i, j int) bool { return addrs[i].Name < addrs[j].Name })
	for _, a := range addrs {
		obj := &addrObj{}
		comment := a.Type + " " + a.Value
		if a.Type == "fqdn" {
			obj.fqdns = []string{a.Value}
			comment = "fqdn " + a.Value + " (resolved at runtime)"
		} else {
			v4, v6, err := addrValue(a.Type, a.Value)
			if err != nil {
				return fmt.Errorf("address %q: %w", a.Name, err)
			}
			obj.spans, obj.spans6 = v4, v6
		}
		o.addrs[a.Name] = obj
		o.declare(obj, "addr_", a.Name, comment)
	}

	groups := map[string]config.AddressGroup{}
	var gnames []string
	for _, g := range cfg.AddressGroups {
		if _, dup := o.addrs[g.Name]; dup {
			return fmt.Errorf("address group %q: name also used by an address", g.Name)
		}
		groups[g.Name] = g
		gnames = append(gnames, g.Name)
	}
	sort.Strings(gnames)
	var resolve func(name string, stack map[string]bool) (*addrObj, error)
	resolve = func(name string, stack map[string]bool) (*addrObj, error) {
		if obj, ok := o.addrs[name]; ok {
			return obj, nil
		}
		g, ok := groups[name]
		if !ok {
			return nil, fmt.Errorf("unknown address %q", name)
		}
		if stack[name] {
			return nil, fmt.Errorf("address group %q includes itself", name)
		}
		stack[name] = true
		defer delete(stack, name)
		obj := &addrObj{}
		for _, m := range g.Members {
			mo, err := resolve(m, stack)
			if err != nil {
				return nil, fmt.Errorf("address group %q: %w", name, err)
			}
			obj.spans = append(obj.spans, mo.spans...)
			obj.spans6 = append(obj.spans6, mo.spans6...)
			obj.fqdns = append(obj.fqdns, mo.fqdns...)
		}
		obj.spans = mergeSpans(obj.spans)
		obj.spans6 = mergeSpans6(obj.spans6)
		o.addrs[name] = obj
		return obj, nil
	}
	for _, name := range gnames {
		obj, err := resolve(name, map[string]bool{})
		if err != nil {
			return err
		}
		comment := "group"
		if len(obj.fqdns) > 0 {
			comment += "; fqdn members resolved at runtime: " + strings.Join(obj.fqdns, " ")
		}
		o.declare(obj, "addrgrp_", name, comment)
	}
	return nil
}

func (o *objects) resolveSvcs(cfg *config.Config) error {
	svcs := append([]config.Service(nil), cfg.Services...)
	sort.Slice(svcs, func(i, j int) bool { return svcs[i].Name < svcs[j].Name })
	for _, s := range svcs {
		obj := &svcObj{protoOnly: map[string]bool{}, pairs: map[string][]span{}}
		switch p := strings.ToLower(s.Protocol); p {
		case "any":
			obj.any = true
		case "icmp":
			obj.protoOnly["icmp"] = true
		case "tcp", "udp":
			ports, err := parsePorts(s.Ports)
			if err != nil {
				return fmt.Errorf("service %q: %w", s.Name, err)
			}
			if len(ports) == 0 {
				obj.protoOnly[p] = true
			} else {
				obj.pairs[p] = ports
			}
		default:
			return fmt.Errorf("service %q: unsupported protocol %q", s.Name, s.Protocol)
		}
		o.svcs[s.Name] = obj
	}

	groups := map[string]config.ServiceGroup{}
	for _, g := range cfg.ServiceGroups {
		if _, dup := o.svcs[g.Name]; dup {
			return fmt.Errorf("service group %q: name also used by a service", g.Name)
		}
		groups[g.Name] = g
	}
	var resolve func(name string, stack map[string]bool) (*svcObj, error)
	resolve = func(name string, stack map[string]bool) (*svcObj, error) {
		if obj, ok := o.svcs[name]; ok {
			return obj, nil
		}
		g, ok := groups[name]
		if !ok {
			return nil, fmt.Errorf("unknown service %q", name)
		}
		if stack[name] {
			return nil, fmt.Errorf("service group %q includes itself", name)
		}
		stack[name] = true
		defer delete(stack, name)
		var members []*svcObj
		for _, m := range g.Members {
			mo, err := resolve(m, stack)
			if err != nil {
				return nil, fmt.Errorf("service group %q: %w", name, err)
			}
			members = append(members, mo)
		}
		obj := unionSvcs(members)
		o.svcs[name] = obj
		return obj, nil
	}
	for _, g := range cfg.ServiceGroups {
		if _, err := resolve(g.Name, map[string]bool{}); err != nil {
			return err
		}
	}

	// Declare a set for every service or group that carries port pairs.
	var all []string
	for name := range o.svcs {
		all = append(all, name)
	}
	sort.Strings(all)
	for _, name := range all {
		obj := o.svcs[name]
		if obj.any || len(obj.pairs) == 0 {
			continue
		}
		prefix := "svc_"
		if _, isGroup := groups[name]; isGroup {
			prefix = "svcgrp_"
		}
		obj.set = o.names.get(prefix, name)
		o.sets = append(o.sets, svcSetDecl(obj.set, obj.pairs, ""))
	}
	return nil
}

// unionSvcs merges service objects; protocols matched on all ports absorb
// that protocol's port pairs.
func unionSvcs(objs []*svcObj) *svcObj {
	u := &svcObj{protoOnly: map[string]bool{}, pairs: map[string][]span{}}
	for _, o := range objs {
		if o.any {
			u.any = true
		}
		for p := range o.protoOnly {
			u.protoOnly[p] = true
		}
		for p, s := range o.pairs {
			u.pairs[p] = append(u.pairs[p], s...)
		}
	}
	for p, s := range u.pairs {
		if u.protoOnly[p] {
			delete(u.pairs, p)
			continue
		}
		u.pairs[p] = mergeSpans(s)
	}
	return u
}

func addrSetDecl(name string, spans []span, comment string) setDecl {
	d := setDecl{name: name, typ: "ipv4_addr", comment: comment}
	for _, s := range mergeSpans(spans) {
		d.elements = append(d.elements, formatAddrSpan(s))
	}
	return d
}

func addrSetDecl6(name string, spans []span6, comment string) setDecl {
	d := setDecl{name: name, typ: "ipv6_addr", comment: comment}
	for _, s := range mergeSpans6(spans) {
		d.elements = append(d.elements, formatAddrSpan6(s))
	}
	return d
}

func svcSetDecl(name string, pairs map[string][]span, comment string) setDecl {
	d := setDecl{name: name, typ: "inet_proto . inet_service", comment: comment}
	for _, p := range sortedKeys(pairs) {
		for _, s := range pairs[p] {
			d.elements = append(d.elements, p+" . "+formatPortSpan(s))
		}
	}
	return d
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
