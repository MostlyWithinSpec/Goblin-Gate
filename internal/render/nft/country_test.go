package nft

import (
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// geoConfig: inbound SSH only from the US and CA (a country object), and a
// group mixing a country with a fixed host, used next to another address
// (a per-policy union set).
func geoConfig() *config.Config {
	cfg := simConfig()
	cfg.Addresses = append(cfg.Addresses,
		config.Address{Name: "north-america", Type: "country", Value: "us, CA"},
		config.Address{Name: "nz", Type: "country", Value: "NZ"},
		config.Address{Name: "office", Type: "host", Value: "198.51.100.7"},
	)
	cfg.AddressGroups = []config.AddressGroup{{Name: "trusted", Members: []string{"office", "nz"}}}
	cfg.NAT.PortForwards = append(cfg.NAT.PortForwards, config.PortForward{Name: "ssh", InIntf: "wan1", ExtPort: "2222", Protocol: "tcp", ToAddr: "192.168.1.23", ToPort: "22", Source: []string{"north-america"}})
	cfg.Policies = append([]config.Policy{
		{ID: 10, Name: "no-nz-out", Enabled: true, SrcIntf: []string{"lan"}, DstIntf: []string{"wan1"}, SrcAddr: []string{"all"}, DstAddr: []string{"trusted", "north-america"}, Service: []string{"any"}, Action: "deny"},
	}, cfg.Policies...)
	return cfg
}

func TestCountrySets(t *testing.T) {
	cfg := geoConfig()
	if errs := config.Validate(cfg); len(errs) > 0 {
		t.Fatalf("invalid: %v", errs)
	}
	sets, err := CountrySets(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, s := range sets {
		got[s.Name] = s.Countries
	}
	// object sets (both families), the group's, and the policy union's
	for name, want := range map[string][]string{
		"addr_north_america":  {"US", "CA"},
		"addr6_north_america": {"US", "CA"},
		"addr_nz":             {"NZ"},
		"addrgrp_trusted":     {"NZ"},
	} {
		if !slices.Equal(got[name], want) {
			t.Errorf("%s: countries %v, want %v (all: %v)", name, got[name], want, got)
		}
	}
	union := false
	for name, c := range got {
		if !strings.HasPrefix(name, "addr") && slices.Contains(c, "NZ") && slices.Contains(c, "US") {
			union = true
		}
	}
	if !union {
		t.Errorf("no union set with NZ and US: %v", got)
	}
	if u := UsedCountries(sets); !slices.Equal(u, []string{"CA", "NZ", "US"}) {
		t.Errorf("used: %v", u)
	}
	out, err := build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `comment "country US CA (filled at runtime)"`) {
		t.Errorf("ruleset lacks the country set comment:\n%s", out)
	}
}

func TestCountryBatch(t *testing.T) {
	cfg := geoConfig()
	sets, _ := CountrySets(cfg)
	ranges := func(c string) []Range {
		r := func(lo, hi string) Range { return Range{netip.MustParseAddr(lo), netip.MustParseAddr(hi)} }
		switch c {
		case "NZ":
			// adjacent ranges merge; the office host stays a separate element
			return []Range{r("203.0.113.0", "203.0.113.127"), r("203.0.113.128", "203.0.113.255"), r("2001:db8:a::", "2001:db8:a::ffff")}
		case "US":
			return []Range{r("192.0.2.0", "192.0.2.255")}
		}
		return nil
	}
	b := string(CountryBatch(sets, ranges))
	for _, want := range []string{
		"flush set inet goblin addrgrp_trusted\nadd element inet goblin addrgrp_trusted { 198.51.100.7, 203.0.113.0/24 }\n",
		"flush set inet goblin addr_north_america\nadd element inet goblin addr_north_america { 192.0.2.0/24 }\n",
		"flush set inet goblin addr6_nz\nadd element inet goblin addr6_nz { 2001:db8:a::/112 }\n",
		// CA has no ranges and IPv6 has none for US: the set is only flushed
		"flush set inet goblin addr6_north_america\n",
	} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %q in:\n%s", want, b)
		}
	}
	if strings.Contains(b, "addr6_north_america {") {
		t.Errorf("IPv6 set got IPv4 elements:\n%s", b)
	}
}

