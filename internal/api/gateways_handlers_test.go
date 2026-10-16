package api

import (
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/gateways"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/rendertest"
)

type fakeGateways struct{}

func (fakeGateways) Report() gateways.Report {
	return gateways.Report{Enabled: true, Gateways: []gateways.Status{{Name: "wan1", State: "down", Reason: "50% packet loss"}},
		Targets: []gateways.TargetStatus{}, Default: gateways.TargetStatus{Name: "default", Active: []string{"wan2"}, Mode: "gateways"}}
}

func TestGatewayStatus(t *testing.T) {
	a := newTestAPI(t)
	if out := a.expect("GET", "/api/v1/gateways/status", nil, 200); out["enabled"] != false {
		t.Fatalf("no monitor: %v", out)
	}
	a = newTestAPIWith(t, func(s *Server) { s.Gateways = fakeGateways{} })
	out := a.expect("GET", "/api/v1/gateways/status", nil, 200)
	gw := out["gateways"].([]any)[0].(map[string]any)
	if gw["state"] != "down" || out["default"].(map[string]any)["active"].([]any)[0] != "wan2" {
		t.Fatalf("status %v", out)
	}
}

func TestWireGuardClientKeys(t *testing.T) {
	dir := t.TempDir()
	a := newTestAPIWith(t, func(s *Server) { s.WireGuardKeyDir = dir })
	if err := a.store.SetCandidate(rendertest.MultiWAN()); err != nil {
		t.Fatal(err)
	}
	pub := a.expect("GET", "/api/v1/vpn/wireguard-clients/vpn/public-key", nil, 200)["public_key"].(string)
	if len(pub) != 44 {
		t.Fatalf("public key %q", pub)
	}
	// a pasted private key replaces the generated one
	priv := "YAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk="
	got := a.expect("PUT", "/api/v1/vpn/wireguard-clients/vpn/private-key", map[string]string{"private_key": priv}, 200)["public_key"].(string)
	if got == pub {
		t.Fatal("the public key did not change")
	}
	if again := a.expect("GET", "/api/v1/vpn/wireguard-clients/vpn/public-key", nil, 200)["public_key"]; again != got {
		t.Fatalf("stored key not used: %v", again)
	}
	if out := a.expect("PUT", "/api/v1/vpn/wireguard-clients/vpn/private-key", map[string]string{"private_key": "nope"}, 400); errCode(out) != "invalid_key" {
		t.Fatalf("bad key: %v", out)
	}
	a.expect("GET", "/api/v1/vpn/wireguard-clients/missing/public-key", nil, 404)
}
