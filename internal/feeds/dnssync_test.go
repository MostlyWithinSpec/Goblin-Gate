package feeds

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

type mapFeeds map[string][]string

func (m mapFeeds) Entries(name string) []string { return m[name] }

func TestDNSSync(t *testing.T) {
	written := map[string]string{}
	var reloaded []string
	feeds := mapFeeds{"bad": {"evil.example"}}
	s := &DNSSync{
		Feeds:  feeds,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Write:  func(p string, d []byte) error { written[p] = string(d); return nil },
		Reload: func(_ context.Context, z string) error { reloaded = append(reloaded, z); return nil },
	}
	s.Update("bad") // no live config yet
	if len(written) != 0 {
		t.Fatal("wrote before the first apply")
	}
	cfg := config.Default()
	lan := netip.MustParsePrefix("192.168.1.1/24")
	cfg.Interfaces[1].Address = &lan
	cfg.Feeds = []config.Feed{{Name: "bad", Type: "domain", URL: "https://x.example/f", Enabled: true}, {Name: "other", Type: "domain", URL: "https://x.example/o", Enabled: true}}
	cfg.Profiles.DNSFilter = []config.DNSFilterProfile{{Name: "home", BlockFeeds: []string{"bad"}}}
	cfg.Policies[0].DNSFilterProfile = "home"
	s.Resync(cfg)

	feeds["bad"] = []string{"evil.example", "worse.example"}
	s.Update("bad")
	z := written["/var/lib/unbound/rpz/home.zone"]
	if !strings.Contains(z, "worse.example CNAME .") || len(reloaded) != 1 || reloaded[0] != "home.rpz.goblin." {
		t.Fatalf("zone %q reloads %v", z, reloaded)
	}
	s.Update("other") // no profile blocks it
	if len(reloaded) != 1 {
		t.Fatalf("reloaded for an unused feed: %v", reloaded)
	}
}
