package api

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func TestWakeBroadcastCalc(t *testing.T) {
	// Interface /24 → broadcast .255
	ifaceAddr := netip.MustParsePrefix("192.168.1.0/24")
	bcast, ok := wolBroadcast(&config.Config{
		Interfaces: []config.Interface{{Name: "lan", Address: &ifaceAddr}},
	}, "lan")
	if !ok || bcast != netip.MustParseAddr("192.168.1.255") {
		t.Fatalf("/24 broadcast = %v, ok=%v; want 192.168.1.255", bcast, ok)
	}

	// VLAN /26 → broadcast .63
	vlanAddr := netip.MustParsePrefix("10.0.0.0/26")
	bcast, ok = wolBroadcast(&config.Config{
		VLANs: []config.VLAN{{Name: "guest", Address: &vlanAddr}},
	}, "guest")
	if !ok || bcast != netip.MustParseAddr("10.0.0.63") {
		t.Fatalf("/26 broadcast = %v, ok=%v; want 10.0.0.63", bcast, ok)
	}

	// Interface with no IPv4 address → false
	if _, ok := wolBroadcast(&config.Config{
		Interfaces: []config.Interface{{Name: "lan"}},
	}, "lan"); ok {
		t.Fatal("no-address interface should return false")
	}

	// Unknown name → false
	if _, ok := wolBroadcast(&config.Config{}, "nonexistent"); ok {
		t.Fatal("unknown name should return false")
	}

	// IPv6-only address → false
	v6 := netip.MustParsePrefix("fd00::/64")
	if _, ok := wolBroadcast(&config.Config{
		Interfaces: []config.Interface{{Name: "lan", Address: &v6}},
	}, "lan"); ok {
		t.Fatal("IPv6 address should return false (no IPv4)")
	}
}

func TestWakeNotFound(t *testing.T) {
	store := newDeviceStore(t)
	if err := store.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	a := newTestAPIWith(t, func(s *Server) { s.Devices = store })

	out := a.expect("POST", `/api/v1/devices/aabbccddee99/wake`, nil, 404)
	if errCode(out) != "not_found" {
		t.Fatalf("error code = %q", errCode(out))
	}
}

func TestWakeNoSubnet(t *testing.T) {
	store := newDeviceStore(t)
	// Seed a device on interface "lan".
	store.Runner = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte(`[{"dst":"192.168.1.10","dev":"eth0","lladdr":"aa:bb:cc:dd:ee:01","state":"REACHABLE"}]`), nil
	}
	store.Interfaces = func() map[string]string { return map[string]string{"eth0": "lan"} }
	if err := store.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	a := newTestAPIWith(t, func(s *Server) { s.Devices = store })

	// Commit a running config where "lan" exists but has no IPv4 address.
	run, _ := a.store.Running()
	run.Interfaces = []config.Interface{{Name: "lan", Device: "eth0", Role: "lan"}}
	if _, err := a.store.SaveRevision(run, "test", "wol-no-subnet"); err != nil {
		t.Fatal(err)
	}

	out := a.expect("POST", `/api/v1/devices/aabbccddee01/wake`, nil, 422)
	if errCode(out) != "no_subnet" {
		t.Fatalf("error code = %q, want no_subnet", errCode(out))
	}
}

func TestWakeHappyPath(t *testing.T) {
	store := newDeviceStore(t)
	// Seed a device on interface "lan".
	store.Runner = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte(`[{"dst":"192.168.1.10","dev":"eth0","lladdr":"aa:bb:cc:dd:ee:01","state":"REACHABLE"}]`), nil
	}
	store.Interfaces = func() map[string]string { return map[string]string{"eth0": "lan"} }
	if err := store.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	a := newTestAPIWith(t, func(s *Server) { s.Devices = store })

	// Commit a running config with interface "lan" on 192.168.1.0/24.
	lanAddr := netip.MustParsePrefix("192.168.1.0/24")
	run, _ := a.store.Running()
	run.Interfaces = []config.Interface{{Name: "lan", Device: "eth0", Role: "lan", Address: &lanAddr}}
	if _, err := a.store.SaveRevision(run, "test", "wol"); err != nil {
		t.Fatal(err)
	}

	out := a.expect("POST", `/api/v1/devices/aabbccddee01/wake`, nil, 200)
	if out["status"] != "sent" || out["broadcast"] != "192.168.1.255" {
		t.Fatalf("wake: %v", out)
	}
}

func TestWakeAudit(t *testing.T) {
	a := newAuthAPI(t)
	store := newDeviceStore(t)
	// Seed a device on interface "lan".
	store.Runner = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte(`[{"dst":"192.168.1.10","dev":"eth0","lladdr":"aa:bb:cc:dd:ee:01","state":"REACHABLE"}]`), nil
	}
	store.Interfaces = func() map[string]string { return map[string]string{"eth0": "lan"} }
	if err := store.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.server.Devices = store

	// Commit a running config with interface "lan" on 192.168.1.0/24.
	lanAddr := netip.MustParsePrefix("192.168.1.0/24")
	run, _ := a.store.Running()
	run.Interfaces = []config.Interface{{Name: "lan", Device: "eth0", Role: "lan", Address: &lanAddr}}
	if _, err := a.store.SaveRevision(run, "test", "wol"); err != nil {
		t.Fatal(err)
	}

	b := a.browser()
	b.login("admin")
	out := b.expect("POST", `/api/v1/devices/aabbccddee01/wake`, nil, 200)
	if out["status"] != "sent" || out["broadcast"] != "192.168.1.255" {
		t.Fatalf("wake: %v", out)
	}

	audit, _ := os.ReadFile(filepath.Join(a.dir, "audit.log"))
	if !strings.Contains(string(audit), `"event":"device_wake"`) {
		t.Fatalf("audit lacks device_wake:\n%s", audit)
	}
}
