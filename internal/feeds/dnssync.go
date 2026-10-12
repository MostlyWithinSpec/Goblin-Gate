package feeds

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/unbound"
)

// DNSSync keeps the DNS filter's RPZ zones (task 029) current between
// commits: when a domain feed changes, the zones of the profiles that block
// it are rewritten and reloaded with unbound-control. A commit renders the
// zones itself (unbound.Renderer has the same feed source), so Resync only
// records the live config.
type DNSSync struct {
	Feeds  unbound.FeedSource
	Logger *slog.Logger

	// Write and Reload default to an atomic file write and
	// `unbound-control auth_zone_reload <zone>`.
	Write  func(path string, data []byte) error
	Reload func(ctx context.Context, zone string) error

	mu   sync.Mutex
	live *config.Config
}

func (s *DNSSync) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// Resync records the config that is live after an apply.
func (s *DNSSync) Resync(live *config.Config) {
	s.mu.Lock()
	s.live = live
	s.mu.Unlock()
}

// Update rewrites and reloads the zones that use feed.
func (s *DNSSync) Update(feed string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.live == nil {
		return
	}
	for _, z := range unbound.Zones(s.live) {
		if !slices.Contains(z.Profile.BlockFeeds, feed) {
			continue
		}
		path, name := unbound.ZonePath(z.Profile.Name), unbound.ZoneName(z.Profile.Name)
		data := unbound.ZoneFile(z.Profile, s.Feeds, z.BlockAddr)
		if err := s.write(path, data); err != nil {
			s.logger().Error("feeds: writing DNS filter zone failed", "zone", path, "error", err)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		err := s.reload(ctx, name)
		cancel()
		if err != nil {
			s.logger().Error("feeds: reloading DNS filter zone failed", "zone", name, "error", err)
			continue
		}
		s.logger().Info("feeds: DNS filter zone reloaded", "profile", z.Profile.Name, "feed", feed)
	}
}

func (s *DNSSync) write(path string, data []byte) error {
	if s.Write != nil {
		return s.Write(path, data)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeAtomicMode(path, data, 0o644)
}

func (s *DNSSync) reload(ctx context.Context, zone string) error {
	if s.Reload != nil {
		return s.Reload(ctx, zone)
	}
	out, err := exec.CommandContext(ctx, "unbound-control", "-c", "/etc/unbound/unbound.conf", "auth_zone_reload", zone).CombinedOutput()
	if err != nil {
		return fmt.Errorf("unbound-control: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func writeAtomicMode(path string, data []byte, mode os.FileMode) error {
	if err := writeAtomic(path, data); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}
