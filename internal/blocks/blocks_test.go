package blocks

import (
	"context"
	"errors"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

type fakeNft struct {
	mu      sync.Mutex
	scripts []string
	fail    error
}

func (f *fakeNft) run(_ context.Context, script []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.scripts = append(f.scripts, string(script))
	return nil
}

func (f *fakeNft) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.scripts) == 0 {
		return ""
	}
	return f.scripts[len(f.scripts)-1]
}

func newManager(t *testing.T) (*Manager, *fakeNft, *time.Time) {
	t.Helper()
	nft := &fakeNft{}
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	m := &Manager{Path: filepath.Join(t.TempDir(), "blocks.json"), Run: nft.run, Now: func() time.Time { return now }}
	return m, nft, &now
}

var ip1 = netip.MustParseAddr("198.51.100.7")

func TestAddListExpire(t *testing.T) {
	m, nft, now := newManager(t)
	ctx := context.Background()
	b, err := m.Add(ctx, Block{IP: ip1, Reason: "scan", Source: "admin", CreatedBy: "alice"}, 10*time.Minute, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Until.Equal(now.Add(10 * time.Minute)) {
		t.Fatalf("until %v", b.Until)
	}
	if got := nft.last(); !strings.Contains(got, "add element inet goblin block_tmp { 198.51.100.7 timeout 600s }") {
		t.Fatalf("script:\n%s", got)
	}
	v6 := netip.MustParseAddr("2001:db8::7")
	if _, err := m.Add(ctx, Block{IP: v6, Reason: "x", Source: "admin"}, time.Hour, 0); err != nil {
		t.Fatal(err)
	}
	if got := nft.last(); !strings.Contains(got, "add element inet goblin block_tmp6 { 2001:db8::7 timeout 3600s }") {
		t.Fatalf("v6 script:\n%s", got)
	}
	if l := m.List(); len(l) != 2 || l[0].IP != ip1 {
		t.Fatalf("list: %+v", l)
	}
	// blocking again replaces the deadline
	if _, err := m.Add(ctx, Block{IP: ip1, Reason: "again", Source: "admin"}, 30*time.Minute, 0); err != nil {
		t.Fatal(err)
	}
	if b, _ := m.Get(ip1); b.Reason != "again" || !b.Until.Equal(now.Add(30*time.Minute)) {
		t.Fatalf("replaced: %+v", b)
	}
	// time passes: the IPv4 block expires, the hour-long one stays
	*now = now.Add(45 * time.Minute)
	if l := m.List(); len(l) != 1 || l[0].IP != v6 {
		t.Fatalf("after expiry: %+v", l)
	}
	for _, d := range []time.Duration{0, 30 * time.Second, (MaxMinutes + 1) * time.Minute} {
		if _, err := m.Add(ctx, Block{IP: ip1, Source: "admin"}, d, 0); err == nil {
			t.Errorf("duration %v accepted", d)
		}
	}
}

func TestPersistAndResync(t *testing.T) {
	m, _, now := newManager(t)
	ctx := context.Background()
	if _, err := m.Add(ctx, Block{IP: ip1, Reason: "scan", Source: "plugin:sentry", CreatedBy: "sentry"}, 20*time.Minute, 0); err != nil {
		t.Fatal(err)
	}
	// a new goblind reads the store and puts the block back with the time left
	nft2 := &fakeNft{}
	later := now.Add(5 * time.Minute)
	m2 := &Manager{Path: m.Path, Run: nft2.run, Now: func() time.Time { return later }}
	m2.Resync(ctx)
	if got := nft2.last(); !strings.Contains(got, "{ 198.51.100.7 timeout 900s }") {
		t.Fatalf("resync script:\n%s", got)
	}
	if b, ok := m2.Get(ip1); !ok || b.Source != "plugin:sentry" || b.Reason != "scan" {
		t.Fatalf("loaded: %+v %v", b, ok)
	}
	// nothing to restore: no script
	m3 := &Manager{Path: filepath.Join(t.TempDir(), "none.json"), Run: nft2.run}
	n := len(nft2.scripts)
	m3.Resync(ctx)
	if len(nft2.scripts) != n {
		t.Fatal("empty resync ran nft")
	}
}

func TestRemove(t *testing.T) {
	m, nft, _ := newManager(t)
	ctx := context.Background()
	if _, err := m.Add(ctx, Block{IP: ip1, Source: "admin"}, time.Hour, 0); err != nil {
		t.Fatal(err)
	}
	if ok, err := m.Remove(ctx, netip.MustParseAddr("::ffff:198.51.100.7")); !ok || err != nil {
		t.Fatalf("remove: %v %v", ok, err)
	}
	want := "add element inet goblin block_tmp { 198.51.100.7 }\ndelete element inet goblin block_tmp { 198.51.100.7 }\n"
	if got := nft.last(); got != want {
		t.Fatalf("remove script:\n%s", got)
	}
	if ok, _ := m.Remove(ctx, ip1); ok {
		t.Fatal("removed twice")
	}
}

func TestRateLimitAndKernelFailure(t *testing.T) {
	m, nft, now := newManager(t)
	ctx := context.Background()
	for i := range 3 {
		ip := netip.AddrFrom4([4]byte{198, 51, 100, byte(i + 1)})
		if _, err := m.Add(ctx, Block{IP: ip, Source: "plugin:sentry", CreatedBy: "sentry"}, time.Hour, 3); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Add(ctx, Block{IP: ip1, Source: "plugin:sentry", CreatedBy: "sentry"}, time.Hour, 3); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("4th block: %v", err)
	}
	// another requester has its own budget
	if _, err := m.Add(ctx, Block{IP: ip1, Source: "admin", CreatedBy: "alice"}, time.Hour, 3); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(61 * time.Minute)
	if _, err := m.Add(ctx, Block{IP: netip.MustParseAddr("198.51.100.99"), Source: "plugin:sentry", CreatedBy: "sentry"}, time.Hour, 3); err != nil {
		t.Fatalf("after an hour: %v", err)
	}
	// nft fails: nothing is stored
	nft.fail = errors.New("boom")
	if _, err := m.Add(ctx, Block{IP: netip.MustParseAddr("203.0.113.50"), Source: "admin", CreatedBy: "alice"}, time.Hour, 0); err == nil {
		t.Fatal("kernel failure ignored")
	}
	if _, ok := m.Get(netip.MustParseAddr("203.0.113.50")); ok {
		t.Fatal("stored although the kernel refused it")
	}
}

func TestProtected(t *testing.T) {
	cfg := config.Default()
	lan := netip.MustParsePrefix("192.168.1.1/24")
	wanAddr := netip.MustParsePrefix("203.0.113.2/24")
	wanGw := netip.MustParseAddr("203.0.113.1")
	cfg.Interfaces = []config.Interface{
		{Name: "wan", Role: "wan", Address: &wanAddr, Gateway: &wanGw, Enabled: true},
		{Name: "lan", Role: "lan", Address: &lan, Enabled: true},
	}
	cfg.Gateways = []config.Gateway{{Name: "isp2", Interface: "wan", Address: "198.51.100.1"}}
	cfg.DNS.Forwarders = []netip.Addr{netip.MustParseAddr("9.9.9.9")}
	cfg.System.NTPServers = []string{"pool.ntp.org", "192.0.2.123"}
	cfg.VPN.WireGuard = []config.WireGuard{{Name: "wg0", Address: netip.MustParsePrefix("10.66.0.1/24")}}

	admin := netip.MustParseAddr("192.0.2.200")
	check := Check{Requester: admin, Live: []netip.Addr{netip.MustParseAddr("100.70.0.5")}}
	for ip, want := range map[string]string{
		"198.51.100.7":    "",
		"2001:db8::7":     "",
		"127.0.0.1":       "special",
		"0.0.0.0":         "special",
		"224.0.0.1":       "special",
		"169.254.1.1":     "special",
		"fe80::1":         "special",
		"255.255.255.255": "special",
		"203.0.113.2":     "address of the box",
		"192.168.1.1":     "address of the box",
		"203.0.113.1":     "gateway",
		"198.51.100.1":    "gateway",
		"9.9.9.9":         "DNS server",
		"192.0.2.123":     "NTP server",
		"192.0.2.200":     "your own",
		"100.70.0.5":      "box or one of its gateways",
		"192.168.1.50":    "box's own networks",
		"10.66.0.9":       "box's own networks",
		"203.0.113.77":    "", // the WAN subnet is not internal
	} {
		got := Protected(cfg, netip.MustParseAddr(ip), check)
		if (want == "") != (got == "") || !strings.Contains(got, want) {
			t.Errorf("%s: %q, want %q", ip, got, want)
		}
	}
	check.AllowInternal = true
	if got := Protected(cfg, netip.MustParseAddr("192.168.1.50"), check); got != "" {
		t.Errorf("allow_internal: %q", got)
	}
	if got := Protected(cfg, netip.MustParseAddr("192.168.1.1"), check); got == "" {
		t.Error("allow_internal must still protect the box itself")
	}
}
