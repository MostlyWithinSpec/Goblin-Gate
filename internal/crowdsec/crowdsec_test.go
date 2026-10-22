package crowdsec

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// fakeLAPI answers the decisions stream: the startup list on
// startup=true, then queued deltas.
type fakeLAPI struct {
	mu      sync.Mutex
	key     string
	startup []map[string]string
	deltas  []struct{ New, Deleted []map[string]string }
	calls   []string
}

func (f *fakeLAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.URL.RequestURI())
	if r.URL.Path != "/v1/decisions/stream" || r.Header.Get("X-Api-Key") != f.key {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	out := map[string]any{"new": nil, "deleted": nil}
	switch {
	case r.URL.Query().Get("startup") == "true":
		out["new"] = f.startup
	case len(f.deltas) > 0:
		out["new"], out["deleted"] = f.deltas[0].New, f.deltas[0].Deleted
		f.deltas = f.deltas[1:]
	}
	json.NewEncoder(w).Encode(out)
}

func ban(scope, value, dur string) map[string]string {
	return map[string]string{"type": "ban", "scope": scope, "value": value, "duration": dur, "origin": "crowdsec"}
}

type nftLog struct {
	mu      sync.Mutex
	scripts []string
	fail    func(string) bool
}

func (n *nftLog) run(_ context.Context, s []byte) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.fail != nil && n.fail(string(s)) {
		return context.DeadlineExceeded
	}
	n.scripts = append(n.scripts, string(s))
	return nil
}

func (n *nftLog) all() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return strings.Join(n.scripts, "")
}

func TestPull(t *testing.T) {
	lapi := &fakeLAPI{key: "bouncer-secret", startup: []map[string]string{
		ban("Ip", "203.0.113.9", "4h"),
		ban("Range", "198.51.100.0/24", "1h"),
		ban("Ip", "2001:db8::66", "30m"),
		{"type": "captcha", "scope": "Ip", "value": "203.0.113.10", "duration": "1h"}, // not a ban
		ban("Country", "NZ", "1h"),                                                    // not an address
	}}
	srv := httptest.NewServer(lapi)
	defer srv.Close()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	nl := &nftLog{}
	cfg := config.CrowdSec{Enabled: true, URL: srv.URL, APIKey: "bouncer-secret"}
	m := &Manager{Config: func() (config.CrowdSec, error) { return cfg, nil }, RunNFT: nl.run, Now: func() time.Time { return now }}
	ctx := context.Background()

	m.Pull(ctx, cfg)
	got := nl.all()
	for _, want := range []string{
		"add element inet goblin crowdsec4 { 203.0.113.9 timeout 14400s }",
		"add element inet goblin crowdsec_range4 { 198.51.100.0/24 timeout 3600s }",
		"add element inet goblin crowdsec6 { 2001:db8::66 timeout 1800s }",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "203.0.113.10") || strings.Contains(got, "NZ") {
		t.Fatalf("non-ban decisions applied:\n%s", got)
	}
	if st := m.Status(ctx); !st.Connected || st.Active != 3 || st.LastError != "" {
		t.Fatalf("status: %+v", st)
	}

	// a delta: one new ban, one lifted
	lapi.deltas = append(lapi.deltas, struct{ New, Deleted []map[string]string }{
		New:     []map[string]string{ban("Ip", "192.0.2.77", "10m")},
		Deleted: []map[string]string{ban("Ip", "203.0.113.9", "0s")},
	})
	nl.scripts = nil
	m.Pull(ctx, cfg)
	got = nl.all()
	if !strings.Contains(got, "192.0.2.77 timeout 600s") || !strings.Contains(got, "delete element inet goblin crowdsec4 { 203.0.113.9 }") {
		t.Fatalf("delta:\n%s", got)
	}
	if !strings.Contains(lapi.calls[0], "startup=true") || strings.Contains(lapi.calls[1], "startup") {
		t.Fatalf("calls: %v", lapi.calls)
	}

	// a commit emptied the sets: Resync puts back the live bans with the time left
	now = now.Add(20 * time.Minute) // the /128 (30m) has 10m left, 192.0.2.77 (10m) is gone
	nl.scripts = nil
	m.Resync(ctx)
	got = nl.all()
	if !strings.Contains(got, "2001:db8::66 timeout 600s") || strings.Contains(got, "192.0.2.77") {
		t.Fatalf("resync:\n%s", got)
	}

	// turning it off removes every ban
	nl.scripts = nil
	m.Pull(ctx, config.CrowdSec{})
	if got = nl.all(); !strings.Contains(got, "delete element inet goblin crowdsec_range4 { 198.51.100.0/24 }") {
		t.Fatalf("disable:\n%s", got)
	}
	if st := m.Status(ctx); st.Active != 0 || st.Connected {
		t.Fatalf("status after disable: %+v", st)
	}
}

func TestPullErrorsHideKey(t *testing.T) {
	lapi := &fakeLAPI{key: "right-key"}
	srv := httptest.NewServer(lapi)
	defer srv.Close()
	cfg := config.CrowdSec{Enabled: true, URL: srv.URL, APIKey: "wrong-key-secret"}
	m := &Manager{Config: func() (config.CrowdSec, error) { return cfg, nil }, RunNFT: (&nftLog{}).run}
	m.Pull(context.Background(), cfg)
	st := m.Status(context.Background())
	if st.Connected || !strings.Contains(st.LastError, "refused the bouncer key") || strings.Contains(st.LastError, "secret") {
		t.Fatalf("status: %+v", st)
	}
	// unreachable: the error names neither the key nor a query
	cfg.URL = "http://127.0.0.1:1"
	m.Pull(context.Background(), cfg)
	if st = m.Status(context.Background()); !strings.Contains(st.LastError, "unreachable") || strings.Contains(st.LastError, "secret") || strings.Contains(st.LastError, "startup") {
		t.Fatalf("status: %+v", st)
	}
}

// One bad element (an overlapping range, say) must not stop the others.
func TestApplyFallsBackPerElement(t *testing.T) {
	lapi := &fakeLAPI{key: "k", startup: []map[string]string{ban("Ip", "203.0.113.1", "1h"), ban("Ip", "203.0.113.2", "1h")}}
	srv := httptest.NewServer(lapi)
	defer srv.Close()
	nl := &nftLog{fail: func(s string) bool { return strings.Contains(s, "203.0.113.1") }}
	cfg := config.CrowdSec{Enabled: true, URL: srv.URL, APIKey: "k"}
	m := &Manager{Config: func() (config.CrowdSec, error) { return cfg, nil }, RunNFT: nl.run}
	m.Pull(context.Background(), cfg)
	if got := nl.all(); !strings.Contains(got, "203.0.113.2 timeout") {
		t.Fatalf("good element not applied:\n%s", got)
	}
}
