package api

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/diag"
)

func TestDiagnosticsAPI(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	release := make(chan struct{})
	a := newTestAPIWith(t, func(s *Server) {
		s.DiagRunner = func(ctx context.Context, name string, args ...string) ([]byte, error) {
			mu.Lock()
			calls = append(calls, name+" "+strings.Join(args, " "))
			mu.Unlock()
			if args[len(args)-1] == "slow.example" {
				<-release
			}
			if name == "ping" {
				return []byte("2 packets transmitted, 2 received, 0% packet loss, time 501ms\nrtt min/avg/max/mdev = 1.0/1.5/2.0/0.5 ms\n"), nil
			}
			return []byte(" 1  192.168.1.1  0.5 ms  0.4 ms  0.3 ms\n"), nil
		}
		s.DiagLookup = func(ctx context.Context, name, typ, server string) (diag.DNSResult, error) {
			return diag.DNSResult{Name: name, Type: "A", Server: "127.0.0.1", RCode: "NOERROR", Answers: []string{"192.0.2.1"}}, nil
		}
	})

	out := a.expect("POST", "/api/v1/diag/ping", map[string]any{"host": "1.1.1.1", "count": 2, "interface": "wan1"}, 200)
	if out["received"] != float64(2) || out["rtt_avg_ms"] != 1.5 {
		t.Fatalf("ping: %v", out)
	}
	if !strings.Contains(calls[0], "-I eth0 1.1.1.1") {
		t.Fatalf("ping args: %q", calls[0])
	}
	a.expect("POST", "/api/v1/diag/ping", map[string]any{"host": "1.1.1.1", "interface": "nope"}, 400)
	a.expect("POST", "/api/v1/diag/ping", map[string]any{"host": "-c1"}, 400)

	out = a.expect("POST", "/api/v1/diag/traceroute", map[string]any{"host": "example.com"}, 200)
	if hops := out["hops"].([]any); len(hops) != 1 || hops[0].(map[string]any)["address"] != "192.168.1.1" {
		t.Fatalf("traceroute: %v", out)
	}
	out = a.expect("POST", "/api/v1/diag/dns", map[string]any{"name": "example.com"}, 200)
	if out["rcode"] != "NOERROR" || out["answers"].([]any)[0] != "192.0.2.1" {
		t.Fatalf("dns: %v", out)
	}

	// two slow ones fill the slots; a third is refused
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.expect("POST", "/api/v1/diag/traceroute", map[string]any{"host": "slow.example"}, 200)
		}()
	}
	for {
		mu.Lock()
		n := 0
		for _, c := range calls {
			if strings.HasSuffix(c, "slow.example") {
				n++
			}
		}
		mu.Unlock()
		if n == 2 {
			break
		}
	}
	if out := a.expect("POST", "/api/v1/diag/ping", map[string]any{"host": "1.1.1.1"}, 429); errCode(out) != "busy" {
		t.Fatalf("third: %v", out)
	}
	close(release)
	wg.Wait()
	a.expect("POST", "/api/v1/diag/ping", map[string]any{"host": "1.1.1.1"}, 200)
}

func TestDiagnosticsAdminsOnly(t *testing.T) {
	a := newAuthAPI(t)
	v := a.browser()
	v.login("viewer")
	v.expect("POST", "/api/v1/diag/ping", map[string]any{"host": "1.1.1.1"}, 403)
	v.expect("POST", "/api/v1/diag/dns", map[string]any{"name": "example.com"}, 403)
}

func TestStatusReportsSQM(t *testing.T) {
	a := newTestAPIWith(t, func(s *Server) { s.SQMActive = func(dev string) bool { return dev == "eth0" } })
	if out := a.expect("GET", "/api/v1/system/status", nil, 200); len(out["sqm"].([]any)) != 0 {
		t.Fatalf("off: %v", out["sqm"])
	}
	cfg, _ := a.store.Running()
	cfg.Interfaces[0].SQM = &config.SQM{Enabled: true, DownloadMbit: 450, UploadMbit: 36}
	if _, err := a.store.SaveRevision(cfg, "test", "sqm"); err != nil {
		t.Fatal(err)
	}
	sqm := a.expect("GET", "/api/v1/system/status", nil, 200)["sqm"].([]any)
	if len(sqm) != 1 || sqm[0].(map[string]any)["interface"] != "wan1" || sqm[0].(map[string]any)["active"] != true || sqm[0].(map[string]any)["download_mbit"] != float64(450) {
		t.Fatalf("on: %v", sqm)
	}
}
