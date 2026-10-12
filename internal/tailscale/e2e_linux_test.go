//go:build linux

package tailscale

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"
)

// TestLocalAPIE2E drives a real tailscaled (not logged in): status, the
// preferences goblind sets (netfilter off, routes, exit node, hostname),
// an interactive login URL, and turning it off. Needs root, tailscaled
// running and internet: scripts/linux-tailscale-test.ps1 runs it with
// GOBLIN_TS_E2E=1.
func TestLocalAPIE2E(t *testing.T) {
	if os.Getenv("GOBLIN_TS_E2E") != "1" {
		t.Skip("set GOBLIN_TS_E2E=1 (tailscaled running)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := &Client{}
	if !c.Available() {
		t.Fatal("tailscaled socket missing")
	}
	st, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("initial state %s", st.BackendState)

	d := Desired{Enabled: true, Hostname: "goblin-test", AdvertiseRoutes: []string{"192.168.1.0/24"}, ExitNode: true, AcceptDNS: false}
	if err := Apply(ctx, c, d); err != nil {
		t.Fatal(err)
	}
	p, err := c.GetPrefs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("prefs %+v", *p)
	if p.NetfilterMode != 0 || !p.NoSNAT || p.CorpDNS || !p.WantRunning || p.Hostname != "goblin-test" {
		t.Errorf("prefs not applied: %+v", *p)
	}
	for _, r := range []string{"192.168.1.0/24", "0.0.0.0/0", "::/0"} {
		if !slices.Contains(p.AdvertiseRoutes, r) {
			t.Errorf("route %s missing: %v", r, p.AdvertiseRoutes)
		}
	}

	if err := c.LoginInteractive(ctx); err != nil {
		t.Fatal(err)
	}
	var url string
	for i := 0; i < 60 && url == ""; i++ {
		st, err := c.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		url = st.AuthURL
		time.Sleep(500 * time.Millisecond)
	}
	if url == "" {
		t.Error("no login URL after login-interactive")
	} else {
		t.Logf("login URL %s", url)
	}

	if err := Apply(ctx, c, Desired{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if p, _ := c.GetPrefs(ctx); p.WantRunning {
		t.Error("still wants to run after disable")
	}
	if err := ApplyFile(ctx, c, "/nonexistent.json"); err == nil {
		t.Error("missing file accepted")
	}
}
