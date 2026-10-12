package logs

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

var qNow = time.Date(2026, 10, 11, 12, 0, 30, 0, time.UTC)

func seededStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(filepath.Join(t.TempDir(), "logs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	base := qNow.Unix() - qNow.Unix()%60 // 12:00:00
	for _, e := range []Event{
		{Time: base - 600, Kind: "traffic", Action: "deny", PolicyID: "1", DstIP: "10.0.0.1", Bytes: 100, Message: "old"},
		{Time: base - 90, Kind: "traffic", Action: "deny", PolicyID: "1", DstIP: "10.0.0.1", Bytes: 100},
		{Time: base - 80, Kind: "traffic", Action: "deny", PolicyID: "1", DstIP: "10.0.0.2", Bytes: 50},
		{Time: base - 30, Kind: "traffic", Action: "allow", PolicyID: "2", DstIP: "10.0.0.1", Bytes: 1000},
		{Time: base + 10, Kind: "ips", Action: "alert", DstIP: "10.0.0.3", Signature: "ET SCAN 100%_match", Message: "port scan"},
	} {
		e := e
		if err := s.Insert(&e); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestQueryGroupAndFilter(t *testing.T) {
	s := seededStore(t)
	res, err := s.Query(context.Background(), Query{GroupBy: []string{"dst_ip"}, Filters: map[string]string{"action": "deny"}, From: "-5m"}, qNow)
	if err != nil {
		t.Fatal(err)
	}
	want := Result{Columns: []string{"dst_ip", "count"}, Rows: [][]any{{"10.0.0.1", int64(1)}, {"10.0.0.2", int64(1)}}}
	if d := cmp.Diff(want, res); d != "" {
		t.Fatal(d)
	}

	res, err = s.Query(context.Background(), Query{Metric: "bytes", GroupBy: []string{"policy_id"}, From: "-1h", Limit: 1}, qNow)
	if err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff([][]any{{"2", int64(1000)}}, res.Rows); d != "" {
		t.Fatal(d)
	}
}

func TestQueryBucketsAndRollup(t *testing.T) {
	s := seededStore(t)
	base := qNow.Unix() - qNow.Unix()%60
	res, err := s.Query(context.Background(), Query{Bucket: "1m", From: "-5m"}, qNow)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]any{{base - 120, int64(2)}, {base - 60, int64(1)}, {base, int64(1)}}
	if d := cmp.Diff(want, res.Rows); d != "" || res.Columns[0] != "time" {
		t.Fatalf("%v %s", res.Columns, d)
	}

	// rollup of the minute before 12:00, twice: idempotent
	for range 2 {
		if err := s.RollupAt(base - 60); err != nil {
			t.Fatal(err)
		}
	}
	res, err = s.Query(context.Background(), Query{Source: "rollup", GroupBy: []string{"action"}, From: "-1h"}, qNow)
	if err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff([][]any{{"allow", int64(1)}}, res.Rows); d != "" {
		t.Fatal(d)
	}
}

func TestQueryRejects(t *testing.T) {
	for _, q := range []Query{
		{Source: "users"},
		{Metric: "packets"},
		{GroupBy: []string{"message"}},
		{GroupBy: []string{"dst_ip; DROP TABLE events"}},
		{GroupBy: []string{"kind", "kind"}},
		{Source: "rollup", GroupBy: []string{"src_ip"}},
		{Filters: map[string]string{"1=1 OR kind": "x"}},
		{Bucket: "2m"},
		{From: "yesterday"},
		{From: "now", To: "-1h"},
		{Limit: MaxQueryRows + 1},
	} {
		if _, _, _, err := q.Build(qNow); err == nil {
			t.Errorf("accepted %+v", q)
		}
	}
	// values go in as placeholders, never into the SQL text
	sql, args, _, err := Query{Filters: map[string]string{"dst_ip": "x' OR '1'='1"}}.Build(qNow)
	if err != nil || strings.Contains(sql, "OR '1'") || args[2] != "x' OR '1'='1" {
		t.Fatalf("%v %q %v", err, sql, args)
	}
}

func TestParseTime(t *testing.T) {
	for in, want := range map[string]int64{
		"now": qNow.Unix(), "": qNow.Unix(), "-15m": qNow.Unix() - 900, "-7d": qNow.Unix() - 7*86400,
		"2026-10-11T00:00:00Z": time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC).Unix(), "1700000000": 1700000000,
	} {
		if got, err := ParseTime(in, qNow); err != nil || got != want {
			t.Errorf("%q: %d %v", in, got, err)
		}
	}
}

func TestListEvents(t *testing.T) {
	s := seededStore(t)
	ctx := context.Background()
	page, next, err := s.ListEvents(ctx, EventFilter{Limit: 2})
	if err != nil || len(page) != 2 || next == 0 || page[0].Kind != "ips" || page[0].ID < page[1].ID {
		t.Fatalf("page 1: %v %d %+v", err, next, page)
	}
	rest, next2, err := s.ListEvents(ctx, EventFilter{Limit: 10, Before: next})
	if err != nil || len(rest) != 3 || next2 != 0 || rest[2].Message != "old" {
		t.Fatalf("page 2: %v %d %+v", err, next2, rest)
	}
	// LIKE wildcards in the search text are literal
	hits, _, _ := s.ListEvents(ctx, EventFilter{Text: "100%_"})
	none, _, _ := s.ListEvents(ctx, EventFilter{Text: "1%0"})
	if len(hits) != 1 || len(none) != 0 {
		t.Fatalf("text search: %d %d", len(hits), len(none))
	}
	kinds, _, _ := s.ListEvents(ctx, EventFilter{Kind: "traffic", From: qNow.Unix() - 200})
	if len(kinds) != 3 {
		t.Fatalf("kind/from filter: %d", len(kinds))
	}
	// exact field filters (AND), whitelisted names only
	exact, _, err := s.ListEvents(ctx, EventFilter{Fields: map[string]string{"dst_ip": "10.0.0.1", "action": "deny"}})
	if err != nil || len(exact) != 2 {
		t.Fatalf("field filter: %v %d", err, len(exact))
	}
	if _, _, err := s.ListEvents(ctx, EventFilter{Fields: map[string]string{"message = '' OR 1=1 --": "x"}}); err == nil {
		t.Fatal("accepted a field outside the whitelist")
	}
}
