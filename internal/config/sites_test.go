package config

import (
	"net/netip"
	"strings"
	"testing"
)

const (
	keyA = "YAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk="
	keyB = "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg="
)

// SitesFixture: home (192.168.1.0/24) with a cabin it dials and an office
// reached through the cabin.
func SitesFixture() *Config {
	c := Default()
	c.VPN.Sites = Sites{Enabled: true, Address: netip.MustParseAddr("10.254.1.1"), Endpoint: "home.example.net", Networks: []string{"lan"},
		Remotes: []Site{
			{Name: "cabin", Enabled: true, PublicKey: keyA, Endpoint: "cabin.example.net:51830", ListenPort: 51830,
				Address: netip.MustParseAddr("10.254.2.2"), Networks: []netip.Prefix{netip.MustParsePrefix("192.168.50.0/24")}},
			{Name: "office", Enabled: true, Via: "cabin", Address: netip.MustParseAddr("10.254.3.3"),
				Networks: []netip.Prefix{netip.MustParsePrefix("192.168.60.0/24")}},
		}}
	c.Policies = append(c.Policies, Policy{ID: 2, Name: "lan-to-cabin", Enabled: true, SrcIntf: []string{"lan"}, DstIntf: []string{"cabin"},
		SrcAddr: []string{"all"}, DstAddr: []string{"all"}, Service: []string{"all"}, Action: "accept"})
	return c
}

func errorsAt(c *Config) map[string]string {
	out := map[string]string{}
	for _, e := range Validate(c) {
		out[e.Path] = e.Message
	}
	return out
}

func TestSitesValid(t *testing.T) {
	c := SitesFixture()
	if errs := Validate(c); len(errs) != 0 {
		t.Fatal(errs)
	}
	got := c.SiteRoutes("cabin")
	want := "[10.254.2.2/32 10.254.3.3/32 192.168.50.0/24 192.168.60.0/24]"
	if fmtPrefixes(got) != want {
		t.Fatalf("routes %v", got)
	}
	if n := len(c.DirectSites()); n != 1 {
		t.Fatalf("direct sites %d", n)
	}
	if z := c.RemoteSiteNetworks()[netip.MustParsePrefix("192.168.60.0/24")]; z != "cabin" {
		t.Fatalf("office zone %q", z)
	}
	if p := c.FreeSitePort(); p != 51831 {
		t.Fatalf("free port %d", p)
	}
	// sites off: no tunnels, no zones
	c.VPN.Sites.Enabled = false
	if len(c.DirectSites()) != 0 {
		t.Fatal("tunnels while off")
	}
	if e := errorsAt(c)["policies[1].dst_intf[0]"]; !strings.Contains(e, "unknown") {
		t.Fatalf("policy on a site zone while sites are off: %q", e)
	}
}

func fmtPrefixes(ps []netip.Prefix) string {
	var b strings.Builder
	b.WriteString("[")
	for i, p := range ps {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(p.String())
	}
	b.WriteString("]")
	return b.String()
}

func TestSitesInvalid(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(c *Config)
		path string
		want string
	}{
		"overlaps the LAN": {func(c *Config) {
			c.VPN.Sites.Remotes[0].Networks = []netip.Prefix{netip.MustParsePrefix("192.168.0.0/16")}
		},
			"vpn.sites.remotes[0].networks[0]", "overlaps interface lan"},
		"overlaps another site": {func(c *Config) {
			c.VPN.Sites.Remotes[1].Networks = []netip.Prefix{netip.MustParsePrefix("192.168.50.128/25")}
		},
			"vpn.sites.remotes[1].networks[0]", "overlaps site cabin"},
		"host bits": {func(c *Config) {
			c.VPN.Sites.Remotes[0].Networks = []netip.Prefix{netip.MustParsePrefix("192.168.50.1/24")}
		},
			"vpn.sites.remotes[0].networks[0]", "without host bits"},
		"default route": {func(c *Config) { c.VPN.Sites.Remotes[0].Networks = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")} },
			"vpn.sites.remotes[0].networks[0]", "whole internet"},
		"name taken": {func(c *Config) { c.VPN.Sites.Remotes[0].Name = "lan" }, "vpn.sites.remotes[0].name", "already used"},
		"bad name":   {func(c *Config) { c.VPN.Sites.Remotes[0].Name = "Cabin" }, "vpn.sites.remotes[0].name", "invalid site name"},
		"own address": {func(c *Config) { c.VPN.Sites.Remotes[0].Address = netip.MustParseAddr("10.254.1.1") },
			"vpn.sites.remotes[0].address", "own tunnel address"},
		"address in LAN": {func(c *Config) { c.VPN.Sites.Address = netip.MustParseAddr("192.168.1.9") }, "vpn.sites.address", "inside interface lan"},
		"no endpoint anywhere": {func(c *Config) { c.VPN.Sites.Endpoint, c.VPN.Sites.Remotes[0].Endpoint = "", "" },
			"vpn.sites.remotes[0].endpoint", "neither gate"},
		"bad key": {func(c *Config) { c.VPN.Sites.Remotes[0].PublicKey = "nope" }, "vpn.sites.remotes[0].public_key", "public key"},
		"port clash": {func(c *Config) {
			c.VPN.WireGuard = []WireGuard{{Name: "wg0", Address: netip.MustParsePrefix("10.66.0.1/24"), ListenPort: 51830}}
		}, "vpn.sites.remotes[0].listen_port", "used twice"},
		"via unknown":  {func(c *Config) { c.VPN.Sites.Remotes[1].Via = "nowhere" }, "vpn.sites.remotes[1].via", "unknown site"},
		"via with key": {func(c *Config) { c.VPN.Sites.Remotes[1].PublicKey = keyB }, "vpn.sites.remotes[1].via", "no key"},
		"via chain": {func(c *Config) {
			c.VPN.Sites.Remotes[0].Via = "office"
			c.VPN.Sites.Remotes[0].PublicKey, c.VPN.Sites.Remotes[0].Endpoint, c.VPN.Sites.Remotes[0].ListenPort = "", "", 0
		}, "vpn.sites.remotes[1].via", "itself reached through"},
		"bad networks":  {func(c *Config) { c.VPN.Sites.Networks = []string{"nothing"} }, "vpn.sites.networks[0]", "neither an address object"},
		"bad endpoint":  {func(c *Config) { c.VPN.Sites.Remotes[0].Endpoint = "cabin.example.net" }, "vpn.sites.remotes[0].endpoint", "host:port"},
		"dup addresses": {func(c *Config) { c.VPN.Sites.Remotes[1].Address = netip.MustParseAddr("10.254.2.2") }, "vpn.sites.remotes[1].address", "used by two sites"},
	} {
		c := SitesFixture()
		tc.edit(c)
		if e := errorsAt(c)[tc.path]; !strings.Contains(e, tc.want) {
			t.Errorf("%s: %s = %q, want %q (all: %v)", name, tc.path, e, tc.want, errorsAt(c))
		}
	}
}

// Sites can be turned on before the first pairing picks the tunnel address.
func TestSitesOnBeforePairing(t *testing.T) {
	c := Default()
	c.VPN.Sites = Sites{Enabled: true, Endpoint: "home.example.net", Networks: []string{"lan"}}
	if errs := Validate(c); len(errs) != 0 {
		t.Fatal(errs)
	}
	c.VPN.Sites.Remotes = SitesFixture().VPN.Sites.Remotes[:1]
	if e := errorsAt(c)["vpn.sites.address"]; e == "" {
		t.Fatal("a site without this gate's tunnel address")
	}
}
