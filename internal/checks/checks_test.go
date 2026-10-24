package checks

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

var now = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

// base is the default config with an admin password set and a git backup
// that works, so it has no findings of its own.
func base() *config.Config {
	c := config.Default()
	c.Admins[0].PasswordHash = "$argon2id$x"
	c.Admins[0].TOTPSecret = "secret"
	c.GitBackup = config.GitBackup{Enabled: true, Remote: "git@github.com:me/gate.git"}
	return c
}

func keys(r Report) []string {
	var out []string
	for _, f := range r.Findings {
		out = append(out, f.Key)
	}
	return out
}

func run(c *config.Config, st State) Report {
	st.Now = now
	return Run(c, st, nil)
}

func TestDefaultIsClean(t *testing.T) {
	r := run(base(), State{GitLastPush: now})
	if len(r.Findings) != 0 || r.Score != 100 {
		t.Fatalf("findings on a clean config: %+v", r.Findings)
	}
	if len(r.Passed) != len(Checks()) {
		t.Fatalf("passed %d of %d checks", len(r.Passed), len(Checks()))
	}
}

func TestChecks(t *testing.T) {
	guestPolicy := func(c *config.Config, p config.Policy) {
		c.Interfaces = append(c.Interfaces, config.Interface{Name: "guest", Device: "eth2", Role: "guest", Enabled: true})
		c.Policies = append(c.Policies, p)
	}
	cases := []struct {
		name   string
		change func(c *config.Config)
		state  State
		want   string
		sev    Severity
		link   string
	}{
		{"https on the WAN", func(c *config.Config) { c.Interfaces[0].AllowAccess = []string{"ping", "https"} },
			State{}, "wan-admin:wan1:https", High, "/network/interfaces?edit=wan1"},
		{"ssh on a VLAN of the WAN", func(c *config.Config) {
			c.VLANs = append(c.VLANs, config.VLAN{Name: "wanv", Parent: "wan1", VLANID: 7, AllowAccess: []string{"ssh"}})
		}, State{}, "wan-admin:wanv:ssh", High, "/network/interfaces?edit=wanv"},
		{"forward open to all", func(c *config.Config) {
			c.NAT.PortForwards = []config.PortForward{{Name: "web", InIntf: "wan1", ExtPort: "443", Protocol: "tcp", ToAddr: "192.168.1.5"}}
		}, State{}, "open-forward:web", Medium, "/network/port-forwards?edit=web"},
		{"RDP forward open to all", func(c *config.Config) {
			c.NAT.PortForwards = []config.PortForward{{Name: "rdp", ExtPort: "3000-4000", Protocol: "tcp", ToAddr: "192.168.1.5", Source: []string{"ALL"}}}
		}, State{}, "open-forward:rdp", High, ""},
		{"UPnP on", func(c *config.Config) { c.UPnP.Enabled = true }, State{}, "upnp:on", Low, "/network/upnp"},
		{"any from the WAN", func(c *config.Config) {
			c.Policies = append(c.Policies, config.Policy{ID: 9, Name: "oops", Enabled: true, SrcIntf: []string{"wan1"}, DstIntf: []string{"lan"},
				SrcAddr: []string{"all"}, DstAddr: []string{"all"}, Service: []string{"any"}, Action: "accept"})
		}, State{}, "wan-accept:9", High, "/policy?edit=9"},
		{"any from the WAN, written all/ALL", func(c *config.Config) {
			c.Policies = append(c.Policies, config.Policy{ID: 8, Enabled: true, SrcIntf: []string{"wan1"}, DstIntf: []string{"lan"},
				SrcAddr: []string{"ALL"}, Service: []string{"all"}, Action: "accept"})
		}, State{}, "wan-accept:8", High, "/policy?edit=8"},
		{"service from the WAN without IPS", func(c *config.Config) {
			c.Policies = append(c.Policies, config.Policy{ID: 10, Enabled: true, SrcIntf: []string{"wan1"}, DstIntf: []string{"lan"},
				SrcAddr: []string{"all"}, DstAddr: []string{"nas"}, Service: []string{"HTTPS"}, Action: "accept"})
		}, State{}, "wan-ips:10", Medium, "/policy?edit=10"},
		{"guest into the LAN", func(c *config.Config) {
			guestPolicy(c, config.Policy{ID: 11, Enabled: true, SrcIntf: []string{"guest"}, DstIntf: []string{"lan"},
				SrcAddr: []string{"all"}, DstAddr: []string{"all"}, Service: []string{"any"}, Action: "accept"})
		}, State{}, "guest-access:11", Medium, "/policy?edit=11"},
		{"guest to the internet without a DNS filter", func(c *config.Config) {
			guestPolicy(c, config.Policy{ID: 12, Enabled: true, SrcIntf: []string{"guest"}, DstIntf: []string{"wan1"},
				SrcAddr: []string{"all"}, DstAddr: []string{"all"}, Service: []string{"any"}, Action: "accept", NAT: true})
		}, State{}, "dns-filter:12", Low, "/policy?edit=12"},
		{"admin without 2FA", func(c *config.Config) { c.Admins[0].TOTPSecret = "" }, State{}, "admin-2fa:admin", Medium, "/system?tab=Admins"},
		{"no git backup", func(c *config.Config) { c.GitBackup = config.GitBackup{} }, State{}, "backup:off", Low, "/system?tab=Backup"},
		{"git push failing", func(*config.Config) {}, State{GitLastError: "permission denied"}, "backup:failing", Medium, ""},
		{"git push stale", func(*config.Config) {}, State{GitLastPush: now.Add(-40 * 24 * time.Hour)}, "backup:stale", Low, ""},
		{"security updates manual", func(c *config.Config) { c.System.SecurityUpdates = "manual" }, State{}, "security-updates:manual", Medium, "/system/updates"},
		{"reboot pending", func(*config.Config) {}, State{RebootRequired: true}, "reboot:pending", Low, ""},
		{"update available", func(*config.Config) {}, State{UpdateAvailable: "0.1.0"}, "goblin-update:0.1.0", Low, ""},
		{"custom rules", func(c *config.Config) {
			c.CustomRules = []config.CustomRule{{Name: "x", Enabled: true, Hook: "input", Rule: "tcp dport 9100 accept"}}
		}, State{}, "custom-rules:on", Info, "/policy/custom-rules"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			tc.change(c)
			r := run(c, tc.state)
			if got := keys(r); !slices.Equal(got, []string{tc.want}) {
				t.Fatalf("findings %v, want [%s]", got, tc.want)
			}
			f := r.Findings[0]
			if f.Severity != tc.sev {
				t.Errorf("severity %s, want %s", f.Severity, tc.sev)
			}
			if tc.link != "" && f.Link != tc.link {
				t.Errorf("link %s, want %s", f.Link, tc.link)
			}
			if f.Title == "" || f.Detail == "" || f.Check == "" {
				t.Errorf("incomplete finding %+v", f)
			}
			if r.Score != 100-weight[tc.sev] {
				t.Errorf("score %d", r.Score)
			}
		})
	}
}

