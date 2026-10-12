//go:build linux

package apply

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/kea"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/netif"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/nft"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/unbound"
	wgrender "github.com/networkgoblinlabs/goblin-gate/internal/render/wireguard"
	"github.com/networkgoblinlabs/goblin-gate/internal/wireguard"
)

// TestE2E drives the real applier (NewLinux) against a live Debian system:
// systemd-networkd, nftables, kea-dhcp4-server and unbound. It rewrites
// /etc and the firewall, so it only runs with GOBLIN_APPLY_E2E=1 inside the
// throwaway container of scripts/linux-e2e.ps1, which also creates eth1.
func TestE2E(t *testing.T) {
	if os.Getenv("GOBLIN_APPLY_E2E") != "1" {
		t.Skip("set GOBLIN_APPLY_E2E=1 (destructive: run via scripts/linux-e2e.ps1)")
	}
	ctx := context.Background()
	h := NewLinux(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	sh := func(t *testing.T, cmd string) string {
		t.Helper()
		out, err := exec.Command("sh", "-c", cmd).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v: %s", cmd, err, out)
		}
		return string(out)
	}
	linkExists := func(dev string) bool {
		_, err := os.Stat("/sys/class/net/" + dev)
		return err == nil
	}
	apply := func(cfg *config.Config) error {
		t.Helper()
		if errs := config.Validate(cfg); len(errs) > 0 {
			t.Fatalf("invalid config: %v", errs)
		}
		files := map[string][]byte{}
		for _, r := range []interface {
			Render(*config.Config) (map[string][]byte, error)
		}{netif.Renderer{}, nft.Renderer{}, kea.Renderer{}, unbound.Renderer{}, wgrender.Renderer{}} {
			out, err := r.Render(cfg)
			if err != nil {
				t.Fatal(err)
			}
			for p, b := range out {
				files[p] = b
			}
		}
		if err := wireguard.EnsureKeys(config.WireGuardKeyDir, cfg); err != nil {
			t.Fatal(err)
		}
		return h.Apply(ctx, files)
	}
	withVLAN := func(id int) *config.Config {
		cfg := config.Default()
		p := netip.MustParsePrefix("192.168.20.1/24")
		cfg.VLANs = []config.VLAN{{Name: "guest", Parent: "lan", VLANID: id, Mode: "static", Address: &p, Enabled: true}}
		cfg.DHCP = append(cfg.DHCP, config.DHCPServer{
			Interface:  "guest",
			RangeStart: netip.MustParseAddr("192.168.20.100"),
			RangeEnd:   netip.MustParseAddr("192.168.20.199"),
		})
		return cfg
	}

	t.Run("first apply", func(t *testing.T) {
		if err := apply(withVLAN(20)); err != nil {
			t.Fatal(err)
		}
		if !linkExists("eth1.20") {
			t.Error("VLAN device eth1.20 not created")
		}
		if got := sh(t, "nft list tables"); !strings.Contains(got, "inet goblin") || !strings.Contains(got, "ip goblin_nat") {
			t.Errorf("goblin tables not loaded: %s", got)
		}
		if got := sh(t, "cat /proc/sys/net/ipv4/ip_forward"); strings.TrimSpace(got) != "1" {
			t.Errorf("ip_forward = %q", got)
		}
		sh(t, "systemctl is-active kea-dhcp4-server unbound")
		if got := sh(t, "ss -lunp"); !strings.Contains(got, "192.168.20.1:53") {
			t.Errorf("unbound not listening on the VLAN address: %s", got)
		}
	})

	t.Run("vlan id change recreates the device", func(t *testing.T) {
		if err := apply(withVLAN(21)); err != nil {
			t.Fatal(err)
		}
		if linkExists("eth1.20") || !linkExists("eth1.21") {
			t.Errorf("want eth1.21 only: eth1.20=%v eth1.21=%v", linkExists("eth1.20"), linkExists("eth1.21"))
		}
		if got := sh(t, "cat "+kea.Path); !strings.Contains(got, `"eth1.21"`) {
			t.Error("kea config not updated")
		}
		sh(t, "systemctl is-active kea-dhcp4-server")
	})

	t.Run("activation failure rolls back", func(t *testing.T) {
		before := sh(t, "cat "+kea.Path)
		sh(t, "systemctl mask --runtime kea-dhcp4-server")
		defer sh(t, "systemctl unmask --runtime kea-dhcp4-server && systemctl restart kea-dhcp4-server")
		cfg := withVLAN(21)
		cfg.DHCP[1].RangeEnd = netip.MustParseAddr("192.168.20.150")
		err := apply(cfg)
		if err == nil || !strings.Contains(err.Error(), "previous files restored") && !strings.Contains(err.Error(), "rollback also failed") {
			t.Fatalf("err = %v, want an activation failure with rollback", err)
		}
		t.Logf("expected error: %v", err)
		if got := sh(t, "cat "+kea.Path); got != before {
			t.Error("kea config not restored after failed activation")
		}
	})

	t.Run("bad ruleset is rejected before writing", func(t *testing.T) {
		before := sh(t, "cat "+nft.Path)
		err := h.Apply(ctx, map[string][]byte{nft.Path: []byte("table inet goblin { chain x { nonsense } }\n")})
		if err == nil || !strings.Contains(err.Error(), "check "+nft.Path) {
			t.Fatalf("err = %v, want check failure", err)
		}
		if got := sh(t, "cat "+nft.Path); got != before {
			t.Error("ruleset file changed after a failed check")
		}
	})

	t.Run("removing the vlan deletes the device", func(t *testing.T) {
		if err := apply(config.Default()); err != nil {
			t.Fatal(err)
		}
		if linkExists("eth1.21") {
			t.Error("eth1.21 still exists")
		}
		if _, err := os.Stat("/etc/systemd/network/20-goblin-guest.netdev"); err == nil {
			t.Error("stale netdev file left behind")
		}
		sh(t, "systemctl is-active kea-dhcp4-server unbound")
	})

	// WireGuard (task 036): networkd builds wg0 from the rendered netdev
	// with the key file goblind created; a peer change recreates it.
	withWG := func(peers int) *config.Config {
		cfg := config.Default()
		w := config.WireGuard{Name: "wg0", Enabled: true, Address: netip.MustParsePrefix("10.66.0.1/24")}
		for i := 0; i < peers; i++ {
			k, _ := wireguard.GenerateKey()
			w.Peers = append(w.Peers, config.WireGuardPeer{Name: fmt.Sprintf("p%d", i), PublicKey: k.Public().String(), Address: netip.AddrFrom4([4]byte{10, 66, 0, byte(2 + i)})})
		}
		cfg.VPN.WireGuard = []config.WireGuard{w}
		return cfg
	}
	wgPeers := func() int {
		out, err := exec.Command("wg", "show", "wg0", "peers").Output()
		if err != nil {
			return -1
		}
		return len(strings.Fields(string(out)))
	}
	waitFor := func(cond func() bool) bool {
		for i := 0; i < 50; i++ {
			if cond() {
				return true
			}
			time.Sleep(100 * time.Millisecond)
		}
		return false
	}
	t.Run("wireguard interface", func(t *testing.T) {
		if err := apply(withWG(1)); err != nil {
			t.Fatal(err)
		}
		if !waitFor(func() bool { return wgPeers() == 1 }) {
			t.Fatalf("wg0 not up with 1 peer: %d", wgPeers())
		}
		if port := strings.TrimSpace(sh(t, "wg show wg0 listen-port")); port != "51820" {
			t.Errorf("listen port %q", port)
		}
		pub := strings.TrimSpace(sh(t, "wg show wg0 public-key"))
		want, _ := wireguard.EnsureKey(config.WireGuardKeyDir, "wg0")
		if pub != want.String() {
			t.Errorf("device key %s, goblind key %s", pub, want)
		}
		if got := sh(t, "ip -4 addr show wg0"); !strings.Contains(got, "10.66.0.1/24") {
			t.Errorf("address: %s", got)
		}
		if err := apply(withWG(2)); err != nil {
			t.Fatal(err)
		}
		if !waitFor(func() bool { return wgPeers() == 2 }) {
			t.Fatalf("peer change not applied: %d peers", wgPeers())
		}
		if err := apply(config.Default()); err != nil {
			t.Fatal(err)
		}
		if !waitFor(func() bool { return !linkExists("wg0") }) {
			t.Error("wg0 not removed")
		}
	})
}
