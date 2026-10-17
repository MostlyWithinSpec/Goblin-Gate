package feeds

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// feedServer serves body with an ETag and counts requests; set body/status
// between calls.
type feedServer struct {
	*httptest.Server
	mu       sync.Mutex
	body     string
	status   int
	requests int
	lastReq  *http.Request
}

func newFeedServer(t *testing.T, body string) *feedServer {
	fs := &feedServer{body: body, status: 200}
	fs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.mu.Lock()
		defer fs.mu.Unlock()
		fs.requests++
		fs.lastReq = r.Clone(context.Background())
		etag := fmt.Sprintf(`"%x"`, len(fs.body)*31+strings.Count(fs.body, "."))
		if fs.status != 200 {
			w.WriteHeader(fs.status)
			return
		}
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		io.WriteString(w, fs.body)
	}))
	t.Cleanup(fs.Close)
	return fs
}

func (fs *feedServer) set(body string, status int) {
	fs.mu.Lock()
	fs.body, fs.status = body, status
	fs.mu.Unlock()
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newManager(t *testing.T, dir string) (*Manager, *clock, map[string][]string) {
	c := &clock{t: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)}
	updates := map[string][]string{}
	var mu sync.Mutex
	m := &Manager{
		Dir:    dir,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    c.now,
		OnUpdate: func(name string, e []string) {
			mu.Lock()
			updates[name] = e
			mu.Unlock()
		},
	}
	return m, c, updates
}

func ips(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "10.%d.%d.1\n", i/250, i%250)
	}
	return b.String()
}

func TestRefreshETagAndSchedule(t *testing.T) {
	srv := newFeedServer(t, "192.0.2.1\n192.0.2.9\n")
	dir := t.TempDir()
	m, clk, updates := newManager(t, dir)
	m.SetFeeds([]config.Feed{{Name: "bad-ips", Type: "ip", URL: srv.URL, Enabled: true, RefreshMinutes: 30, AuthHeader: "X-Api-Key: s3cret"}})

	m.RefreshDue(context.Background())
	if d := cmp.Diff([]string{"192.0.2.1", "192.0.2.9"}, updates["bad-ips"]); d != "" {
		t.Fatal(d)
	}
	if got := srv.lastReq.Header.Get("X-Api-Key"); got != "s3cret" {
		t.Fatalf("auth header %q", got)
	}
	st := m.Status()[0]
	if st.Count != 2 || st.ETag == "" || st.Error != "" || !st.NextRefresh.Equal(clk.t.Add(30*time.Minute)) {
		t.Fatalf("status %+v", st)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "bad-ips.txt")); string(b) != "192.0.2.1\n192.0.2.9\n" {
		t.Fatalf("saved copy %q", b)
	}

	// not due yet
	clk.t = clk.t.Add(10 * time.Minute)
	m.RefreshDue(context.Background())
	if srv.requests != 1 {
		t.Fatalf("requests %d, want 1", srv.requests)
	}

	// due: the server answers 304, nothing changes
	delete(updates, "bad-ips")
	clk.t = clk.t.Add(25 * time.Minute)
	m.RefreshDue(context.Background())
	if srv.requests != 2 || updates["bad-ips"] != nil {
		t.Fatalf("requests %d, update %v", srv.requests, updates["bad-ips"])
	}
	if st := m.Status()[0]; st.Count != 2 || !st.CheckedAt.Equal(clk.t) || st.FetchedAt.Equal(clk.t) {
		t.Fatalf("after 304: %+v", st)
	}
}

func TestSafetyGuardsKeepLastGood(t *testing.T) {
	srv := newFeedServer(t, ips(100))
	m, _, updates := newManager(t, t.TempDir())
	m.SetFeeds([]config.Feed{{Name: "f", Type: "ip", URL: srv.URL, Enabled: true}})
	ctx := context.Background()
	if _, err := m.Refresh(ctx, "f"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body string
		status     int
		want       string
	}{
		{"empty", "# nothing here\nnot-an-ip\n", 200, "no valid entries"},
		{"hosts file", "# ads\n0.0.0.0 ads.example.com\n127.0.0.1 tracker.example.net\n", 200, "2 domain names: this is a domain list"},
		{"shrinks", ips(9), 200, "more than 90% fewer"},
		{"grows", ips(1001), 200, "more than 10x"},
		{"http error", "", 503, "HTTP 503"},
	} {
		srv.set(tc.body, tc.status)
		st, err := m.Refresh(ctx, "f")
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(st.Error, tc.want) {
			t.Fatalf("%s: err %v, status %+v", tc.name, err, st)
		}
		if st.Count != 100 || len(m.Entries("f")) != 100 || len(updates["f"]) != 100 {
			t.Fatalf("%s: last good copy not kept: %+v", tc.name, st)
		}
	}
	// within limits again: accepted, error cleared
	srv.set(ips(20), 200)
	if st, err := m.Refresh(ctx, "f"); err != nil || st.Count != 20 || st.Error != "" {
		t.Fatalf("recovery: %v %+v", err, st)
	}
}

