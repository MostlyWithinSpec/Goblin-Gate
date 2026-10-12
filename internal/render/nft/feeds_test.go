package nft

import (
	"fmt"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func TestFeedBatch(t *testing.T) {
	got := string(FeedBatch("feed_x", []string{"192.0.2.1", "10.0.0.0/8", "2001:db8::1", "bogus", "::ffff:1.2.3.4"}))
	want := "flush set inet goblin feed_x\nadd element inet goblin feed_x { 192.0.2.1, 10.0.0.0/8 }\n"
	if got != want {
		t.Fatalf("got %q", got)
	}
	if got := string(FeedBatch("feed_x", nil)); got != "flush set inet goblin feed_x\n" {
		t.Fatalf("empty: %q", got)
	}
	var many []string
	for i := range feedChunk + 10 {
		many = append(many, fmt.Sprintf("10.%d.%d.1", i/256, i%256))
	}
	if n := strings.Count(string(FeedBatch("feed_x", many)), "add element"); n != 2 {
		t.Fatalf("%d add statements, want 2", n)
	}
	both := string(FeedBatches(map[string][]string{"feed_b": {"192.0.2.2"}, "feed_a": {"192.0.2.1"}}))
	if strings.Index(both, "feed_a") > strings.Index(both, "feed_b") || strings.Count(both, "flush set") != 2 {
		t.Fatalf("batches: %q", both)
	}
}

func TestFeedNames(t *testing.T) {
	cfg := config.Default()
	cfg.Feeds = []config.Feed{
		{Name: "a.b", Type: "ip", Enabled: true},
		{Name: "a_b", Type: "ip", Enabled: true},
		{Name: "doms", Type: "domain", Enabled: true},
		{Name: "off", Type: "ip"},
	}
	sets, counters := FeedNames(cfg)
	if len(sets) != 2 || sets["a.b"] != "feed_a_b" || sets["a_b"] != "feed_a_b_2" || counters["a_b"] != "feedhit_a_b_2" {
		t.Fatalf("sets %v counters %v", sets, counters)
	}
}
