package nft

import (
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func TestInternalCARules(t *testing.T) {
	c := proxyConfig()
	c.InternalCA = config.InternalCA{Enabled: true, ACME: true}
	c.ReverseProxy.Hosts = append(c.ReverseProxy.Hosts, config.ProxyHost{Name: "nas", Enabled: true, Issuer: "internal", Domains: []string{"nas.home.lan"}, Upstream: "http://192.168.1.20:5000"})
	if errs := config.Validate(c); len(errs) > 0 {
		t.Fatal(errs)
	}
	out, err := build(c)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		`iifname { "eth1", "eth1.20" } ip daddr 198.18.0.53 tcp dport 443 redirect to :10443 comment "reverse proxy: https (internal certificates)"`,
		`iifname { "eth1", "eth1.20" } ip daddr 198.18.0.53 tcp dport 80 redirect to :10080`,
		`iifname { "eth1", "eth1.20" } tcp dport 9443 accept comment "internal CA: ACME"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing: %s", want)
		}
	}
	if strings.Contains(s, `"eth0" tcp dport 9443`) {
		t.Error("ACME open on the WAN")
	}
	// ACME off, no internal hosts: neither
	c.InternalCA.ACME = false
	c.ReverseProxy.Hosts = c.ReverseProxy.Hosts[:1]
	out, _ = build(c)
	if strings.Contains(string(out), "9443") || strings.Contains(string(out), "198.18.0.53") {
		t.Errorf("rules without ACME or internal hosts:\n%s", out)
	}
}
