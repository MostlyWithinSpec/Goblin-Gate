package api

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/frr"
)

func TestRoutingStatus(t *testing.T) {
	recorded := map[string]string{}
	for name, cmd := range frr.Commands {
		recorded[cmd] = filepath.Join("..", "frr", "testdata", "recorded", name+".json")
	}
	calls := 0
	a := newTestAPIWith(t, func(s *Server) {
		s.FRR = func(ctx context.Context, args ...string) ([]byte, error) {
			calls++
			return os.ReadFile(recorded[args[1]])
		}
	})
	// off: FRR is not asked
	out := a.expect("GET", "/api/v1/routing/status", nil, 200)
	if out["bgp_enabled"] != false || out["running"] != false || calls != 0 {
		t.Fatalf("off: %v (%d calls)", out, calls)
	}
	cfg, _ := a.store.Running()
	cfg.Routing.BGP = config.BGP{Enabled: true, AS: 65001, Neighbors: []config.BGPNeighbor{{Address: netip.MustParseAddr("10.99.0.3"), RemoteAS: 65002, Enabled: true}}}
	if _, err := a.store.SaveRevision(cfg, "test", "bgp"); err != nil {
		t.Fatal(err)
	}
	out = a.expect("GET", "/api/v1/routing/status", nil, 200)
	bgp, _ := out["bgp"].([]any)
	if out["bgp_enabled"] != true || out["running"] != true || len(bgp) != 2 || out["router_id"] == "" {
		t.Fatalf("on: %v", out)
	}
	if n := bgp[0].(map[string]any); n["address"] != "10.99.0.3" || n["state"] != "Established" || n["up"] != true {
		t.Errorf("neighbour %v", n)
	}
	if routes, _ := out["routes"].([]any); len(routes) < 3 {
		t.Errorf("routes %v", routes)
	}
}
