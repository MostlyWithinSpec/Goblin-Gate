package ddns

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestCloudflareCreate(t *testing.T) {
	var createCalled atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/dns_records"):
			// List records: return empty (no existing record).
			if r.Header.Get("Authorization") != "Bearer cf-token-123" {
				t.Errorf("missing or wrong auth header: %q", r.Header.Get("Authorization"))
			}
			json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []any{}})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/dns_records"):
			createCalled.Store(true)
			body, _ := io.ReadAll(r.Body)
			var rec map[string]any
			json.Unmarshal(body, &rec)
			if rec["type"] != "A" || rec["name"] != "host.example.net" || rec["content"] != "1.2.3.4" {
				t.Errorf("create payload: %v", rec)
			}
			if rec["ttl"] != float64(300) || rec["proxied"] != false {
				t.Errorf("create ttl/proxied: %v", rec)
			}
			json.NewEncoder(w).Encode(map[string]any{"success": true})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()

	u := &Updater{Client: srv.Client(), CloudflareBase: srv.URL, Logger: testLogger()}
	entry := config.DDNS{Name: "home", Provider: "cloudflare", Hostname: "host.example.net", Token: "cf-token-123", ZoneID: "zone456"}
	ip := netip.MustParseAddr("1.2.3.4")
	if err := u.Update(context.Background(), entry, ip); err != nil {
		t.Fatal(err)
	}
	if !createCalled.Load() {
		t.Error("expected POST (create) to be called")
	}
}

func TestCloudflareUpdate(t *testing.T) {
	var updateCalled atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/dns_records"):
			// Return an existing record.
			json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"result":  []map[string]any{{"id": "rec-1", "name": "host.example.net", "content": "9.9.9.9"}},
			})
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/dns_records/rec-1"):
			updateCalled.Store(true)
			body, _ := io.ReadAll(r.Body)
			var rec map[string]any
			json.Unmarshal(body, &rec)
			if rec["content"] != "5.6.7.8" {
				t.Errorf("update content: %v", rec)
			}
			json.NewEncoder(w).Encode(map[string]any{"success": true})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()

	u := &Updater{Client: srv.Client(), CloudflareBase: srv.URL, Logger: testLogger()}
	entry := config.DDNS{Name: "home", Provider: "cloudflare", Hostname: "host.example.net", Token: "cf-token-123", ZoneID: "zone456"}
	ip := netip.MustParseAddr("5.6.7.8")
	if err := u.Update(context.Background(), entry, ip); err != nil {
		t.Fatal(err)
	}
	if !updateCalled.Load() {
		t.Error("expected PUT (update) to be called")
	}
}

func TestCloudflareFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []map[string]any{{"code": 1001, "message": "Invalid token"}}})
	}))
	defer srv.Close()

	u := &Updater{Client: srv.Client(), CloudflareBase: srv.URL, Logger: testLogger()}
	entry := config.DDNS{Name: "home", Provider: "cloudflare", Hostname: "host.example.net", Token: "bad", ZoneID: "z"}
	ip := netip.MustParseAddr("1.2.3.4")
	err := u.Update(context.Background(), entry, ip)
	if err == nil || !strings.Contains(err.Error(), "success=false") {
		t.Fatalf("expected success=false error, got: %v", err)
	}
}

func TestDuckDNS(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Encode()
		io.WriteString(w, "OK")
	}))
	defer srv.Close()

	u := &Updater{Client: srv.Client(), DuckDNSBase: srv.URL, Logger: testLogger()}
	entry := config.DDNS{Name: "home", Provider: "duckdns", Hostname: "myhost.duckdns.org", Token: "dd-token-789"}
	ip := netip.MustParseAddr("10.0.0.1")
	if err := u.Update(context.Background(), entry, ip); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotQuery, "domains=myhost") || !strings.Contains(gotQuery, "token=dd-token-789") || !strings.Contains(gotQuery, "ip=10.0.0.1") {
		t.Errorf("query: %s", gotQuery)
	}
}

func TestDuckDNSFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ERROR: bad token")
	}))
	defer srv.Close()

	u := &Updater{Client: srv.Client(), DuckDNSBase: srv.URL, Logger: testLogger()}
	entry := config.DDNS{Name: "home", Provider: "duckdns", Hostname: "myhost.duckdns.org", Token: "bad"}
	ip := netip.MustParseAddr("10.0.0.1")
	err := u.Update(context.Background(), entry, ip)
	if err == nil || !strings.Contains(err.Error(), "unexpected response") {
		t.Fatalf("expected unexpected response error, got: %v", err)
	}
}

func TestCustom(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		w.WriteHeader(200)
	}))
	defer srv.Close()

	u := &Updater{Client: srv.Client(), Logger: testLogger()}
	entry := config.DDNS{Name: "home", Provider: "custom", Hostname: "host.example.net", URL: srv.URL + "/update?ip={ip}&host={hostname}"}
	ip := netip.MustParseAddr("2.3.4.5")
	if err := u.Update(context.Background(), entry, ip); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotPath, "ip=2.3.4.5") || !strings.Contains(gotPath, "host=host.example.net") {
		t.Errorf("path: %s", gotPath)
	}
}

func TestCustomFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()

	u := &Updater{Client: srv.Client(), Logger: testLogger()}
	entry := config.DDNS{Name: "home", Provider: "custom", Hostname: "h.example.net", URL: srv.URL + "/x?ip={ip}"}
	ip := netip.MustParseAddr("2.3.4.5")
	err := u.Update(context.Background(), entry, ip)
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("expected HTTP 503 error, got: %v", err)
	}
}

func TestUnknownProvider(t *testing.T) {
	u := &Updater{Logger: testLogger()}
	entry := config.DDNS{Name: "x", Provider: "nope"}
	ip := netip.MustParseAddr("1.1.1.1")
	err := u.Update(context.Background(), entry, ip)
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("got: %v", err)
	}
}
