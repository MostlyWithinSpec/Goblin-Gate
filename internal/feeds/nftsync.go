package feeds

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/nft"
)

// NFTSync keeps the kernel's feed_<name> sets (task 028) filled with the
// feeds' last good entries. Wire Update to Manager.OnUpdate and Resync to
// commit.Engine.AfterApply: every ruleset load empties the sets.
type NFTSync struct {
	// Run loads an nft script (default: nft -f -).
	Run    func(ctx context.Context, script []byte) error
	Logger *slog.Logger

	mu      sync.Mutex
	entries map[string][]string // feed name -> last good entries
	sets    map[string]string   // feed name -> set, from the live config
}

func (s *NFTSync) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *NFTSync) run(ctx context.Context, script []byte) error {
	if s.Run != nil {
		return s.Run(ctx, script)
	}
	cmd := exec.CommandContext(ctx, "nft", "-f", "-")
	cmd.Stdin = bytes.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("nft: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Update stores a feed's new entries and loads them into its set, if the
// live ruleset has one (the feed is enabled and of type ip).
func (s *NFTSync) Update(name string, entries []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = map[string][]string{}
	}
	s.entries[name] = entries
	set, ok := s.sets[name]
	if !ok {
		return
	}
	s.load(map[string][]string{set: entries})
}

// Resync is called after a ruleset load with the live config: it learns
// the set names and refills every set in one transaction.
func (s *NFTSync) Resync(live *config.Config) {
	sets, _ := nft.FeedNames(live)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sets = sets
	batch := map[string][]string{}
	for name, set := range sets {
		if e, ok := s.entries[name]; ok {
			batch[set] = e
		}
	}
	if len(batch) > 0 {
		s.load(batch)
	}
}

// load runs with s.mu held so updates reach the kernel in order.
func (s *NFTSync) load(batch map[string][]string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	start := time.Now()
	n := 0
	for _, e := range batch {
		n += len(e)
	}
	if err := s.run(ctx, nft.FeedBatches(batch)); err != nil {
		s.logger().Error("feeds: loading nft sets failed", "sets", len(batch), "error", err)
		return
	}
	s.logger().Info("feeds: nft sets loaded", "sets", len(batch), "entries", n, "took", time.Since(start).Round(time.Millisecond))
}
