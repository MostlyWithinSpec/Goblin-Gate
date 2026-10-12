package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/feeds"
)

func TestFeedEndpoints(t *testing.T) {
	a := newTestAPI(t)
	if out := a.expect("GET", "/api/v1/feeds", nil, 503); errCode(out) != "feeds_unavailable" {
		t.Fatalf("without manager: %v", out)
	}

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/broken" {
			w.WriteHeader(500)
			return
		}
		io.WriteString(w, "198.51.100.0/24\n")
	}))
	defer up.Close()
	m := &feeds.Manager{Dir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	m.SetFeeds([]config.Feed{
		{Name: "bad", Type: "ip", URL: up.URL, Enabled: true},
		{Name: "broken", Type: "ip", URL: up.URL + "/broken", Enabled: true},
	})
	a = newTestAPIWith(t, func(s *Server) { s.Feeds = m })

	st := a.expect("POST", "/api/v1/feeds/bad/refresh", nil, 200)
	if st["count"] != float64(1) {
		t.Fatalf("refresh: %v", st)
	}
	if out := a.expect("POST", "/api/v1/feeds/broken/refresh", nil, 502); errCode(out) != "refresh_failed" {
		t.Fatalf("broken: %v", out)
	}
	a.expect("POST", "/api/v1/feeds/nope/refresh", nil, 404)

	list := a.expect("GET", "/api/v1/feeds", nil, 200)
	if fs, _ := list["feeds"].([]any); len(fs) != 2 {
		t.Fatalf("list: %v", list)
	}
	out := a.expect("GET", "/api/v1/feeds/lookup?q=198.51.100.7", nil, 200)
	if hits, _ := out["feeds"].([]any); len(hits) != 1 || hits[0] != "bad" {
		t.Fatalf("lookup: %v", out)
	}
	a.expect("GET", "/api/v1/feeds/lookup?q=", nil, 400)
}
