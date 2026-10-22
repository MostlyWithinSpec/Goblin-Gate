package stats

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestParseNFTCounters(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "counters.json"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseNFTCounters(data)
	if err != nil {
		t.Fatal(err)
	}
	want := map[uint32]Counter{1: {Packets: 1200, Bytes: 950000}, 7: {}}
	if d := cmp.Diff(want, got); d != "" {
		t.Fatal(d)
	}
	feeds, err := ParseNFTFeedCounters(data)
	if err != nil || len(feeds) != 1 || feeds["feedhit_spamhaus_drop"].Packets != 77 {
		t.Fatalf("feed counters: %v %v", feeds, err)
	}
	if _, err := ParseNFTCounters([]byte("not json")); err == nil {
		t.Fatal("bad json accepted")
	}
	n := NFTCounters{Run: func(context.Context) ([]byte, error) { return data, nil }}
	if c, err := n.PolicyCounters(context.Background()); err != nil || c[1].Packets != 1200 {
		t.Fatalf("%v %v", c, err)
	}
}

func TestPolicyTrackerLastHit(t *testing.T) {
	src := &FakeCounters{}
	src.Set(1, Counter{Packets: 10, Bytes: 1000})
	src.Set(2, Counter{Packets: 5, Bytes: 500})
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	tr := &PolicyTracker{Source: src, Now: func() time.Time { return now }}
	ctx := context.Background()

	first, _ := tr.Read(ctx)
	if !first[0].LastHit.IsZero() || !first[1].LastHit.IsZero() {
		t.Fatalf("first read has last hits: %+v", first)
	}
	now = now.Add(time.Minute)
	src.Set(1, Counter{Packets: 11, Bytes: 1100})
	got, _ := tr.Read(ctx)
	if !got[0].LastHit.Equal(now) || !got[1].LastHit.IsZero() {
		t.Fatalf("after a hit: %+v", got)
	}
	hit1 := now

	// a commit reloads the ruleset: counters restart
	now = now.Add(time.Minute)
	src.Set(1, Counter{})
	src.Set(2, Counter{Packets: 1, Bytes: 60})
	got, _ = tr.Read(ctx)
	if !got[0].LastHit.Equal(hit1) || !got[1].LastHit.Equal(now) {
		t.Fatalf("after reset: %+v", got)
	}

	src.Err = errors.New("nft missing")
	if _, err := tr.Read(ctx); err == nil {
		t.Fatal("source error swallowed")
	}
}

func TestSystemSampler(t *testing.T) {
	files := map[string]string{
		"/proc/stat":    "cpu  100 0 100 700 100 0 0 0 0 0\ncpu0 1 2 3 4\n",
		"/proc/meminfo": "MemTotal:        2000 kB\nMemFree:          100 kB\nMemAvailable:     500 kB\n",
		"/proc/uptime":  "3600.52 7000.00\n",
		"/proc/sys/net/netfilter/nf_conntrack_count": "42\n",
	}
	s := &SystemSampler{ReadFile: func(p string) ([]byte, error) {
		if v, ok := files[p]; ok {
			return []byte(v), nil
		}
		return nil, os.ErrNotExist
	}}
	got := s.Sample()
	want := System{MemTotalBytes: 2000 * 1024, MemUsedBytes: 1500 * 1024, UptimeSeconds: 3600, Sessions: 42}
	if d := cmp.Diff(want, got); d != "" {
		t.Fatal(d)
	}
	// 200 more busy jiffies and 200 idle: 50%
	files["/proc/stat"] = "cpu  200 0 200 900 100 0 0 0 0 0\n"
	if got := s.Sample(); got.CPUPercent != 50 {
		t.Fatalf("cpu %v", got.CPUPercent)
	}
	empty := (&SystemSampler{ReadFile: func(string) ([]byte, error) { return nil, os.ErrNotExist }}).Sample()
	if empty != (System{}) {
		t.Fatalf("no /proc: %+v", empty)
	}
}

func TestCustomCounters(t *testing.T) {
	n := NFTCounters{
		Run: func(context.Context) ([]byte, error) {
			return []byte(`{"nftables":[{"counter":{"name":"custom_a","table":"goblin","packets":3,"bytes":90}},{"counter":{"name":"policy_1","table":"goblin","packets":9,"bytes":9}}]}`), nil
		},
		RunNAT: func(context.Context) ([]byte, error) {
			return []byte(`{"nftables":[{"counter":{"name":"custom_b","table":"goblin_nat","packets":1,"bytes":60}}]}`), nil
		},
	}
	got, err := n.CustomCounters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["custom_a"].Packets != 3 || got["custom_b"].Bytes != 60 {
		t.Fatalf("counters: %v", got)
	}
}