func TestDownloadLimit(t *testing.T) {
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := strings.Repeat("1.2.3.4\n", 1<<16)
		for written := 0; written <= MaxDownload; written += len(chunk) {
			if _, err := io.WriteString(w, chunk); err != nil {
				return
			}
		}
	}))
	defer big.Close()
	m, _, _ := newManager(t, t.TempDir())
	m.SetFeeds([]config.Feed{{Name: "big", Type: "ip", URL: big.URL, Enabled: true}})
	if _, err := m.Refresh(context.Background(), "big"); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("err %v", err)
	}
}

func TestLastGoodFallbackAfterRestart(t *testing.T) {
	srv := newFeedServer(t, "evil.example\nads.example.net\n")
	dir := t.TempDir()
	feeds := []config.Feed{{Name: "doms", Type: "domain", URL: srv.URL, Enabled: true}}
	m, _, _ := newManager(t, dir)
	m.SetFeeds(feeds)
	m.RefreshDue(context.Background())

	// a new manager (daemon restart) loads the copy, reports it via OnUpdate
	// and does not download again until it is due
	srv.set("", 500)
	m2, clk, updates := newManager(t, dir)
	m2.SetFeeds(feeds)
	if d := cmp.Diff([]string{"ads.example.net", "evil.example"}, updates["doms"]); d != "" {
		t.Fatalf("loaded: %s", d)
	}
	m2.RefreshDue(context.Background())
	if srv.requests != 1 {
		t.Fatalf("restart re-downloaded a fresh copy (%d requests)", srv.requests)
	}
	clk.t = clk.t.Add(2 * time.Hour)
	m2.RefreshDue(context.Background())
	st := m2.Status()[0]
	if st.Error == "" || st.Count != 2 || len(m2.Lookup("x.evil.example")) != 1 {
		t.Fatalf("after failed refresh: %+v", st)
	}
	// a URL change downloads again right away and ignores the old ETag
	srv.set("new.example\n", 200)
	feeds[0].URL = srv.URL + "/v2"
	m2.SetFeeds(feeds)
	m2.RefreshDue(context.Background())
	if srv.lastReq.Header.Get("If-None-Match") != "" || len(m2.Lookup("new.example")) != 1 || len(m2.Lookup("evil.example")) != 0 {
		t.Fatalf("after url change: %+v", m2.Status()[0])
	}
}

func TestLookupAndDisabled(t *testing.T) {
	ipSrv := newFeedServer(t, "198.51.100.0/24\n203.0.113.7\n")
	domSrv := newFeedServer(t, "evil.example\n")
	urlSrv := newFeedServer(t, "https://phish.example/login\n")
	m, _, _ := newManager(t, t.TempDir())
	m.SetFeeds([]config.Feed{
		{Name: "a-ips", Type: "ip", URL: ipSrv.URL, Enabled: true},
		{Name: "b-ips", Type: "ip", URL: ipSrv.URL, Enabled: true},
		{Name: "doms", Type: "domain", URL: domSrv.URL, Enabled: true},
		{Name: "urls", Type: "url", URL: urlSrv.URL, Enabled: true},
		{Name: "off", Type: "ip", URL: ipSrv.URL, Enabled: false},
	})
	m.RefreshDue(context.Background())
	for q, want := range map[string][]string{
		"198.51.100.99":                 {"a-ips", "b-ips"},
		"203.0.113.8":                   {},
		"sub.EVIL.example":              {"doms"},
		"example":                       {},
		"https://PHISH.example/login":   {"urls"},
		"https://phish.example/login?x": {},
	} {
		if d := cmp.Diff(want, m.Lookup(q)); d != "" {
			t.Errorf("Lookup(%q): %s", q, d)
		}
	}
	for _, st := range m.Status() {
		if st.Name == "off" && (st.Count != 0 || !st.NextRefresh.IsZero()) {
			t.Fatalf("disabled feed was fetched: %+v", st)
		}
	}
	// manual refresh works for a disabled feed; unknown names are reported
	if st, err := m.Refresh(context.Background(), "off"); err != nil || st.Count != 2 {
		t.Fatalf("manual refresh: %v %+v", err, st)
	}
	if _, err := m.Refresh(context.Background(), "nope"); err != ErrUnknownFeed {
		t.Fatalf("unknown: %v", err)
	}
}

func TestRefreshInterval(t *testing.T) {
	for in, want := range map[int]time.Duration{0: time.Hour, 5: 15 * time.Minute, 120: 2 * time.Hour} {
		if got := RefreshInterval(config.Feed{RefreshMinutes: in}); got != want {
			t.Errorf("%d: %v", in, got)
		}
	}
}
