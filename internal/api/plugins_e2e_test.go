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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/apply"
	"github.com/networkgoblinlabs/goblin-gate/internal/auth"
	"github.com/networkgoblinlabs/goblin-gate/internal/commit"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/plugins"
)

// TestPluginE2E runs the example hello plugin as a real goblin-plugin@hello
// unit: installed from its .deb, started by the plugin manager with
// systemctl, sandboxed, calling goblind on the real plugin socket. It needs
// root, systemd as PID 1, the unit in /lib/systemd/system and the hello
// package installed (scripts/linux-plugin-test.ps1 sets that up).
func TestPluginE2E(t *testing.T) {
	if os.Getenv("GOBLIN_PLUGIN_E2E") != "1" {
		t.Skip("set GOBLIN_PLUGIN_E2E=1 (scripts/linux-plugin-test.ps1)")
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
	mgr := &plugins.Manager{ManifestDir: plugins.ManifestDir, ProgramDir: plugins.ProgramDir,
		Version: "0.1.0~alpha.4", Systemd: plugins.Systemctl{}, Logger: logger}
	srv.Plugins = mgr
	config.SetPluginCatalog(mgr.Lookup)
	t.Cleanup(func() { config.SetPluginCatalog(nil) })
	engine.AfterApply = func(live *config.Config) { mgr.Sync(ctx, live) }

	// the plugin socket, as goblind opens it
	if err := os.MkdirAll(filepath.Dir(plugins.SocketPath), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(plugins.SocketPath)
	ln, err := net.Listen("unix", plugins.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(plugins.SocketPath, 0o666); err != nil {
		t.Fatal(err)
	}
	sock := &http.Server{Handler: srv.PluginRoutes(), ConnContext: PluginConnContext(plugins.PeerUnit)}
	go sock.Serve(ln)
	t.Cleanup(func() { sock.Close() })

	if in, ok := mgr.Rescan()["hello"]; !ok || in.Problem != "" {
		t.Fatalf("hello not installed or invalid: %+v", in)
	}

	hash, err := auth.HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	commitPlugins := func(p ...config.Plugin) {
		t.Helper()
		cfg, err := store.Candidate()
		if err != nil {
			t.Fatal(err)
		}
		cfg.Admins = []config.Admin{{Username: "admin", PasswordHash: hash, Role: "admin"}}
		cfg.Plugins = p
		if err := store.SetCandidate(cfg); err != nil {
			t.Fatal(err)
		}
		if _, err := engine.Commit(ctx, "admin", "plugins"); err != nil {
			t.Fatal(err)
		}
	}
	unit := plugins.Unit("hello")
	journal := func() string {
		out, _ := exec.Command("journalctl", "-u", unit, "-o", "cat", "--no-pager").CombinedOutput()
		return string(out)
	}
	waitJournal := func(want ...string) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			j := journal()
			missing := ""
			for _, w := range want {
				if !strings.Contains(j, w) {
					missing = w
					break
				}
			}
			if missing == "" {
				return
			}
			if time.Now().After(deadline) {
				st, _ := exec.Command("systemctl", "status", "--no-pager", unit).CombinedOutput()
				t.Fatalf("journal lacks %q:\n%s\n%s", missing, j, st)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}

	// enabled without config:read
	commitPlugins(config.Plugin{Name: "hello", Enabled: true, Capabilities: []string{"events:read", "notify:send"},
		Settings: map[string]any{"greeting": "e2e-hi", "interval_seconds": float64(10)}})
	waitJournal("hello: e2e-hi, I am hello with capabilities [events:read notify:send]",
		"hello: notify: status 202", "hello: read events: status 503", "hello: read config: status 403")
	// a process outside the plugin's unit (this test, as root) is refused
	local := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", plugins.SocketPath)
	}}}
	resp, err := local.Get("http://goblind/api/v1/plugin/self")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("non-plugin caller: %d", resp.StatusCode)
	}

	// the sandbox: not root, no privileges, a read-only system
	pid := strings.TrimSpace(e2eRun(t, "systemctl", "show", "-P", "MainPID", unit))
	status, err := os.ReadFile("/proc/" + pid + "/status")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(status), "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) > 1 && f[0] == "Uid:" && f[1] == "0":
			t.Fatalf("plugin runs as root: %s", line)
		case len(f) > 1 && f[0] == "NoNewPrivs:" && f[1] != "1":
			t.Fatalf("NoNewPrivs not set: %s", line)
		case len(f) > 1 && f[0] == "CapEff:" && f[1] != "0000000000000000":
			t.Fatalf("plugin has capabilities: %s", line)
		}
	}
	if out := e2eRun(t, "systemctl", "show", "-P", "ProtectSystem", unit); strings.TrimSpace(out) != "strict" {
		t.Fatalf("ProtectSystem=%s", out)
	}
	if _, err := os.Stat("/var/lib/goblin-plugin-hello/started"); err != nil {
		t.Fatalf("state directory: %v", err)
	}

	// the plugin's admin API through goblind's proxy (root reaches the
	// dynamic user's runtime directory)
	admin := httptest.NewUnstartedServer(srv.Routes())
	admin.Config.ConnContext = WithSocketConn // as root over the CLI socket
	admin.Start()
	defer admin.Close()
	var answer map[string]string
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get(admin.URL + "/api/v1/plugins/hello/api/status")
		if err == nil && resp.StatusCode == 200 {
			json.NewDecoder(resp.Body).Decode(&answer)
			resp.Body.Close()
			break
		}
		why := fmt.Sprint(err)
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			why = fmt.Sprintf("%d %s", resp.StatusCode, b)
		}
		if time.Now().After(deadline) {
			ls, _ := exec.Command("ls", "-la", "/run/goblin-plugin-hello").CombinedOutput()
			t.Fatalf("plugin api: %s\n%s", why, ls)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if answer["greeting"] != "e2e-hi" {
		t.Fatalf("plugin api answered %v", answer)
	}

	// approving config:read restarts it with the new capability
	commitPlugins(config.Plugin{Name: "hello", Enabled: true, Capabilities: []string{"events:read", "notify:send", "config:read"},
		Settings: map[string]any{"greeting": "e2e-again", "interval_seconds": float64(10)}})
	waitJournal("hello: e2e-again, I am hello", "hello: read config: status 200")

	audit, _ := os.ReadFile(filepath.Join(dir, "audit.log"))
	for _, want := range []string{`"user":"plugin:hello"`, `"result":"allowed"`, `"result":"denied"`, `"path":"/api/v1/config/running"`} {
		if !strings.Contains(string(audit), want) {
			t.Fatalf("audit lacks %s:\n%s", want, audit)
		}
	}

	// disabled: stopped, no longer answered
	commitPlugins(config.Plugin{Name: "hello"})
	if st := strings.TrimSpace(e2eRun(t, "systemctl", "show", "-P", "ActiveState", unit)); st != "inactive" {
		t.Fatalf("after disable: %s", st)
	}
	if _, ok := mgr.Identify("hello"); ok {
		t.Fatal("disabled plugin still identified")
	}
	t.Logf("journal:\n%s", journal())
}

func e2eRun(t *testing.T, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Stderr = io.Discard
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %v: %v", name, args, err)
	}
	return string(out)
}