func TestSimulateCountry(t *testing.T) {
	cfg := geoConfig()
	country := func(a netip.Addr) string {
		if netip.MustParsePrefix("203.0.113.0/24").Contains(a) {
			return "NZ"
		}
		return "DE"
	}
	if res := runSim(t, cfg, SimRuntime{Country: country}, "192.168.1.10", "203.0.113.9", "tcp", 443, noon); res.Verdict != "deny" || res.Policy == nil || res.Policy.ID != 10 {
		t.Fatalf("to NZ: %+v", res)
	}
	if res := runSim(t, cfg, SimRuntime{Country: country}, "192.168.1.10", "93.184.216.34", "tcp", 443, noon); res.Verdict != "accept" {
		t.Fatalf("to DE: %+v", res)
	}
	res := runSim(t, cfg, SimRuntime{}, "192.168.1.10", "203.0.113.9", "tcp", 443, noon)
	if res.Verdict != "accept" || !slices.ContainsFunc(res.Notes, func(n string) bool { return strings.Contains(n, "GeoIP") }) {
		t.Fatalf("without the database: %+v", res)
	}
}

// A port forward limited to a country only lets that country through; the
// rest is translated but falls to the forward chain's drop.
func TestPortForwardSource(t *testing.T) {
	cfg := geoConfig()
	out, err := build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `iifname "eth0" ip saddr @addr_north_america ip daddr 192.168.1.23 tcp dport 22 ct status dnat`) {
		t.Fatalf("forward accept lacks the source match:\n%s", out)
	}
	country := func(a netip.Addr) string {
		if netip.MustParsePrefix("192.0.2.0/24").Contains(a) {
			return "US"
		}
		return "DE"
	}
	if res := runSim(t, cfg, SimRuntime{Country: country}, "192.0.2.5", "198.51.100.2", "tcp", 2222, noon); res.Verdict != "accept" {
		t.Fatalf("from US: %+v", res)
	}
	if res := runSim(t, cfg, SimRuntime{Country: country}, "93.184.216.34", "198.51.100.2", "tcp", 2222, noon); res.Verdict == "accept" {
		t.Fatalf("from DE: %+v", res)
	}

	// an IPv6-only source can never match the IPv4 forward
	cfg.Addresses = append(cfg.Addresses, config.Address{Name: "v6", Type: "host", Value: "2001:db8::1"})
	cfg.NAT.PortForwards[len(cfg.NAT.PortForwards)-1].Source = []string{"v6"}
	if out, err = build(cfg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `# port forward "ssh": its source has no IPv4 addresses`) || strings.Contains(string(out), "dport 2222 dnat") {
		t.Fatalf("v6-only source:\n%s", out)
	}

	cfg.NAT.PortForwards[len(cfg.NAT.PortForwards)-1].Source = []string{"gone"}
	if errs := config.Validate(cfg); !slices.ContainsFunc(errs, func(e config.ValidationError) bool {
		return strings.Contains(e.Path, "source[0]") && strings.Contains(e.Message, `"gone"`)
	}) {
		t.Fatalf("unknown source accepted: %v", errs)
	}
}

func TestValidateCountry(t *testing.T) {
	cfg := config.Default()
	cfg.Addresses = []config.Address{{Name: "x", Type: "country", Value: "US,XX"}, {Name: "y", Type: "country", Value: " , "}}
	errs := config.Validate(cfg)
	s := ""
	for _, e := range errs {
		s += e.Error() + "\n"
	}
	if !strings.Contains(s, `unknown country code "XX"`) || !strings.Contains(s, "pick at least one country") {
		t.Fatalf("errors:\n%s", s)
	}
}
