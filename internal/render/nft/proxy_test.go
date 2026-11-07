package nft

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func proxyConfig() *config.Config {
	c := config.Default()
	c.AllowNoAdmin = true
	guest := netip.MustParsePrefix("192.168.20.1/24")
	c.VLANs = []config.VLAN{{Name: "guest", Parent: "lan", VLANID: 20, Mode: "static", Address: &guest, Enabled: true}}
	c.ReverseProxy = config.ReverseProxy{Enabled: true, Hosts: []config.ProxyHost{
		{Name: "jellyfin", Enabled: true, Domains: []string{"media.example.com"}, Upstream: "http://192.168.1.10:8096"},
	}}
	return c
}

func TestReverseProxyRules(t *testing.T) {
	c := proxyConfig()
	if errs := config.Validate(c); len(errs) > 0 {
		t.Fatal(errs)
	}
	out, err := build(c)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		`iifname "eth0" tcp dport 80 redirect to :10080 comment "reverse proxy: http"`,
		`iifname "eth0" tcp dport 443 redirect to :10443 comment "reverse proxy: https"`,
		// LAN clients opening the public name; the box's LAN addresses keep the admin page
		`iifname { "eth1", "eth1.20" } fib daddr type local ip daddr != { 192.168.1.1, 192.168.20.1 } tcp dport 443 redirect to :10443`,
		`iifname { "eth0", "eth1", "eth1.20" } tcp dport { 10080, 10443 } ct status dnat accept comment "reverse proxy"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing: %s", want)
		}
	}
	// the redirects belong to the nat prerouting chain, the accept to input
	nat := s[strings.Index(s, "table ip goblin_nat"):]
	if !strings.Contains(nat, "redirect to :10443") {
		t.Error("redirect not in table ip goblin_nat")
	}
	in := s[strings.Index(s, "chain input {"):strings.Index(s, "chain forward {")]
	if !strings.Contains(in, "ct status dnat accept comment \"reverse proxy\"") {
		t.Error("proxy accept not in the input chain")
	}
	// Caddy's ports never open without the redirect
	if strings.Contains(s, "dport 10443 accept") || strings.Contains(s, "dport { 10080, 10443 } accept") {
		t.Error("proxy ports open without ct status dnat")
	}
}

func TestReverseProxyRulesOff(t *testing.T) {
	for name, edit := range map[string]func(c *config.Config){
		"disabled":         func(c *config.Config) { c.ReverseProxy.Enabled = false },
		"no enabled hosts": func(c *config.Config) { c.ReverseProxy.Hosts[0].Enabled = false },
	} {
		c := proxyConfig()
		edit(c)
		out, err := build(c)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(out), "reverse proxy") {
			t.Errorf("%s: proxy rules rendered", name)
		}
	}
	// lan_access off: only the WAN is redirected
	c := proxyConfig()
	off := false
	c.ReverseProxy.LANAccess = &off
	out, _ := build(c)
	if strings.Contains(string(out), "from the lan") || !strings.Contains(string(out), `iifname "eth0" tcp dport { 10080, 10443 } ct status dnat accept`) {
		t.Errorf("lan_access off:\n%s", out)
	}
}
