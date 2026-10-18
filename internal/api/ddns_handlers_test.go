package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/ddns"
)

func TestDDNSEndpoints(t *testing.T) {
	a := newTestAPI(t)
	if out := a.expect("GET", "/api/v1/ddns", nil, 503); errCode(out) != "ddns_unavailable" {
		t.Fatalf("without manager: %v", out)
	}

	ipSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "203.0.113.5\n")
	}))
	defer ipSrv.Close()

	dnsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/broken" {
			io.WriteString(w, "ERROR: bad token")
			return
		}
		io.WriteString(w, "OK")
	}))
	defer dnsSrv.Close()

	m := &ddns.Manager{
		Updater: &ddns.Updater{Client: ipSrv.Client(), IPCheckURL: ipSrv.URL, DuckDNSBase: dnsSrv.URL, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))},
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	a = newTestAPIWith(t, func(s *Server) { s.DDNS = m })

	// The handlers sync the manager from the running config on each call, so
	// commit a config that contains the DDNS entry first.
	cfg := config.Default()
	cfg.DDNS = []config.DDNS{
		{Name: "home", Enabled: true, Provider: "duckdns", Hostname: "myhost.duckdns.org", Token: "tok"},
	}
	a.expect("PUT", "/api/v1/config/candidate", cfg, 200)
	a.expect("POST", "/api/v1/commit", nil, 200)

	list := a.expect("GET", "/api/v1/ddns", nil, 200)
	entries, _ := list["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries: %v", list)
	}
	e0, _ := entries[0].(map[string]any)
	if e0["name"] != "home" || e0["hostname"] != "myhost.duckdns.org" {
		t.Fatalf("entry: %v", e0)
	}

	st := a.expect("POST", "/api/v1/ddns/home/update", nil, 200)
	if st["name"] != "home" {
		t.Fatalf("update status: %v", st)
	}
	if ip, _ := st["ip"].(string); ip != "203.0.113.5" {
		t.Fatalf("update ip = %q, want 203.0.113.5: %v", ip, st)
	}

	a.expect("POST", "/api/v1/ddns/nope/update", nil, 404)
}
