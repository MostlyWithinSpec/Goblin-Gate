//go:build linux

package apply

import (
	"context"
	"net/netip"
	"os"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/kea"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/netif"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/nft"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/unbound"
)

// TestRealChecks runs every group's syntax check (nft -c, kea-dhcp4 -t,
// unbound-checkconf) on real renderer output. It needs those tools and
// CAP_NET_ADMIN, so it only runs with GOBLIN_APPLY_CHECK=1
// (scripts/linux-test.ps1 sets it).
func TestRealChecks(t *testing.T) {
	if os.Getenv("GOBLIN_APPLY_CHECK") != "1" {
		t.Skip("set GOBLIN_APPLY_CHECK=1 (needs nft, kea-dhcp4, unbound-checkconf)")
	}
	vlans := config.Default()
	guest := netip.MustParsePrefix("10.20.0.1/24")
	vlans.VLANs = []config.VLAN{{Name: "guest", Parent: "lan", VLANID: 20, Mode: "static", Address: &guest, Enabled: true}}
	vlans.DHCP = append(vlans.DHCP, config.DHCPServer{
		Interface:  "guest",
		RangeStart: netip.MustParseAddr("10.20.0.100"),
		RangeEnd:   netip.MustParseAddr("10.20.0.199"),
		StaticLeases: []config.DHCPStaticLease{
			{MAC: "aa:bb:cc:dd:ee:ff", IP: netip.MustParseAddr("10.20.0.10"), Hostname: "printer"},
		},
	})
	vlans.DNS.Forwarders = []netip.Addr{netip.MustParseAddr("1.1.1.1")}
	vlans.DNS.DNSSEC = true
	vlans.DNS.LocalRecords = map[string]string{"nas.home.arpa": "192.168.1.5"}

	for name, cfg := range map[string]*config.Config{"default": config.Default(), "vlans": vlans} {
		t.Run(name, func(t *testing.T) {
			if errs := config.Validate(cfg); len(errs) > 0 {
				t.Fatalf("fixture invalid: %v", errs)
			}
			files := map[string][]byte{}
			for _, r := range []interface {
				Render(*config.Config) (map[string][]byte, error)
			}{netif.Renderer{}, nft.Renderer{}, kea.Renderer{}, unbound.Renderer{}} {
				out, err := r.Render(cfg)
				if err != nil {
					t.Fatal(err)
				}
				for p, b := range out {
					files[p] = b
				}
			}
			h := &Host{Root: t.TempDir(), Runner: ExecRunner{}}
			var changes []change
			for p, b := range files {
				g, err := groupFor(p)
				if err != nil {
					t.Fatal(err)
				}
				changes = append(changes, change{path: p, group: g, data: b})
			}
			if err := h.check(context.Background(), changes); err != nil {
				t.Fatal(err)
			}
		})
	}
}
