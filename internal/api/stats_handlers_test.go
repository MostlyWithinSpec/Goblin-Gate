package api

import (
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/logs"
	"github.com/networkgoblinlabs/goblin-gate/internal/stats"
)

func TestStatsEndpointsUnavailable(t *testing.T) {
	a := newTestAPI(t)
	a.expect("POST", "/api/v1/stats/query", map[string]any{}, 503)
	a.expect("GET", "/api/v1/logs", nil, 503)
	a.expect("GET", "/api/v1/stats/policies", nil, 503)
	a.expect("GET", "/api/v1/stats/system", nil, 503)
}

func TestStatsEndpoints(t *testing.T) {
	store, err := logs.NewStore(filepath.Join(t.TempDir(), "logs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().Unix()
	for i, e := range []logs.Event{
		{Time: now - 120, Kind: "traffic", Action: "deny", DstIP: "10.0.0.1", Message: "a"},
		{Time: now - 60, Kind: "traffic", Action: "deny", DstIP: "10.0.0.1", Message: "b"},
		{Time: now - 30, Kind: "ips", Action: "alert", DstIP: "10.0.0.2", Message: "scan"},
	} {
		e := e
		if err := store.Insert(&e); err != nil {
			t.Fatal(i, err)
		}
	}
	counters := &stats.FakeCounters{}
	counters.Set(1, stats.Counter{Packets: 3, Bytes: 180})
	a := newTestAPIWith(t, func(s *Server) {
		s.Logs = store
		s.Policies = &stats.PolicyTracker{Source: counters}
		s.System = &stats.SystemSampler{}
	})

	out := a.expect("POST", "/api/v1/stats/query", map[string]any{
		"metric": "count", "group_by": []string{"dst_ip"}, "filters": map[string]string{"kind": "traffic"}, "from": "-1h", "to": "now", "bucket": "none", "limit": 10,
	}, 200)
	rows, _ := out["rows"].([]any)
	if len(rows) != 1 || rows[0].([]any)[0] != "10.0.0.1" || rows[0].([]any)[1] != float64(2) {
		t.Fatalf("query: %v", out)
	}
	if out := a.expect("POST", "/api/v1/stats/query", map[string]any{"group_by": []string{"password"}}, 400); errCode(out) != "invalid_query" {
		t.Fatalf("bad field: %v", out)
	}
	a.expect("POST", "/api/v1/stats/query", `{"metric":"count","sql":"x"}`, 400)

	page := a.expect("GET", "/api/v1/logs?limit=2", nil, 200)
	evs, _ := page["events"].([]any)
	if len(evs) != 2 || evs[0].(map[string]any)["kind"] != "ips" || page["next_cursor"] == float64(0) {
		t.Fatalf("logs page: %v", page)
	}
	cursor := strconv.FormatInt(int64(page["next_cursor"].(float64)), 10)
	page = a.expect("GET", "/api/v1/logs?limit=2&cursor="+cursor, nil, 200)
	if evs, _ := page["events"].([]any); len(evs) != 1 || page["next_cursor"] != float64(0) {
		t.Fatalf("logs page 2: %v", page)
	}
	if evs, _ := a.expect("GET", "/api/v1/logs?kind=traffic&q=b&from=-1h", nil, 200)["events"].([]any); len(evs) != 1 {
		t.Fatalf("filtered: %v", evs)
	}
	a.expect("GET", "/api/v1/logs?from=yesterday", nil, 400)
	a.expect("GET", "/api/v1/logs?limit=0", nil, 400)

	pol := a.expect("GET", "/api/v1/stats/policies", nil, 200)
	ps, _ := pol["policies"].([]any)
	if len(ps) != 1 || ps[0].(map[string]any)["packets"] != float64(3) {
		t.Fatalf("policies: %v", pol)
	}
	counters.Err = errors.New("nft: no such table")
	a.expect("GET", "/api/v1/stats/policies", nil, 503)

	a.expect("GET", "/api/v1/stats/feeds", nil, 503)

	sys := a.expect("GET", "/api/v1/stats/system", nil, 200)
	if _, ok := sys["cpu_percent"]; !ok {
		t.Fatalf("system: %v", sys)
	}
}

func TestFeedStats(t *testing.T) {
	counters := &stats.FakeCounters{Feeds: map[string]stats.Counter{"feedhit_bad_ips": {Packets: 12, Bytes: 720}}}
	a := newTestAPIWith(t, func(s *Server) { s.FeedHits = counters })
	cfg, _ := a.store.Running()
	cfg.Feeds = []config.Feed{
		{Name: "bad-ips", Type: "ip", URL: "https://example.com/a", Enabled: true},
		{Name: "quiet", Type: "ip", URL: "https://example.com/b", Enabled: true},
		{Name: "doms", Type: "domain", URL: "https://example.com/c", Enabled: true},
	}
	if _, err := a.store.SaveRevision(cfg, "test", "feeds"); err != nil {
		t.Fatal(err)
	}
	out := a.expect("GET", "/api/v1/stats/feeds", nil, 200)
	fs, _ := out["feeds"].([]any)
	if len(fs) != 2 || fs[0].(map[string]any)["name"] != "bad-ips" || fs[0].(map[string]any)["packets"] != float64(12) || fs[1].(map[string]any)["packets"] != float64(0) {
		t.Fatalf("feeds: %v", out)
	}
}
