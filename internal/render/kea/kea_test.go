package kea

import (
	"encoding/json"
	"flag"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/")

func pfx(s string) *netip.Prefix { p := netip.MustParsePrefix(s); return &p }
func ip(s string) *netip.Addr    { a := netip.MustParseAddr(s); return &a }

// vlans has DHCP on the lan, an enabled VLAN (with reservations, custom DNS,
// gateway and lease time) and a disabled VLAN that must be left out.
func vlans() *config.Config {
	cfg := config.Default()
	cfg.System.Domain = "home.arpa"
	cfg.VLANs = []config.VLAN{
		{Name: "guest", Parent: "lan", VLANID: 20, Mode: "static", Address: pfx("192.168.20.1/24"), Enabled: true},
		{Name: "iot", Parent: "lan", VLANID: 30, Mode: "static", Address: pfx("192.168.30.1/24"), Enabled: false},
	}
	cfg.DHCP = append(cfg.DHCP,
		config.DHCPServer{
			Interface:    "guest",
			RangeStart:   netip.MustParseAddr("192.168.20.100"),
			RangeEnd:     netip.MustParseAddr("192.168.20.199"),
			LeaseSeconds: 600,
			DNS:          []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("9.9.9.9")},
			Gateway:      ip("192.168.20.254"),
			StaticLeases: []config.DHCPStaticLease{
				{MAC: "aa:bb:cc:dd:ee:01", IP: netip.MustParseAddr("192.168.20.10"), Hostname: "printer"},
				{MAC: "aa:bb:cc:dd:ee:02", IP: netip.MustParseAddr("192.168.20.11")},
			},
		},
		config.DHCPServer{
			Interface:  "iot",
			RangeStart: netip.MustParseAddr("192.168.30.100"),
			RangeEnd:   netip.MustParseAddr("192.168.30.199"),
		},
	)
	return cfg
}

func noDHCP() *config.Config {
	cfg := config.Default()
	cfg.DHCP = nil
	return cfg
}

// TestGolden compares the rendered file with testdata/<case>.json and checks
// it is valid JSON with the Dhcp4 top-level object kea-dhcp4 expects.
// Regenerate with: go test ./internal/render/kea -update
// scripts/linux-test.ps1 runs the real `kea-dhcp4 -t` on rendered configs.
func TestGolden(t *testing.T) {
	cases := map[string]*config.Config{
		"default": config.Default(),
		"vlans":   vlans(),
		"none":    noDHCP(),
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
			if len(files) != 1 || files[Path] == nil {
				t.Fatalf("want exactly %s, got %v", Path, files)
			}
			got := files[Path]
			var top map[string]json.RawMessage
			if err := json.Unmarshal(got, &top); err != nil || len(top) != 1 || top["Dhcp4"] == nil {
				t.Fatalf("not a {\"Dhcp4\": ...} object: %v", err)
			}
			golden := filepath.Join("testdata", name+".json")
			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != strings.ReplaceAll(string(want), "\r\n", "\n") {
				t.Errorf("differs from %s:\n--- got\n%s--- want\n%s", golden, got, want)
			}
		})
	}
}

func TestDetails(t *testing.T) {
	files, err := Renderer{}.Render(vlans())
	if err != nil {
		t.Fatal(err)
	}
	var f file
	if err := json.Unmarshal(files[Path], &f); err != nil {
		t.Fatal(err)
	}
	d := f.Dhcp4
	if got := strings.Join(d.InterfacesConfig.Interfaces, " "); got != "eth1 eth1.20" {
		t.Errorf("interfaces = %q, want lan device and guest VLAN device only", got)
	}
	if len(d.Subnet4) != 2 {
		t.Fatalf("got %d subnets, want 2 (disabled iot left out)", len(d.Subnet4))
	}
	lan, guest := d.Subnet4[0], d.Subnet4[1]
	if lan.Subnet != "192.168.1.0/24" {
		t.Errorf("lan subnet = %q, want the network address", lan.Subnet)
	}
	if lan.OptionData[0].Data != "192.168.1.1" || lan.OptionData[1].Data != "192.168.1.1" {
		t.Errorf("lan router/dns = %+v, want the interface address", lan.OptionData)
	}
	if guest.Interface != "eth1.20" || guest.ValidLifetime != 600 || guest.ID != 2 {
		t.Errorf("guest subnet = %+v", guest)
	}
	if guest.OptionData[0].Data != "192.168.20.254" || guest.OptionData[1].Data != "1.1.1.1, 9.9.9.9" {
		t.Errorf("guest options = %+v", guest.OptionData)
	}
	if len(guest.Reservations) != 2 || guest.Reservations[0].Hostname != "printer" {
		t.Errorf("guest reservations = %+v", guest.Reservations)
	}
}

func TestRenderErrors(t *testing.T) {
	unknown := config.Default()
	unknown.DHCP[0].Interface = "nope"
	noAddr := config.Default()
	noAddr.Interfaces[1].Address = nil
	for name, cfg := range map[string]*config.Config{"unknown interface": unknown, "no address": noAddr} {
		if _, err := (Renderer{}).Render(cfg); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
