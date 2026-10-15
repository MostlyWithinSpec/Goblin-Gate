package netif

import (
	"flag"
	"net/netip"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/")

func pfx(s string) *netip.Prefix { p := netip.MustParsePrefix(s); return &p }
func ip(s string) *netip.Addr    { a := netip.MustParseAddr(s); return &a }

func staticWAN() *config.Config {
	cfg := config.Default()
	cfg.Interfaces[0].Mode = "static"
	cfg.Interfaces[0].Address = pfx("203.0.113.10/29")
	cfg.Interfaces[0].Gateway = ip("203.0.113.9")
	cfg.Interfaces[0].MTU = 1492
	return cfg
}

func twoVLANs() *config.Config {
	cfg := config.Default()
	cfg.VLANs = []config.VLAN{
		{Name: "iot", Parent: "lan", VLANID: 30, Mode: "static", Address: pfx("192.168.30.1/24"), Enabled: true},
		{Name: "guest", Parent: "lan", VLANID: 20, Mode: "static", Address: pfx("192.168.20.1/24"), MTU: 1400, Enabled: true},
	}
	return cfg
}

func disabled() *config.Config {
	cfg := twoVLANs()
	cfg.Interfaces[1].Enabled = false
	cfg.VLANs[0].Enabled = false
	return cfg
}

// dualStack: IPv4 DHCP + DHCPv6 with a /60 on WAN, delegated /64s on LAN
// (prefix 0) and two VLANs (1, 2); the guest VLAN has a DNS filter, so its
// router advertisements do not offer DNS.
func dualStack() *config.Config {
	cfg := twoVLANs()
	cfg.Interfaces[0].IPv6 = &config.IPv6{Mode: "dhcp6", PDSize: 60}
	cfg.Interfaces[1].IPv6 = &config.IPv6{Mode: "delegated"}
	cfg.VLANs[0].IPv6 = &config.IPv6{Mode: "delegated", PrefixID: 1}
	cfg.VLANs[1].IPv6 = &config.IPv6{Mode: "delegated", PrefixID: 2, RA: &config.RA{Enabled: true, DNS: true, Other: true}}
	cfg.Profiles.DNSFilter = []config.DNSFilterProfile{{Name: "kids", BlockDomains: []string{"example.com"}}}
	cfg.Policies = append([]config.Policy{{ID: 7, Name: "guest-out", Enabled: true, SrcIntf: []string{"guest"}, DstIntf: []string{"wan1"},
		SrcAddr: []string{"all"}, DstAddr: []string{"all"}, Service: []string{"all"}, Action: "accept", NAT: true, DNSFilterProfile: "kids"}}, cfg.Policies...)
	return cfg
}

// staticV6: static IPv6 on WAN (link-local gateway) and LAN (announced /64),
// a v6-only VLAN without router advertisements.
func staticV6() *config.Config {
	cfg := staticWAN()
	cfg.Interfaces[0].IPv6 = &config.IPv6{Mode: "static", Address: pfx("2001:db8::2/64"), Gateway: ip("fe80::1")}
	cfg.Interfaces[1].IPv6 = &config.IPv6{Mode: "static", Address: pfx("2001:db8:1::1/64")}
	cfg.VLANs = []config.VLAN{{Name: "lab", Parent: "lan", VLANID: 40, Mode: "none", Enabled: true,
		IPv6: &config.IPv6{Mode: "static", Address: pfx("fd00:40::1/64"), RA: &config.RA{}}}}
	return cfg
}

// TestGolden compares every rendered file with testdata/<case>/<base name>.
// Regenerate with: go test ./internal/render/netif -update
func TestGolden(t *testing.T) {
	cases := map[string]*config.Config{
		"default":    config.Default(),
		"static-wan": staticWAN(),
		"vlans":      twoVLANs(),
		"disabled":   disabled(),
		"dual-stack": dualStack(),
		"static-v6":  staticV6(),
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := config.Validate(cfg); err != nil {
				t.Fatalf("fixture is not a valid config: %v", err)
			}
			files, err := Renderer{}.Render(cfg)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join("testdata", name)
			if *update {
				os.RemoveAll(dir)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				for p, b := range files {
					if err := os.WriteFile(filepath.Join(dir, path.Base(p)), b, 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			for _, e := range entries {
				want = append(want, e.Name())
			}
			var got []string
			for p := range files {
				got = append(got, path.Base(p))
			}
			sort.Strings(got)
			if strings.Join(got, " ") != strings.Join(want, " ") {
				t.Fatalf("rendered files %v, golden files %v", got, want)
			}
			for p, b := range files {
				g, _ := os.ReadFile(filepath.Join(dir, path.Base(p)))
				if string(b) != strings.ReplaceAll(string(g), "\r\n", "\n") {
					t.Errorf("%s differs from golden:\n--- got\n%s--- want\n%s", p, b, g)
				}
			}
		})
	}
}

func TestPaths(t *testing.T) {
	files, err := Renderer{}.Render(twoVLANs())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{SysctlPath, NetworkPath(10, "lan"), NetworkPath(10, "wan1"),
		NetdevPath("guest"), NetworkPath(20, "guest"), NetdevPath("iot"), NetworkPath(20, "iot")} {
		b, ok := files[p]
		if !ok {
			t.Errorf("missing %s", p)
			continue
		}
		if !strings.HasPrefix(string(b), header) || !strings.HasSuffix(string(b), "\n") {
			t.Errorf("%s: missing header or trailing newline", p)
		}
	}
	if len(files) != 7 {
		t.Errorf("got %d files, want 7", len(files))
	}
}

func TestDeterministic(t *testing.T) {
	cfg := twoVLANs()
	first, _ := Renderer{}.Render(cfg)
	cfg.VLANs[0], cfg.VLANs[1] = cfg.VLANs[1], cfg.VLANs[0]
	second, _ := Renderer{}.Render(cfg)
	for p, b := range first {
		if string(second[p]) != string(b) {
			t.Errorf("%s changed when VLAN order changed", p)
		}
	}
}

func TestErrors(t *testing.T) {
	cases := map[string]func(*config.Config){
		"unknown parent":  func(c *config.Config) { c.VLANs[0].Parent = "nope" },
		"vlan id range":   func(c *config.Config) { c.VLANs[0].VLANID = 4095 },
		"duplicate name":  func(c *config.Config) { c.VLANs[1].Name = "iot" },
		"duplicate vlan":  func(c *config.Config) { c.VLANs[1].VLANID = 30 },
		"bad device":      func(c *config.Config) { c.Interfaces[1].Device = "eth1\nName=x" },
		"long device":     func(c *config.Config) { c.Interfaces[1].Device = "enp0s20f0u1u2c2" },
		"static no addr":  func(c *config.Config) { c.Interfaces[1].Address = nil },
		"ipv6 address":    func(c *config.Config) { c.Interfaces[1].Address = pfx("fd00::1/64") },
		"unknown mode":    func(c *config.Config) { c.Interfaces[1].Mode = "pppoe" },
		"bad name":        func(c *config.Config) { c.VLANs[0].Name = "../x" },
		"vlan ipv6 gw":    func(c *config.Config) { c.VLANs[0].Gateway = ip("fd00::1") },
		"duplicate iface": func(c *config.Config) { c.Interfaces[1].Device = "eth0" },
	}
	for name, mutate := range cases {
		cfg := twoVLANs()
		mutate(cfg)
		if _, err := (Renderer{}).Render(cfg); err == nil {
			t.Errorf("%s: Render succeeded", name)
		} else if !strings.HasPrefix(err.Error(), "render netif: ") {
			t.Errorf("%s: error %q lacks prefix", name, err)
		}
	}
}
