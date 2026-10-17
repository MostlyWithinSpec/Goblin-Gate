//go:build linux

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/apply"
	"github.com/networkgoblinlabs/goblin-gate/internal/auth"
	"github.com/networkgoblinlabs/goblin-gate/internal/blocks"
	"github.com/networkgoblinlabs/goblin-gate/internal/commit"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/logs"
	"github.com/networkgoblinlabs/goblin-gate/internal/plugins"
)

// TestSentryE2E runs Goblin Sentry from its release package as the real
// goblin-plugin@sentry unit against goblind's plugin socket: it reads an
// IPS alert from the log store, queues an incident, and its admin API
// (incidents, models) answers through goblind's proxy; approving the
// incident blocks the address through goblind. Needs root, systemd as PID 1
// and the package installed (scripts/linux-plugin-test.ps1).
func TestSentryE2E(t *testing.T) {
	if os.Getenv("GOBLIN_SENTRY_E2E") != "1" {
		t.Skip("set GOBLIN_SENTRY_E2E=1 (scripts/linux-plugin-test.ps1)")
	}
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	dir := t.TempDir()
	store, err := config.New(filepath.Join(dir, "config"))
	if err != nil {
		t.Fatal(err)
	}
	engine := commit.New(store, nil, apply.NewFake())
	engine.Logger = logger
	srv := NewServer(store, engine)
	srv.Logger = logger
	srv.Authn = NewAuthenticator(filepath.Join(dir, "tokens.json"), filepath.Join(dir, "audit.log"))
	if srv.Logs, err = logs.NewStore(filepath.Join(dir, "logs.db")); err != nil {
		t.Fatal(err)
	}
	defer srv.Logs.Close()
	var blocked []string
	srv.Blocks = &blocks.Manager{Path: filepath.Join(dir, "blocks.json"), Run: func(_ context.Context, script []byte) error {
		blocked = append(blocked, string(script))
		return nil
	}}
	version := os.Getenv("GOBLIN_VERSION") // the version the package was built with
	mgr := &plugins.Manager{ManifestDir: plugins.ManifestDir, ProgramDir: plugins.ProgramDir,
		Version: version, Systemd: plugins.Systemctl{}, Logger: logger}
	srv.Plugins = mgr
	config.SetPluginCatalog(mgr.Lookup)
	t.Cleanup(func() { config.SetPluginCatalog(nil) })
	engine.AfterApply = func(live *config.Config) { mgr.Sync(ctx, live) }

	if err := os.MkdirAll(filepath.Dir(plugins.SocketPath), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(plugins.SocketPath)
	ln, err := net.Listen("unix", plugins.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(plugins.SocketPath, 0o666)
	sock := &http.Server{Handler: srv.PluginRoutes(), ConnContext: PluginConnContext(plugins.PeerUnit)}
	go sock.Serve(ln)
	t.Cleanup(func() { sock.Close() })

	in, ok := mgr.Rescan()["sentry"]
	if !ok || in.Problem != "" {
		t.Fatalf("sentry not installed or refused: %+v", in)
	}
	if in.Manifest.Version != version || in.Manifest.MinGoblind != version {
		t.Fatalf("manifest not stamped with %s: %+v", version, in.Manifest)
	}

	// the box: LAN 192.168.1.0/24, a DNS forwarder; Sentry enabled with all it asks for
	hash, _ := auth.HashPassword("correct horse battery")
	cfg, err := store.Candidate()
	if err != nil {
		t.Fatal(err)
	}
	lan := netip.MustParsePrefix("192.168.1.1/24")
	wan := netip.MustParsePrefix("203.0.113.2/24")
	cfg.Interfaces = []config.Interface{{Name: "wan1", Device: "eth0", Mode: "static", Role: "wan", Address: &wan, Enabled: true},
		{Name: "lan", Device: "eth1", Mode: "static", Role: "lan", Address: &lan, Enabled: true}}
	cfg.Admins = []config.Admin{{Username: "admin", PasswordHash: hash, Role: "admin"}}
	cfg.Plugins = []config.Plugin{{Name: "sentry", Enabled: true, Capabilities: in.Manifest.Capabilities}}
	if err := store.SetCandidate(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Commit(ctx, "admin", "sentry"); err != nil {
		t.Fatal(err)
	}
	unit := plugins.Unit("sentry")
	journal := func() string {
		out, _ := exec.Command("journalctl", "-u", unit, "-o", "cat", "--no-pager").CombinedOutput()
		return string(out)
	}

	admin := httptest.NewUnstartedServer(srv.Routes())
	admin.Config.ConnContext = WithSocketConn // as root over the CLI socket
	admin.Start()
	defer admin.Close()
	call := func(method, path, body string) (int, map[string]any) {
		req, _ := http.NewRequest(method, admin.URL+"/api/v1/plugins/sentry/api"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, nil
		}
		defer resp.Body.Close()
		var out map[string]any
		b, _ := io.ReadAll(resp.Body)
		json.Unmarshal(b, &out)
		return resp.StatusCode, out
	}
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(40 * time.Second); !ok(); time.Sleep(500 * time.Millisecond) {
			if time.Now().After(deadline) {
				st, _ := exec.Command("systemctl", "status", "--no-pager", unit).CombinedOutput()
				t.Fatalf("%s: timed out\n%s\n%s", what, journal(), st)
			}
		}
	}
	var status map[string]any
	waitFor("sentry api", func() bool {
		code, out := call("GET", "/status", "")
		status = out
		return code == 200
	})
	if status["mode"] != "approve" {
		t.Fatalf("status: %v", status)
	}

	// an alert from outside against a LAN host becomes a pending incident
	// (Sentry starts reading after the newest alert it finds, so log it now)
	now := time.Now().Unix()
	if err := srv.Logs.InsertBatch([]*logs.Event{{Time: now, Kind: "ips", Action: "alert", SrcIP: "198.51.100.7", SrcPort: 40000,
		DstIP: "192.168.1.10", DstPort: 445, Proto: "TCP", Signature: "2024217: ET EXPLOIT Possible ETERNALBLUE", Category: "Attempted Administrator Privilege Gain",
		Severity: "1", Message: "ET EXPLOIT Possible ETERNALBLUE"}}); err != nil {
		t.Fatal(err)
	}
	var incident map[string]any
	waitFor("incident", func() bool {
		_, out := call("GET", "/incidents?status=pending", "")
		list, _ := out["incidents"].([]any)
		if len(list) == 1 {
			incident = list[0].(map[string]any)
		}
		return incident != nil
	})
	if en := incident["enrichment"].(map[string]any); en["direction"] != "inbound" || en["remote"] != "198.51.100.7" {
		t.Fatalf("incident: %v", incident)
	}

	// the decision model: none yet, too few answers to train
	if code, out := call("GET", "/models", ""); code != 200 || out["current"].(float64) != 0 {
		t.Fatalf("models: %d %v", code, out)
	}
	if code, out := call("POST", "/models/train", "{}"); code != 400 || !strings.Contains(fmt.Sprint(out["error"]), "not enough answers") {
		t.Fatalf("train: %d %v", code, out)
	}

	// approving blocks the address through goblind's temporary blocks
	id := int64(incident["id"].(float64))
	code, out := call("POST", fmt.Sprintf("/incidents/%d/answer", id), `{"answer":"approve"}`)
	if code != 200 || out["incident"].(map[string]any)["status"] != "acted" {
		t.Fatalf("approve: %d %v", code, out)
	}
	if b, ok := srv.Blocks.Get(netip.MustParseAddr("198.51.100.7")); !ok || b.Source != "plugin:sentry" || len(blocked) == 0 {
		t.Fatalf("block: %+v %v", b, ok)
	}
	if code, out := call("GET", "/models/examples", ""); code != 200 || len(out["examples"].([]any)) != 1 {
		t.Fatalf("examples: %d %v", code, out)
	}

	// the sandbox caps
	for prop, want := range map[string]string{"CPUQuotaPerSecUSec": "1s", "MemoryMax": "536870912", "DynamicUser": "yes"} {
		if got := strings.TrimSpace(e2eRun(t, "systemctl", "show", "-P", prop, unit)); got != want {
			t.Fatalf("%s=%s, want %s", prop, got, want)
		}
	}
	t.Logf("journal:\n%s", journal())
}
