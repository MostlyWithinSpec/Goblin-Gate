package plugins

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.0", "0.1.0", 0},
		{"0.1.0~alpha.3", "0.1.0", -1},
		{"0.1.0~alpha.3", "0.1.0~alpha.2", 1},
		{"0.1.0~alpha.10", "0.1.0~alpha.9", 1},
		{"0.1.0+3.gabc", "0.1.0", 1},
		{"1.0", "0.9.9", 1},
		{"1:0.1", "2.0", 1},
		{"1.0-2", "1.0-10", -1},
		{"1.0a", "1.0", 1},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
		if got := CompareVersions(c.b, c.a); got != -c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.b, c.a, got, -c.want)
		}
	}
}

const helloManifest = `{
  "name": "hello",
  "version": "1.0.0",
  "description": "Example plugin",
  "min_goblind": "0.1.0~alpha.3",
  "capabilities": ["events:read", "notify:send"],
  "settings_schema": {
    "type": "object",
    "properties": {
      "greeting": {"type": "string", "default": "hi", "maxLength": 8},
      "interval_seconds": {"type": "integer", "minimum": 10, "maximum": 3600},
      "mode": {"type": "string", "enum": ["a", "b"]},
      "tags": {"type": "array", "items": {"type": "string"}}
    },
    "required": ["greeting"]
  },
  "ui": {"pages": [{"path": "status.html", "title": "Hello status"}]}
}`

func TestParseManifest(t *testing.T) {
	m, err := ParseManifest([]byte(helloManifest), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "hello" || len(m.Capabilities) != 2 || m.UI == nil {
		t.Fatalf("manifest: %+v", m)
	}
	bad := map[string]string{
		"unknown field":      `{"name":"hello","version":"1","bogus":1}`,
		"wrong dir":          `{"name":"other","version":"1"}`,
		"bad name":           `{"name":"Hello","version":"1"}`,
		"bad version":        `{"name":"hello","version":"v1"}`,
		"unknown capability": `{"name":"hello","version":"1","capabilities":["root:all"]}`,
		"twice":              `{"name":"hello","version":"1","capabilities":["stats:read","stats:read"]}`,
		"schema type":        `{"name":"hello","version":"1","settings_schema":{"type":"array"}}`,
		"property type":      `{"name":"hello","version":"1","settings_schema":{"type":"object","properties":{"x":{"type":"object"}}}}`,
		"array of numbers":   `{"name":"hello","version":"1","settings_schema":{"type":"object","properties":{"x":{"type":"array","items":{"type":"number"}}}}}`,
		"bad default":        `{"name":"hello","version":"1","settings_schema":{"type":"object","properties":{"x":{"type":"integer","default":"1"}}}}`,
		"required missing":   `{"name":"hello","version":"1","settings_schema":{"type":"object","required":["x"]}}`,
		"page traversal":     `{"name":"hello","version":"1","ui":{"pages":[{"path":"../etc","title":"x"}]}}`,
		"page no title":      `{"name":"hello","version":"1","ui":{"pages":[{"path":"status"}]}}`,
	}
	for what, data := range bad {
		if _, err := ParseManifest([]byte(data), "hello"); err == nil {
			t.Errorf("%s: accepted", what)
		}
	}
}

func TestCheckSettings(t *testing.T) {
	m, err := ParseManifest([]byte(helloManifest), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if msgs := m.CheckSettings(nil); len(msgs) != 0 {
		t.Fatalf("defaults cover required: %v", msgs)
	}
	ok := map[string]any{"greeting": "yo", "interval_seconds": float64(60), "mode": "b", "tags": []any{"x"}}
	if msgs := m.CheckSettings(ok); len(msgs) != 0 {
		t.Fatalf("valid settings refused: %v", msgs)
	}
	bad := map[string]any{"greeting": "far too long", "interval_seconds": 1.5, "mode": "c", "tags": []any{1}, "extra": true}
	msgs := m.CheckSettings(bad)
	for _, want := range []string{"extra: unknown setting", "greeting: at most 8", "interval_seconds: must be a whole number", "mode: must be one of", "tags: must be a list of strings"} {
		if !slices.ContainsFunc(msgs, func(s string) bool { return strings.HasPrefix(s, want) }) {
			t.Errorf("missing %q in %v", want, msgs)
		}
	}
	if msgs := m.CheckSettings(map[string]any{"interval_seconds": float64(5)}); len(msgs) != 1 || !strings.Contains(msgs[0], "at least 10") {
		t.Errorf("minimum: %v", msgs)
	}
	eff := m.Effective(map[string]any{"mode": "a"})
	if eff["greeting"] != "hi" || eff["mode"] != "a" {
		t.Errorf("effective: %v", eff)
	}
}

// fakeSystemd records unit actions.
type fakeSystemd struct {
	mu      sync.Mutex
	actions []string
	active  map[string]bool
}

func (f *fakeSystemd) Restart(_ context.Context, unit string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, "restart "+unit)
	f.active[unit] = true
	return nil
}

func (f *fakeSystemd) Stop(_ context.Context, unit string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, "stop "+unit)
	delete(f.active, unit)
	return nil
}

