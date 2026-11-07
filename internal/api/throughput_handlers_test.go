package api

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/nicinfo"
	"github.com/networkgoblinlabs/goblin-gate/internal/throughput"
)

func TestThroughputAPI(t *testing.T) {
	a := newTestAPI(t)
	a.expect("GET", "/api/v1/diag/throughput", nil, 503)
	a.expect("GET", "/api/v1/diag/nics", nil, 503)

	var mu sync.Mutex
	var open [][]string
	mgr := &throughput.Manager{
		Serve: func(ctx context.Context, addr string, port int) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() },
		SetOpen: func(d []string) error {
			mu.Lock()
			defer mu.Unlock()
			open = append(open, d)
			return nil
		},
	}
	a = newTestAPIWith(t, func(s *Server) { s.Throughput = mgr })
	// the default config: wan1 by DHCP, lan 192.168.1.1/24 on eth1
	for body, code := range map[string]string{
		`{"interface":"wan1"}`:             "wan_interface",
		`{"interface":"nope"}`:             "unknown_interface",
		`{"interface":"lan","minutes":60}`: "invalid_minutes",
		`{"interface":"lan","minutes":-1}`: "invalid_minutes",
	} {
		out := a.expect("POST", "/api/v1/diag/throughput", body, 400)
		if got := out["error"].(map[string]any)["code"]; got != code {
			t.Errorf("%s: %v, want %s", body, got, code)
		}
	}
	out := a.expect("POST", "/api/v1/diag/throughput", map[string]any{"interface": "lan", "minutes": 2}, 201)
	if out["running"] != true || out["address"] != "192.168.1.1" || out["device"] != "eth1" || out["port"] != float64(5201) {
		t.Fatalf("start: %v", out)
	}
	a.expect("POST", "/api/v1/diag/throughput", map[string]any{"interface": "lan"}, 409)
	if out := a.expect("GET", "/api/v1/diag/throughput", nil, 200); out["running"] != true {
		t.Fatalf("status: %v", out)
	}
	if out := a.expect("DELETE", "/api/v1/diag/throughput", nil, 200); out["running"] != false {
		t.Fatalf("stop: %v", out)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(open) != 2 || len(open[0]) != 1 || open[0][0] != "eth1" || open[1] != nil {
		t.Fatalf("firewall: %v", open)
	}
}

func TestNICsAPI(t *testing.T) {
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		if strings.Join(args, " ") == "-k eth1" {
			return []byte("generic-receive-offload: off\n"), nil
		}
		return nil, errors.New("no")
	}
	a := newTestAPIWith(t, func(s *Server) { s.NICs = &nicinfo.Reader{SysRoot: t.TempDir(), Run: run} })
	out := a.expect("GET", "/api/v1/diag/nics", nil, 200)
	list := out["interfaces"].([]any)
	if len(list) != 2 {
		t.Fatalf("%v", out)
	}
	lan := list[1].(map[string]any)
	if lan["name"] != "lan" || lan["device"] != "eth1" || lan["role"] != "lan" {
		t.Fatalf("lan: %v", lan)
	}
	hints := lan["hints"].([]any)
	if len(hints) != 1 || hints[0].(map[string]any)["code"] != "offloads_off" {
		t.Fatalf("hints: %v", hints)
	}
}
