package api

import (
	"context"
	"net/netip"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/wireguard"
)

func TestWireGuardEndpoints(t *testing.T) {
	a := newTestAPI(t)
	a.expect("GET", "/api/v1/vpn/wireguard", nil, 503)

	keyDir := t.TempDir()
	a = newTestAPIWith(t, func(s *Server) {
		s.WireGuardKeyDir = keyDir
		s.WireGuardStats = func(context.Context, string) (map[string]wireguard.PeerStat, error) {
			return map[string]wireguard.PeerStat{}, nil
		}
	})
	cfg, _ := a.store.Candidate()
	cfg.VPN.WireGuard = []config.WireGuard{{Name: "wg0", Enabled: true, Address: netip.MustParsePrefix("10.66.0.1/24"), Endpoint: "gate.example.net"}}
	a.store.SetCandidate(cfg)

	out := a.expect("GET", "/api/v1/vpn/wireguard", nil, 200)
	ifs := out["interfaces"].([]any)
	pub := ifs[0].(map[string]any)["public_key"].(string)
	if len(pub) != 44 {
		t.Fatalf("list: %v", out)
	}
	out = a.expect("POST", "/api/v1/vpn/wireguard/wg0/peers", map[string]any{"name": "phone"}, 201)
	text := out["config"].(string)
	if !strings.Contains(text, "PublicKey = "+pub) || !strings.Contains(text, "Address = 10.66.0.2/32") || !strings.HasPrefix(out["qr_svg"].(string), "<svg") {
		t.Fatalf("peer: %v", out)
	}
	cand, _ := a.store.Candidate()
	p := cand.VPN.WireGuard[0].Peers[0]
	if p.Name != "phone" || p.Address.String() != "10.66.0.2" || strings.Contains(text, "PrivateKey = "+p.PublicKey) {
		t.Fatalf("candidate peer %+v", p)
	}
	a.expect("POST", "/api/v1/vpn/wireguard/wg0/peers", map[string]any{"name": "phone"}, 422) // duplicate name
	a.expect("POST", "/api/v1/vpn/wireguard/nope/peers", map[string]any{"name": "x"}, 404)
}
