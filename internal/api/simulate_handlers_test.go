package api

import (
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func TestSimulatePolicy(t *testing.T) {
	a := newTestAPI(t)
	// the default config: LAN 192.168.1.0/24 with one accept policy to the WAN
	pol := config.Default().Policies[0]
	out := a.expect("POST", "/api/v1/policy/simulate", map[string]any{"src_ip": "192.168.1.10", "dst_ip": "93.184.216.34", "protocol": "tcp", "dst_port": 443}, 200)
	if out["verdict"] != "accept" || out["chain"] != "forward" || out["src_intf"] != "lan" || out["dst_intf"] != "wan1" {
		t.Fatalf("simulate: %v", out)
	}
	if p, _ := out["policy"].(map[string]any); p == nil || p["id"].(float64) != float64(pol.ID) {
		t.Fatalf("policy: %v", out["policy"])
	}
	if steps, _ := out["steps"].([]any); len(steps) == 0 {
		t.Fatalf("steps: %v", out)
	}

	// the candidate, not the running config, when asked
	cfg, _ := a.store.Candidate()
	cfg.Policies[0].Action = "deny"
	if err := a.store.SetCandidate(cfg); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"src_ip": "192.168.1.10", "dst_ip": "93.184.216.34", "protocol": "tcp", "dst_port": 443, "config": "candidate"}
	if out := a.expect("POST", "/api/v1/policy/simulate", body, 200); out["verdict"] != "deny" {
		t.Fatalf("candidate: %v", out)
	}

	for _, bad := range []map[string]any{
		{"src_ip": "nope", "dst_ip": "1.1.1.1", "protocol": "tcp", "dst_port": 1},
		{"src_ip": "192.168.1.10", "dst_ip": "1.1.1.1", "protocol": "sctp"},
		{"src_ip": "192.168.1.10", "dst_ip": "1.1.1.1", "protocol": "tcp", "dst_port": 70000},
		{"src_ip": "192.168.1.10", "dst_ip": "1.1.1.1", "protocol": "icmp", "at": "tomorrow"},
		{"src_ip": "192.168.1.10", "dst_ip": "1.1.1.1", "protocol": "icmp", "config": "other"},
	} {
		a.expect("POST", "/api/v1/policy/simulate", bad, 400)
	}
	if out := a.expect("POST", "/api/v1/policy/simulate", map[string]any{"src_ip": "192.168.1.10", "dst_ip": "1.1.1.1", "protocol": "tcp"}, 422); errCode(out) != "cannot_simulate" {
		t.Fatalf("no port: %v", out)
	}
}

// readonly users may simulate and validate (POSTs that change nothing), not
// change the config.
func TestSimulateReadonly(t *testing.T) {
	a := newAuthAPI(t)
	b := a.browser()
	b.login("viewer")
	b.expect("POST", "/api/v1/policy/simulate", map[string]any{"src_ip": "192.168.1.10", "dst_ip": "192.168.1.1", "protocol": "icmp"}, 200)
	b.expect("POST", "/api/v1/config/validate", nil, 200)
	b.expect("POST", "/api/v1/commit", nil, 403)
}
