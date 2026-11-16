package nft

import (
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// liveConfig: a live object "containers" kept by the homelab plugin, in a
// group with a country and a fixed host, and next to another address in one
// policy (a union set).
func liveConfig() *config.Config {
	cfg := geoConfig()
	cfg.Plugins = append(cfg.Plugins, config.Plugin{Name: "homelab", Enabled: true, Capabilities: []string{"objects:write"}})
	cfg.Addresses = append(cfg.Addresses, config.Address{Name: "containers", Type: "live", Value: "homelab"})
	cfg.AddressGroups = append(cfg.AddressGroups, config.AddressGroup{Name: "lab", Members: []string{"containers", "nz", "office"}})
	cfg.Policies = append([]config.Policy{
		{ID: 20, Name: "lab-out", Enabled: true, SrcIntf: []string{"lan"}, DstIntf: []string{"wan1"}, SrcAddr: []string{"containers", "office"}, DstAddr: []string{"all"}, Service: []string{"any"}, Action: "deny"},
		{ID: 21, Name: "lab-group", Enabled: true, SrcIntf: []string{"lan"}, DstIntf: []string{"wan1"}, SrcAddr: []string{"lab"}, DstAddr: []string{"all"}, Service: []string{"any"}, Action: "deny"},
	}, cfg.Policies...)
	return cfg
}

func TestLiveRuntimeSets(t *testing.T) {
	cfg := liveConfig()
	if errs := config.Validate(cfg); len(errs) > 0 {
		t.Fatalf("invalid: %v", errs)
	}
	sets, err := RuntimeSets(cfg)
	if err != nil {
		t.Fatal(err)
	}
	live := map[string][]string{}
	countries := map[string][]string{}
	for _, s := range sets {
		if len(s.Live) > 0 {
			live[s.Name] = s.Live
		}
		countries[s.Name] = s.Countries
	}
	for _, name := range []string{"addr_containers", "addr6_containers", "addrgrp_lab"} {
		if !slices.Equal(live[name], []string{"containers"}) {
			t.Errorf("%s: live %v, want [containers] (all: %v)", name, live[name], live)
		}
	}
	if !slices.Equal(countries["addrgrp_lab"], []string{"NZ"}) {
		t.Errorf("the group keeps its country too: %v", countries["addrgrp_lab"])
	}
	union := false
	for name, l := range live {
		if !strings.HasPrefix(name, "addr") && slices.Contains(l, "containers") {
			union = true
		}
	}
	if !union {
		t.Errorf("no policy union set with the live object: %v", live)
	}
	// CountrySets leaves out sets with only live members
	cs, _ := CountrySets(cfg)
	for _, s := range cs {
		if s.Name == "addr_containers" {
			t.Error("CountrySets lists a live-only set")
		}
	}
	out, err := build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `comment "live, kept up to date by plugin homelab"`) {
		t.Errorf("ruleset lacks the live set comment")
	}
}

func TestRuntimeBatchMixesSources(t *testing.T) {
	cfg := liveConfig()
	sets, _ := RuntimeSets(cfg)
	var lab []RuntimeSet
	for _, s := range sets {
		if s.Name == "addrgrp_lab" || s.Name == "addr6grp_lab" || s.Name == "addr_containers" || s.Name == "addr6_containers" {
			lab = append(lab, s)
		}
	}
	r := func(lo, hi string) Range { return Range{netip.MustParseAddr(lo), netip.MustParseAddr(hi)} }
	ranges := func(c string) []Range {
		if c == "NZ" {
			return []Range{r("203.0.113.0", "203.0.113.255")}
		}
		return nil
	}
	members := func(o string) []netip.Prefix {
		if o == "containers" {
			return []netip.Prefix{netip.MustParsePrefix("10.20.0.5/32"), netip.MustParsePrefix("10.20.1.0/24"), netip.MustParsePrefix("fd00:20::/64")}
		}
		return nil
	}
	got := string(RuntimeBatch(lab, ranges, members))
	for _, want := range []string{
		"flush set inet goblin addrgrp_lab\n",
		// static host, country range and live members in one set
		"add element inet goblin addrgrp_lab { 10.20.0.5, 10.20.1.0/24, 198.51.100.7, 203.0.113.0/24 }",
		"add element inet goblin addr_containers { 10.20.0.5, 10.20.1.0/24 }",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("batch lacks %q:\n%s", want, got)
		}
	}
	// the IPv6 member lands in the IPv6 sets only
	for _, want := range []string{"add element inet goblin addr6_containers { fd00:20::/64 }", "add element inet goblin addr6grp_lab { fd00:20::/64 }"} {
		if !strings.Contains(got, want) {
			t.Errorf("batch lacks %q:\n%s", want, got)
		}
	}
	// nothing known yet: the sets keep only their static part
	empty := string(RuntimeBatch(lab, nil, nil))
	if !strings.Contains(empty, "add element inet goblin addrgrp_lab { 198.51.100.7 }") || strings.Contains(empty, "addr_containers {") {
		t.Errorf("without sources:\n%s", empty)
	}
}

func TestPrefixRange(t *testing.T) {
	for _, c := range []struct{ p, lo, hi string }{
		{"10.0.0.0/8", "10.0.0.0", "10.255.255.255"},
		{"10.1.2.3/32", "10.1.2.3", "10.1.2.3"},
		{"10.1.2.3/24", "10.1.2.0", "10.1.2.255"},
		{"fd00::/64", "fd00::", "fd00::ffff:ffff:ffff:ffff"},
		{"::ffff:10.0.0.0/120", "10.0.0.0", "10.0.0.255"},
	} {
		lo, hi := prefixRange(netip.MustParsePrefix(c.p))
		if lo.String() != c.lo || hi.String() != c.hi {
			t.Errorf("%s: %s-%s, want %s-%s", c.p, lo, hi, c.lo, c.hi)
		}
	}
}

func TestSimulateLive(t *testing.T) {
	cfg := liveConfig()
	member := netip.MustParseAddr("10.20.0.5")
	rt := SimRuntime{Live: func(o string, a netip.Addr) bool { return o == "containers" && a == member }}
	flow := SimFlow{SrcIP: member, DstIP: netip.MustParseAddr("8.8.8.8"), Protocol: "tcp", DstPort: 443, SrcIntf: "lan"}
	res, err := Simulate(cfg, flow, rt)
	if err != nil {
		t.Fatal(err)
	}
	if res.Policy == nil || res.Policy.ID != 20 {
		t.Fatalf("a live member matches lab-out: %+v", res)
	}
	flow.SrcIP = netip.MustParseAddr("192.168.1.50")
	res, err = Simulate(cfg, flow, rt)
	if err != nil {
		t.Fatal(err)
	}
	if res.Policy != nil && (res.Policy.ID == 20 || res.Policy.ID == 21) {
		t.Fatalf("a non-member matched a live policy: %+v", res.Policy)
	}
}