func TestQuietCases(t *testing.T) {
	c := base()
	// limited forward, policy to a specific service with IPS, a disabled any-any
	c.NAT.PortForwards = []config.PortForward{{Name: "plex", ExtPort: "32400", Protocol: "tcp", ToAddr: "192.168.1.5", Source: []string{"home"}}}
	c.Policies = append(c.Policies,
		config.Policy{ID: 20, Enabled: true, SrcIntf: []string{"wan1"}, DstIntf: []string{"lan"}, DstAddr: []string{"nas"}, Service: []string{"HTTPS"}, Action: "accept", IPSProfile: "default"},
		config.Policy{ID: 21, Enabled: false, SrcIntf: []string{"wan1"}, Action: "accept"},
		config.Policy{ID: 22, Enabled: true, SrcIntf: []string{"wan1"}, Action: "deny"},
	)
	c.Admins = append(c.Admins, config.Admin{Username: "viewer", Role: "readonly", PasswordHash: "$argon2id$y"})
	c.CustomRules = []config.CustomRule{{Name: "off", Enabled: false, Hook: "input", Rule: "accept"}}
	if r := run(c, State{GitLastPush: now.Add(-24 * time.Hour)}); len(r.Findings) != 0 {
		t.Fatalf("unexpected findings %v", keys(r))
	}
}

