package config

import (
	"net/netip"
	"testing"
)

func TestInternalCAValidation(t *testing.T) {
	base := func() *Config {
		c := Default()
		c.AllowNoAdmin = true
		return c
	}
	ok := base()
	ok.InternalCA = InternalCA{Enabled: true, ACME: true, ACMEAllow: []string{"192.168.1.0/28"}, LifetimeHours: 24, Name: "Home CA"}
	ok.ReverseProxy = ReverseProxy{Enabled: true, Hosts: []ProxyHost{{Name: "nas", Enabled: true, Issuer: "internal", Domains: []string{"nas.home.lan"}, Upstream: "http://192.168.1.20"}}}
	if errs := Validate(ok); len(errs) > 0 {
		t.Fatal(errs)
	}
	for path, mod := range map[string]func(c *Config){
		"internal_ca.lifetime_hours":        func(c *Config) { c.InternalCA = InternalCA{Enabled: true, LifetimeHours: 500} },
		"internal_ca.name":                  func(c *Config) { c.InternalCA = InternalCA{Enabled: true, Name: "a\"b"} },
		"internal_ca.acme":                  func(c *Config) { c.InternalCA = InternalCA{ACME: true} },
		"internal_ca.acme_allow":            func(c *Config) { c.InternalCA = InternalCA{Enabled: true, ACMEAllow: []string{"10.0.0.0/8"}} },
		"internal_ca.acme_allow[0]":         func(c *Config) { c.InternalCA = InternalCA{Enabled: true, ACME: true, ACMEAllow: []string{"nope"}} },
		"reverse_proxy.hosts[0].issuer":     func(c *Config) { c.ReverseProxy.Hosts[0].Issuer = "internal" },
		"reverse_proxy.hosts[0].issuer (x)": func(c *Config) { c.InternalCA.Enabled = true; c.ReverseProxy.Hosts[0].Issuer = "zerossl" },
		"internal_ca": func(c *Config) {
			p := netip.MustParsePrefix("198.18.0.1/24")
			c.Interfaces[1].Address = &p
		},
	} {
		c := base()
		c.ReverseProxy = ReverseProxy{Enabled: true, Hosts: []ProxyHost{{Name: "nas", Enabled: true, Domains: []string{"nas.example.com"}, Upstream: "http://192.168.1.20"}}}
		mod(c)
		want := path
		if i := len(path) - 4; i > 0 && path[i:] == " (x)" {
			want = path[:i]
		}
		if !hasErr(Validate(c), want) {
			t.Errorf("%s: no error (%v)", path, Validate(c))
		}
	}
}
