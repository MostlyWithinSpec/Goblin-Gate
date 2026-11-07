package config

import (
	"strings"
	"testing"
)

func proxyHost(name, upstream string, domains ...string) ProxyHost {
	return ProxyHost{Name: name, Enabled: true, Domains: domains, Upstream: upstream}
}

func TestReverseProxyValidation(t *testing.T) {
	jelly := proxyHost("jellyfin", "http://192.168.1.10:8096", "media.example.com")
	ok := []ReverseProxy{
		{},
		{Enabled: true, Email: "me@example.com", Hosts: []ProxyHost{jelly}},
		{Enabled: true, Hosts: []ProxyHost{
			jelly,
			{Name: "nas", Enabled: true, Domains: []string{"nas.example.com", "files.example.com"}, Upstream: "https://192.168.1.20:5001", InsecureSkipVerify: true, Allow: []string{"203.0.113.0/24", "2001:db8::/32", "198.51.100.7"}},
			proxyHost("ha", "http://homeassistant.home.lan:8123", "ha.example.com"),
		}},
		{Enabled: false, Hosts: []ProxyHost{proxyHost("x", "http://[fd00::5]:80", "x.example.com")}},
	}
	for _, rp := range ok {
		c := Default()
		c.AllowNoAdmin = true
		c.ReverseProxy = rp
		if errs := Validate(c); len(errs) > 0 {
			t.Errorf("%+v: %v", rp, errs)
		}
	}

	bad := map[string]ReverseProxy{
		"reverse_proxy.email":                         {Email: "not an email"},
		"reverse_proxy.hosts[0].name":                 {Hosts: []ProxyHost{proxyHost("bad name", "http://10.0.0.2", "a.example.com")}},
		"reverse_proxy.hosts[1].name":                 {Hosts: []ProxyHost{jelly, proxyHost("jellyfin", "http://10.0.0.2", "b.example.com")}},
		"reverse_proxy.hosts[0].domains":              {Hosts: []ProxyHost{proxyHost("a", "http://10.0.0.2")}},
		"reverse_proxy.hosts[0].domains[0]":           {Hosts: []ProxyHost{proxyHost("a", "http://10.0.0.2", "*.example.com")}},
		"reverse_proxy.hosts[1].domains[0]":           {Hosts: []ProxyHost{jelly, proxyHost("b", "http://10.0.0.2", "media.example.com")}},
		"reverse_proxy.hosts[0].upstream":             {Hosts: []ProxyHost{proxyHost("a", "ftp://10.0.0.2", "a.example.com")}},
		"reverse_proxy.hosts[0].insecure_skip_verify": {Hosts: []ProxyHost{{Name: "a", Domains: []string{"a.example.com"}, Upstream: "http://10.0.0.2", InsecureSkipVerify: true}}},
		"reverse_proxy.hosts[0].allow[0]":             {Hosts: []ProxyHost{{Name: "a", Domains: []string{"a.example.com"}, Upstream: "http://10.0.0.2", Allow: []string{"nope"}}}},
		"reverse_proxy.hosts[0].domains[1]":           {Hosts: []ProxyHost{proxyHost("a", "http://10.0.0.2", "a.example.com", "Upper.example.com")}},
		"reverse_proxy.hosts[0].domains[0] (ip)":      {Hosts: []ProxyHost{proxyHost("a", "http://10.0.0.2", "203.0.113.5")}},
		"reverse_proxy.hosts[0].domains[0] (1 label)": {Hosts: []ProxyHost{proxyHost("a", "http://10.0.0.2", "nas")}},
		"reverse_proxy.hosts[0].upstream (path)":      {Hosts: []ProxyHost{proxyHost("a", "http://10.0.0.2/app", "a.example.com")}},
		"reverse_proxy.hosts[0].upstream (user)":      {Hosts: []ProxyHost{proxyHost("a", "http://u:p@10.0.0.2", "a.example.com")}},
	}
	for path, rp := range bad {
		c := Default()
		c.AllowNoAdmin = true
		c.ReverseProxy = rp
		want, _, _ := strings.Cut(path, " ")
		if !hasErr(Validate(c), want) {
			t.Errorf("%s: no error in %v", path, Validate(c))
		}
	}
}

