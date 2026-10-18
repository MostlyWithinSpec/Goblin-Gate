package ddns

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func TestNoUpdateWhenUnchanged(t *testing.T) {
	// IP check server: returns a fixed public IP.
	ipSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "8.8.8.8")
	}))
	defer ipSrv.Close()

	// Provider server: counts calls.
	var providerCalls atomic.Int32
	providerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		io.WriteString(w, "OK")
	}))
	defer providerSrv.Close()

	fixedNow := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	m := &Manager{
		Updater: &Updater{
			Client:      ipSrv.Client(),
			IPCheckURL:  ipSrv.URL,
			DuckDNSBase: providerSrv.URL,
			Logger:      testLogger(),
		},
		Logger: testLogger(),
		Now:    func() time.Time { return fixedNow },
	}

	entry := config.DDNS{
		Name:     "home",
		Enabled:  true,
		Provider: "duckdns",
		Hostname: "myhost.duckdns.org",
		Token:    "tok",
	}
	m.Sync([]config.DDNS{entry})

	// First update: should call the provider.
	if err := m.doUpdate(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	if got := providerCalls.Load(); got != 1 {
		t.Fatalf("expected 1 provider call after first update, got %d", got)
	}

	// Second update with same IP: should NOT call the provider.
	if err := m.doUpdate(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	if got := providerCalls.Load(); got != 1 {
		t.Fatalf("expected still 1 provider call (IP unchanged), got %d", got)
	}

	// Verify status shows the IP.
	sts := m.Status()
	if len(sts) != 1 || sts[0].IP != "8.8.8.8" {
		t.Fatalf("unexpected status: %+v", sts)
	}
}

func TestIPCheckFallback(t *testing.T) {
	// IP check server returns a valid IPv4.
	ipSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "  192.168.1.1\n")
	}))
	defer ipSrv.Close()

	m := &Manager{
		Updater: &Updater{
			Client:     ipSrv.Client(),
			IPCheckURL: ipSrv.URL,
			Logger:     testLogger(),
		},
		Logger: testLogger(),
	}

	ip, err := m.resolveIP(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := netip.MustParseAddr("192.168.1.1")
	if ip != want {
		t.Fatalf("got %v, want %v", ip, want)
	}
}

func TestIPCheckBadBody(t *testing.T) {
	ipSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "not-an-ip")
	}))
	defer ipSrv.Close()

	m := &Manager{
		Updater: &Updater{
			Client:     ipSrv.Client(),
			IPCheckURL: ipSrv.URL,
			Logger:     testLogger(),
		},
		Logger: testLogger(),
	}

	_, err := m.resolveIP(context.Background())
	if err == nil {
		t.Fatal("expected error for non-IP body")
	}
}

func TestUpdateNowUnknownEntry(t *testing.T) {
	m := &Manager{
		Updater: &Updater{Logger: testLogger()},
		Logger:  testLogger(),
	}
	_, err := m.UpdateNow(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown entry")
	}
}

func TestStatusSorted(t *testing.T) {
	m := &Manager{
		Updater: &Updater{Logger: testLogger()},
		Logger:  testLogger(),
	}
	m.Sync([]config.DDNS{
		{Name: "zeta", Provider: "duckdns", Hostname: "z.duckdns.org"},
		{Name: "alpha", Provider: "duckdns", Hostname: "a.duckdns.org"},
	})
	sts := m.Status()
	if len(sts) != 2 || sts[0].Name != "alpha" || sts[1].Name != "zeta" {
		t.Fatalf("unexpected order: %+v", sts)
	}
}

// A tick pushes again when the public IP changes, retries a failed push on
// the next tick, and pushes again after the entry's config changed.
func TestTickFollowsIPAndRetries(t *testing.T) {
	var ip atomic.Value
	ip.Store("198.51.100.1")
	ipSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, ip.Load().(string))
	}))
	defer ipSrv.Close()
	var calls atomic.Int32
	var fail atomic.Bool
	providerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail.Load() {
			io.WriteString(w, "KO")
			return
		}
		io.WriteString(w, "OK")
	}))
	defer providerSrv.Close()
	m := &Manager{
		Updater: &Updater{Client: ipSrv.Client(), IPCheckURL: ipSrv.URL, DuckDNSBase: providerSrv.URL, Logger: testLogger()},
		Logger:  testLogger(),
	}
	list := []config.DDNS{{Name: "home", Enabled: true, Provider: "duckdns", Hostname: "h.duckdns.org", Token: "tok"}}
	ctx := context.Background()

	m.tick(ctx, list)
	m.tick(ctx, list)
	if calls.Load() != 1 {
		t.Fatalf("same IP: %d pushes, want 1", calls.Load())
	}
	ip.Store("198.51.100.2")
	fail.Store(true)
	m.tick(ctx, list)
	if calls.Load() != 2 || m.Status()[0].LastError == "" || m.Status()[0].IP != "198.51.100.1" {
		t.Fatalf("new IP, failing: %d pushes, %+v", calls.Load(), m.Status())
	}
	fail.Store(false)
	m.tick(ctx, list)
	if st := m.Status()[0]; calls.Load() != 3 || st.LastError != "" || st.IP != "198.51.100.2" {
		t.Fatalf("retry: %d pushes, %+v", calls.Load(), st)
	}
	list[0].Token = "new"
	m.tick(ctx, list)
	if calls.Load() != 4 {
		t.Fatalf("new token: %d pushes, want 4", calls.Load())
	}
}

// A transport error must not quote the update URL: it carries the token and
// the status is shown to read-only users.
func TestErrorHidesToken(t *testing.T) {
	ipSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "198.51.100.1")
	}))
	defer ipSrv.Close()
	m := &Manager{
		Updater: &Updater{Client: ipSrv.Client(), IPCheckURL: ipSrv.URL, DuckDNSBase: "http://127.0.0.1:1", Logger: testLogger()},
		Logger:  testLogger(),
	}
	m.Sync([]config.DDNS{
		{Name: "duck", Enabled: true, Provider: "duckdns", Hostname: "h.duckdns.org", Token: "duck-secret"},
		{Name: "custom", Enabled: true, Provider: "custom", Hostname: "h.example.net", URL: "https://127.0.0.1:1/u?key=custom-secret&ip={ip}"},
	})
	for _, name := range []string{"duck", "custom"} {
		st, err := m.UpdateNow(context.Background(), name)
		if err == nil {
			t.Fatalf("%s: no error", name)
		}
		if strings.Contains(st.LastError, "secret") || strings.Contains(err.Error(), "secret") {
			t.Fatalf("%s: error leaks the token: %s", name, st.LastError)
		}
		if st.LastAttempt.IsZero() || !st.LastUpdate.IsZero() {
			t.Fatalf("%s: a failure is an attempt, not an update: %+v", name, st)
		}
	}
}
