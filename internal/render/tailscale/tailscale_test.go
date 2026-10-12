package tailscale

import (
	"encoding/json"
	"net/netip"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	ts "github.com/networkgoblinlabs/goblin-gate/internal/tailscale"
)

func TestRender(t *testing.T) {
	cfg := config.Default()
	g := netip.MustParsePrefix("192.168.20.1/24")
	cfg.VLANs = []config.VLAN{{Name: "guest", Parent: "lan", VLANID: 20, Mode: "static", Address: &g, Enabled: true}}
	files, err := Renderer{}.Render(cfg)
	if err != nil || len(files) != 1 {
		t.Fatalf("%v %v", files, err)
	}
	var d ts.Desired
	json.Unmarshal(files[ts.PrefsPath], &d)
	if d.Enabled {
		t.Fatal("disabled config rendered enabled")
	}

	cfg.VPN.Tailscale = config.Tailscale{Enabled: true, AdvertiseRoutes: []string{"lan", "guest"}, ExitNode: true, LoginServer: "https://hs.example"}
	if errs := config.Validate(cfg); len(errs) > 0 {
		t.Fatal(errs)
	}
	files, _ = Renderer{}.Render(cfg)
	json.Unmarshal(files[ts.PrefsPath], &d)
	want := ts.Desired{Enabled: true, Hostname: "goblin-gate", AdvertiseRoutes: []string{"192.168.1.0/24", "192.168.20.0/24"}, ExitNode: true, LoginServer: "https://hs.example"}
	if diff := cmp.Diff(want, d); diff != "" {
		t.Fatal(diff)
	}
}

func TestValidate(t *testing.T) {
	cfg := config.Default()
	cfg.VPN.Tailscale = config.Tailscale{Enabled: true, AdvertiseRoutes: []string{"wan1", "nope"}, Hostname: "bad name", LoginServer: "http://x", AllowAccess: []string{"telnet"}}
	cfg.Policies[0].SrcIntf = []string{"tailscale"}
	got := map[string]bool{}
	for _, e := range config.Validate(cfg) {
		got[e.Path] = true
	}
	for _, p := range []string{"vpn.tailscale.advertise_routes[0]", "vpn.tailscale.advertise_routes[1]", "vpn.tailscale.hostname", "vpn.tailscale.login_server", "vpn.tailscale.allow_access[0]"} {
		if !got[p] {
			t.Errorf("missing error %s (got %v)", p, got)
		}
	}
	if got["policies[0].src_intf[0]"] {
		t.Error("tailscale interface not accepted in policies")
	}
	cfg.VPN.Tailscale = config.Tailscale{}
	for _, e := range config.Validate(cfg) {
		if e.Path == "policies[0].src_intf[0]" {
			return
		}
	}
	t.Error("tailscale interface accepted while Tailscale is off")
}