func (f *fakeSystemd) State(_ context.Context, unit string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active[unit] {
		return "active"
	}
	return "inactive"
}

func (f *fakeSystemd) take() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.actions
	f.actions = nil
	return out
}

// installPlugin puts a plugin on disk like its package would.
func installPlugin(t *testing.T, m *Manager, name, manifest string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(m.ManifestDir, name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.ManifestDir, name, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(m.ProgramDir, name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.ProgramDir, name, "run"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func newTestManager(t *testing.T) (*Manager, *fakeSystemd) {
	dir := t.TempDir()
	sd := &fakeSystemd{active: map[string]bool{}}
	return &Manager{ManifestDir: filepath.Join(dir, "share"), ProgramDir: filepath.Join(dir, "lib"),
		Version: "0.1.0~alpha.3", Systemd: sd}, sd
}

func TestScan(t *testing.T) {
	m, _ := newTestManager(t)
	installPlugin(t, m, "hello", helloManifest)
	installPlugin(t, m, "future", strings.Replace(strings.Replace(helloManifest, `"hello"`, `"future"`, 1), "0.1.0~alpha.3", "0.2.0", 1))
	installPlugin(t, m, "broken", `{"name":"broken"`)
	installPlugin(t, m, "norun", strings.Replace(helloManifest, `"hello"`, `"norun"`, 1))
	if err := os.Remove(filepath.Join(m.ProgramDir, "norun", "run")); err != nil {
		t.Fatal(err)
	}
	in := m.Rescan()
	if in["hello"] == nil || in["hello"].Problem != "" {
		t.Fatalf("hello: %+v", in["hello"])
	}
	if p := in["future"].Problem; !strings.Contains(p, "needs Goblin Gate 0.2.0") {
		t.Errorf("future: %q", p)
	}
	if in["broken"].Problem == "" || in["norun"].Problem == "" {
		t.Errorf("broken %q, norun %q", in["broken"].Problem, in["norun"].Problem)
	}
	if runtime.GOOS != "windows" { // no exec bit on Windows
		if err := os.Chmod(filepath.Join(m.ProgramDir, "hello", "run"), 0o644); err != nil {
			t.Fatal(err)
		}
		if p := m.Rescan()["hello"].Problem; !strings.Contains(p, "not executable") {
			t.Errorf("not executable: %q", p)
		}
	}
}

func TestSyncUnitsAndIdentify(t *testing.T) {
	m, sd := newTestManager(t)
	installPlugin(t, m, "hello", helloManifest)
	ctx := context.Background()
	cfg := &config.Config{Plugins: []config.Plugin{{Name: "hello", Enabled: true, Capabilities: []string{"events:read"}}}}

	if _, ok := m.Identify("hello"); ok {
		t.Fatal("identified before it was started")
	}
	m.Sync(ctx, cfg)
	if got := sd.take(); !slices.Equal(got, []string{"restart goblin-plugin@hello.service"}) {
		t.Fatalf("first sync: %v", got)
	}
	id, ok := m.Identify("hello")
	if !ok || id.Name != "hello" || !id.Can("events:read") || id.Can("notify:send") {
		t.Fatalf("identify: %+v %v", id, ok)
	}
	if _, ok := m.Identify("other"); ok {
		t.Fatal("unknown plugin identified")
	}

	// nothing changed: nothing happens
	m.Sync(ctx, cfg)
	if got := sd.take(); len(got) != 0 {
		t.Fatalf("unchanged sync: %v", got)
	}

	// new capability: restarted, granted at once
	cfg.Plugins[0].Capabilities = []string{"events:read", "notify:send"}
	m.Sync(ctx, cfg)
	if got := sd.take(); len(got) != 1 {
		t.Fatalf("changed sync: %v", got)
	}
	if id, _ := m.Identify("hello"); !id.Can("notify:send") {
		t.Fatal("new capability not granted")
	}

	// a capability the manifest no longer asks for is not granted
	cfg.Plugins[0].Capabilities = []string{"events:read", "config:read"}
	m.Sync(ctx, cfg)
	sd.take()
	if id, _ := m.Identify("hello"); id.Can("config:read") {
		t.Fatal("capability outside the manifest granted")
	}

	// settings that do not fit: stopped and no longer identified
	cfg.Plugins[0].Settings = map[string]any{"bogus": 1}
	m.Sync(ctx, cfg)
	if got := sd.take(); !slices.Equal(got, []string{"stop goblin-plugin@hello.service"}) {
		t.Fatalf("bad settings: %v", got)
	}
	if _, ok := m.Identify("hello"); ok {
		t.Fatal("stopped plugin still identified")
	}

	// disabled: stays stopped
	cfg.Plugins[0].Settings = nil
	cfg.Plugins[0].Enabled = false
	m.Sync(ctx, cfg)
	if got := sd.take(); len(got) != 0 {
		t.Fatalf("disabled: %v", got)
	}
}

func TestUnitFromCgroup(t *testing.T) {
	cases := map[string]string{
		`0::/system.slice/system-goblin\x2dplugin.slice/goblin-plugin@hello.service` + "\n":         "hello",
		`0::/docker/abc/system.slice/system-goblin\x2dplugin.slice/goblin-plugin@sentry.service`:    "sentry",
		"12:pids:/x\n" + `0::/system.slice/system-goblin\x2dplugin.slice/goblin-plugin@a-b.service`: "a-b",
		"0::/system.slice/goblind.service":                 "",
		"0::/system.slice/goblin-plugin@hello.service/sub": "",
		"0::/user.slice/goblin-plugin@hello.scope":         "",
		"0::/system.slice/goblin-plugin@Bad.service":       "",
		"0::/system.slice/goblin-plugin@.service":          "",
		"": "",
	}
	for in, want := range cases {
		got, ok := unitFromCgroup(in)
		if got != want || ok != (want != "") {
			t.Errorf("unitFromCgroup(%q) = %q %v, want %q", in, got, ok, want)
		}
	}
}

func TestResyncUsesNewest(t *testing.T) {
	m, sd := newTestManager(t)
	installPlugin(t, m, "hello", helloManifest)
	ctx := context.Background()
	on := &config.Config{Plugins: []config.Plugin{{Name: "hello", Enabled: true}}}
	off := &config.Config{Plugins: []config.Plugin{{Name: "hello"}}}
	m.Resync(ctx, on)
	m.Resync(ctx, off)
	m.Resync(ctx, on)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		done := !m.syncing
		m.mu.Unlock()
		if done {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if sd.State(ctx, Unit("hello")) != "active" {
		t.Fatalf("hello should run after the last Resync: %v", sd.take())
	}
}

func TestList(t *testing.T) {
	m, sd := newTestManager(t)
	installPlugin(t, m, "hello", helloManifest)
	installPlugin(t, m, "broken", `{}`)
	sd.active[Unit("hello")] = true
	cfg := &config.Config{Plugins: []config.Plugin{{Name: "hello", Enabled: true, Capabilities: []string{"events:read"}}, {Name: "gone", Enabled: true}}}
	list := m.List(context.Background(), cfg)
	if len(list) != 3 {
		t.Fatalf("list: %+v", list)
	}
	states := map[string]string{}
	for _, s := range list {
		states[s.Name] = s.State
	}
	if states["hello"] != "running" || states["broken"] != "invalid" || states["gone"] != "not_installed" {
		t.Fatalf("states: %v", states)
	}
	if list[2].Name != "hello" || len(list[2].Requested) != 2 || list[2].Schema == nil {
		t.Fatalf("hello: %+v", list[2])
	}
}

func TestRoutesAreKnownCapabilities(t *testing.T) {
	for route, c := range Routes {
		if c != "" && !slices.Contains(config.PluginCapabilities, c) {
			t.Errorf("%s needs unknown capability %q", route, c)
		}
		if strings.Contains(route, "/config/candidate") || strings.Contains(route, "commit") || strings.Contains(route, "/auth/") {
			t.Errorf("%s must not be reachable by plugins", route)
		}
	}
}
