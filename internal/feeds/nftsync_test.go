package feeds

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func TestNFTSync(t *testing.T) {
	var mu sync.Mutex
	var scripts []string
	s := &NFTSync{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Run: func(_ context.Context, b []byte) error {
			mu.Lock()
			scripts = append(scripts, string(b))
			mu.Unlock()
			return nil
		},
	}
	// before the first ruleset load there are no sets: entries are kept
	s.Update("bad", []string{"192.0.2.1"})
	if len(scripts) != 0 {
		t.Fatalf("loaded before any set exists: %q", scripts)
	}
	cfg := config.Default()
	cfg.Feeds = []config.Feed{
		{Name: "bad", Type: "ip", Enabled: true},
		{Name: "doms", Type: "domain", Enabled: true},
	}
	s.Resync(cfg)
	if len(scripts) != 1 || !strings.Contains(scripts[0], "add element inet goblin feed_bad { 192.0.2.1 }") {
		t.Fatalf("resync: %q", scripts)
	}
	s.Update("bad", []string{"192.0.2.1", "198.51.100.0/24"})
	s.Update("doms", []string{"evil.example"}) // no set: nothing loaded
	if len(scripts) != 2 || !strings.HasPrefix(scripts[1], "flush set inet goblin feed_bad\n") {
		t.Fatalf("update: %q", scripts)
	}
	// feed disabled by a commit: its set is gone, updates stop
	cfg.Feeds[0].Enabled = false
	s.Resync(cfg)
	s.Update("bad", []string{"192.0.2.9"})
	if len(scripts) != 2 {
		t.Fatalf("loaded a disabled feed: %q", scripts[2:])
	}
}