// The proxy must never publish the box itself: its admin page would be on
// the internet.
func TestReverseProxyRefusesTheBox(t *testing.T) {
	for _, up := range []string{"https://192.168.1.1", "https://192.168.1.1:443", "http://127.0.0.1:8080", "http://localhost:3000", "http://[::1]:80", "http://0.0.0.0"} {
		c := Default()
		c.AllowNoAdmin = true
		c.ReverseProxy = ReverseProxy{Hosts: []ProxyHost{proxyHost("a", up, "a.example.com")}}
		if !hasErr(Validate(c), "reverse_proxy.hosts[0].upstream") {
			t.Errorf("upstream %s accepted", up)
		}
	}
}

func TestReverseProxyPortConflicts(t *testing.T) {
	base := func() *Config {
		c := Default()
		c.AllowNoAdmin = true
		c.ReverseProxy = ReverseProxy{Enabled: true, Hosts: []ProxyHost{proxyHost("a", "http://192.168.1.10", "a.example.com")}}
		return c
	}
	// the admin page on the WAN wants 443
	c := base()
	c.Interfaces[0].AllowAccess = []string{"https"}
	if !hasErr(Validate(c), "reverse_proxy.enabled") {
		t.Error("admin https on the WAN accepted")
	}
	// on another admin port it is fine
	c.System.AdminPort = 8443
	if errs := Validate(c); hasErr(errs, "reverse_proxy.enabled") {
		t.Errorf("admin on 8443: %v", errs)
	}
	// a port forward of TCP 443 (or a range over 80) on the WAN
	for _, ext := range []string{"443", "1-1000"} {
		c = base()
		c.NAT.PortForwards = []PortForward{{Name: "web", InIntf: "wan1", ExtPort: ext, Protocol: "tcp", ToAddr: "192.168.1.10", ToPort: ext}}
		if !hasErr(Validate(c), "nat.port_forwards[0].ext_port") {
			t.Errorf("port forward %s accepted", ext)
		}
	}
	// UDP 443 (QUIC to a game server, say) does not conflict
	c = base()
	c.NAT.PortForwards = []PortForward{{Name: "q", InIntf: "wan1", ExtPort: "443", Protocol: "udp", ToAddr: "192.168.1.10", ToPort: "443"}}
	if errs := Validate(c); hasErr(errs, "nat.port_forwards[0].ext_port") {
		t.Errorf("udp 443: %v", errs)
	}
	// disabled: no conflicts
	c = base()
	c.ReverseProxy.Enabled = false
	c.Interfaces[0].AllowAccess = []string{"https"}
	if errs := Validate(c); hasErr(errs, "reverse_proxy.enabled") {
		t.Errorf("disabled proxy: %v", errs)
	}
}

func TestParseProxyUpstream(t *testing.T) {
	for in, want := range map[string]string{
		"http://10.0.0.2":               "10.0.0.2:80",
		"https://10.0.0.2":              "10.0.0.2:443",
		"http://nas.lan:5000/":          "nas.lan:5000",
		"https://[fd00::5]:8443":        "[fd00::5]:8443",
		"http://Jellyfin.home.lan:8096": "Jellyfin.home.lan:8096",
	} {
		u, err := ParseProxyUpstream(in)
		if err != nil || u.Dial() != want {
			t.Errorf("%s: %v %v, want %s", in, u.Dial(), err, want)
		}
	}
}

func hasErr(errs []ValidationError, path string) bool {
	for _, e := range errs {
		if e.Path == path {
			return true
		}
	}
	return false
}
