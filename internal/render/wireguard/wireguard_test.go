package wireguard

import (
	"flag"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/rendertest"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/")

// Fixture is a server with a phone and a site-to-site peer, and a
// disabled second interface.
func Fixture() *config.Config {
	cfg := config.Default()
	cfg.VPN.WireGuard = []config.WireGuard{
		{Name: "wg0", Enabled: true, Address: netip.MustParsePrefix("10.66.0.1/24"), Endpoint: "gate.example.net",
			Peers: []config.WireGuardPeer{
				{Name: "phone", PublicKey: "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=", Address: netip.MustParseAddr("10.66.0.2")},
				{Name: "cabin", PublicKey: "TrMvSoP4jYQlY6RIzBgbssQqY3vxI2Pi+y71lOWWXX0=", Address: netip.MustParseAddr("10.66.0.3"),
					AllowedIPs: []netip.Prefix{netip.MustParsePrefix("192.168.50.0/24")}, Keepalive: 25},
			}},
		{Name: "wg1", Enabled: false, ListenPort: 51821, Address: netip.MustParsePrefix("10.67.0.1/24")},
	}
	return cfg
}

func TestGolden(t *testing.T) {
	cfg := Fixture()
	if errs := config.Validate(cfg); len(errs) > 0 {
		t.Fatal(errs)
	}
	files, err := Renderer{}.Render(cfg)
	if err != nil || len(files) != 2 {
		t.Fatalf("%v %v", len(files), err)
	}
	for p, got := range files {
		golden := filepath.Join("testdata", filepath.Base(p))
		if *update {
			os.WriteFile(golden, got, 0o644)
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != strings.ReplaceAll(string(want), "\r\n", "\n") {
			t.Errorf("%s:\n%s", p, got)
		}
	}
}

func TestValidate(t *testing.T) {
	cfg := Fixture()
	w := &cfg.VPN.WireGuard[0]
	w.Peers = append(w.Peers, config.WireGuardPeer{Name: "phone", PublicKey: "short", Address: netip.MustParseAddr("10.99.0.2")})
	cfg.VPN.WireGuard[1].Name = "lan" // taken by an interface
	cfg.VPN.WireGuard[1].ListenPort = 51820
	got := map[string]bool{}
	for _, e := range config.Validate(cfg) {
		got[e.Path] = true
	}
	for _, p := range []string{"vpn.wireguard[0].peers[2].name", "vpn.wireguard[0].peers[2].public_key", "vpn.wireguard[0].peers[2].address", "vpn.wireguard[1].name", "vpn.wireguard[1].listen_port"} {
		if !got[p] {
			t.Errorf("missing %s in %v", p, got)
		}
	}
}

// TestClientGolden (task 049): a VPN-provider tunnel used as a gateway.
func TestClientGolden(t *testing.T) {
	files, err := Renderer{}.Render(rendertest.MultiWAN())
	if err != nil || len(files) != 2 {
		t.Fatalf("%v %v", len(files), err)
	}
	for p, got := range files {
		golden := filepath.Join("testdata", "client-"+filepath.Base(p))
		if *update {
			os.WriteFile(golden, got, 0o644)
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != strings.ReplaceAll(string(want), "\r\n", "\n") {
			t.Errorf("%s:\n%s", p, got)
		}
	}
}