func TestScoreOrderAndDismiss(t *testing.T) {
	c := base()
	c.Admins[0].TOTPSecret = ""                                                                    // medium
	c.UPnP.Enabled = true                                                                          // low
	c.Interfaces[0].AllowAccess = []string{"https", "ssh"}                                         // 2 x high
	c.CustomRules = []config.CustomRule{{Name: "x", Enabled: true, Hook: "input", Rule: "accept"}} // info
	r := run(c, State{})
	if r.Score != 100-20-20-8-3 {
		t.Fatalf("score %d", r.Score)
	}
	var sevs []Severity
	for _, f := range r.Findings {
		sevs = append(sevs, f.Severity)
	}
	if !slices.Equal(sevs, []Severity{High, High, Medium, Low, Info}) {
		t.Fatalf("order %v", sevs)
	}

	r = Run(c, State{Now: now}, map[string]bool{"wan-admin:wan1:ssh": true, "upnp:on": true})
	if r.Score != 100-20-8 || len(r.Dismissed) != 2 || len(r.Findings) != 3 {
		t.Fatalf("after dismiss: score %d, %d findings, %d dismissed", r.Score, len(r.Findings), len(r.Dismissed))
	}
	// a dismissed finding's check did find something, so it is not "passed"
	for _, p := range r.Passed {
		if p.Check == "upnp" {
			t.Fatal("dismissed check listed as passed")
		}
	}

	// the score never goes below zero
	for i := range 10 {
		c.NAT.PortForwards = append(c.NAT.PortForwards, config.PortForward{Name: "f" + string(rune('a'+i)), ExtPort: "22", Protocol: "tcp", ToAddr: "192.168.1.9"})
	}
	if r := run(c, State{}); r.Score != 0 {
		t.Fatalf("score %d, want 0", r.Score)
	}
}

func TestDismissals(t *testing.T) {
	d := &Dismissals{Path: filepath.Join(t.TempDir(), "checks", "dismissed.json")}
	if m, err := d.Load(); err != nil || len(m) != 0 {
		t.Fatalf("empty load: %v %v", m, err)
	}
	for _, k := range []string{"upnp:on", "wan-admin:wan1:https", "upnp:on"} {
		if err := d.Set(k, true); err != nil {
			t.Fatal(err)
		}
	}
	m, _ := d.Load()
	if len(m) != 2 || !m["upnp:on"] {
		t.Fatalf("loaded %v", m)
	}
	if err := d.Set("upnp:on", false); err != nil {
		t.Fatal(err)
	}
	if m, _ := d.Load(); len(m) != 1 || m["upnp:on"] {
		t.Fatalf("after undo %v", m)
	}
	for _, bad := range []string{"", "nocolon", "x:../../etc", "UPPER:x", "a:b c"} {
		if err := d.Set(bad, true); err == nil {
			t.Errorf("key %q accepted", bad)
		}
	}
}

// Only a plugin behind: the finding names the plugin, not a Goblin Gate version.
func TestPluginUpdateFinding(t *testing.T) {
	r := run(base(), State{GitLastPush: now, PluginUpdates: []string{"sentry 0.1.0~beta.8 → 0.1.0~beta.9"}})
	if len(r.Findings) != 1 || r.Findings[0].Key != "goblin-update:plugins" ||
		!strings.Contains(r.Findings[0].Title, "sentry 0.1.0~beta.8") || strings.Contains(r.Findings[0].Title, "Goblin Gate 0.1") {
		t.Fatalf("%+v", r.Findings)
	}
}

// A dismissed restart covers that restart only.
func TestRebootFindingKeyedByWhen(t *testing.T) {
	first := run(base(), State{GitLastPush: now, RebootRequired: true, RebootSince: now.Add(-48 * time.Hour)})
	later := run(base(), State{GitLastPush: now, RebootRequired: true, RebootSince: now})
	if first.Findings[0].Key == later.Findings[0].Key || !strings.HasPrefix(first.Findings[0].Key, "reboot:") {
		t.Fatalf("keys %s %s", first.Findings[0].Key, later.Findings[0].Key)
	}
	if !strings.Contains(first.Findings[0].Detail, "Restart Goblin Gate") {
		t.Fatalf("detail %q", first.Findings[0].Detail)
	}
}
