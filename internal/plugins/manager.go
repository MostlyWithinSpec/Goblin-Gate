package plugins

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"slices"
	"sort"
	"sync"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// SocketPath is the API socket plugins call (GOBLIN_PLUGIN_SOCKET).
const SocketPath = "/run/goblin-plugins/api.sock"

// AdminSocket is where a plugin may serve an API for the admin UI
// (GOBLIN_PLUGIN_API_SOCKET); goblind proxies /api/v1/plugins/<name>/api/
// to it.
func (m *Manager) AdminSocket(name string) string {
	dir := m.RuntimeDir
	if dir == "" {
		dir = "/run"
	}
	return filepath.Join(dir, "goblin-plugin-"+name, "api.sock")
}

// Unit returns a plugin's systemd unit.
func Unit(name string) string { return "goblin-plugin@" + name + ".service" }

// Systemd starts and stops plugin units.
type Systemd interface {
	Restart(ctx context.Context, unit string) error
	Stop(ctx context.Context, unit string) error
	// State returns the unit's ActiveState (active, failed, inactive, ...).
	State(ctx context.Context, unit string) string
}

// Identity is an authenticated plugin.
type Identity struct {
	Name         string
	Capabilities []string
}

// Can reports whether the plugin holds a capability.
func (id Identity) Can(capability string) bool { return slices.Contains(id.Capabilities, capability) }

type running struct {
	caps  []string
	stamp string // what the plugin was started with
}

// Manager keeps the plugin units in line with the running config and
// tells API calls from running plugins what they may do.
type Manager struct {
	ManifestDir, ProgramDir string
	// RuntimeDir holds each unit's runtime directory (default /run), where
	// a plugin with an admin API listens (AdminSocket).
	RuntimeDir string
	Version    string // goblind's package version (min_goblind)
	Systemd    Systemd
	Logger     *slog.Logger

	mu        sync.Mutex
	installed map[string]*Installed
	running   map[string]*running
	pending   *config.Config // newest config waiting for Resync's worker
	syncing   bool
	syncMu    sync.Mutex // one Sync at a time
}

func (m *Manager) logger() *slog.Logger {
	if m.Logger != nil {
		return m.Logger
	}
	return slog.Default()
}

// Rescan reads the installed plugins again (package installs happen
// behind goblind's back) and returns them.
func (m *Manager) Rescan() map[string]*Installed {
	in := Scan(m.ManifestDir, m.ProgramDir, m.Version)
	m.mu.Lock()
	m.installed = in
	m.mu.Unlock()
	return in
}

// Lookup is the config.SetPluginCatalog lookup: it rescans so a plugin
// installed a moment ago validates.
func (m *Manager) Lookup(name string) (config.PluginSpec, bool) {
	in, ok := m.Rescan()[name]
	if !ok {
		return config.PluginSpec{}, false
	}
	return in.Spec(), true
}

// Installed returns one installed plugin from the last scan.
func (m *Manager) Installed(name string) (*Installed, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	in, ok := m.installed[name]
	return in, ok
}

// Resync runs Sync in the background (systemctl can take a while; the
// commit engine calls this with its lock held). Calls made while a Sync is
// running collapse into one Sync with the newest config.
func (m *Manager) Resync(ctx context.Context, cfg *config.Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending = cfg
	if m.syncing {
		return
	}
	m.syncing = true
	go func() {
		for {
			m.mu.Lock()
			next := m.pending
			m.pending = nil
			if next == nil {
				m.syncing = false
				m.mu.Unlock()
				return
			}
			m.mu.Unlock()
			m.Sync(ctx, next)
		}
	}()
}

// Sync starts, restarts and stops plugin units to match cfg. A plugin is
// restarted when its capabilities or settings changed (it reads them once,
// at start) or it is not running. The first Sync after goblind starts
// restarts every enabled plugin, so each starts with the current config.
func (m *Manager) Sync(ctx context.Context, cfg *config.Config) {
	m.syncMu.Lock()
	defer m.syncMu.Unlock()
	installed := m.Rescan()
	want := map[string]config.Plugin{}
	for _, p := range cfg.Plugins {
		in, ok := installed[p.Name]
		if !p.Enabled || !ok || in.Problem != "" {
			continue
		}
		// The running config may predate a package upgrade (a rollback
		// skips the plugin checks): grant only what the manifest still
		// asks for, and do not start a plugin whose settings no longer fit.
		if msgs := in.Manifest.CheckSettings(p.Settings); len(msgs) > 0 {
			m.logger().Warn("plugins: settings do not fit the installed version, not starting", "plugin", p.Name, "problems", msgs)
			continue
		}
		p.Capabilities = slices.DeleteFunc(slices.Clone(p.Capabilities), func(c string) bool {
			return !slices.Contains(in.Manifest.Capabilities, c)
		})
		want[p.Name] = p
	}
	m.mu.Lock()
	if m.running == nil {
		m.running = map[string]*running{}
	}
	var stop []string
	for name := range m.running {
		if _, ok := want[name]; !ok {
			stop = append(stop, name)
		}
	}
	m.mu.Unlock()
	sort.Strings(stop)
	for _, name := range stop {
		m.stop(ctx, name)
	}
	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := want[name]
		stamp := stampOf(p)
		m.mu.Lock()
		cur := m.running[name]
		m.mu.Unlock()
		if cur != nil && cur.stamp == stamp && m.Systemd.State(ctx, Unit(name)) == "active" {
			continue
		}
		m.start(ctx, p, stamp)
	}
}

func stampOf(p config.Plugin) string {
	b, _ := json.Marshal(struct {
		C []string       `json:"c"`
		S map[string]any `json:"s"`
	}{p.Capabilities, p.Settings})
	return string(b)
}

func (m *Manager) start(ctx context.Context, p config.Plugin, stamp string) {
	m.mu.Lock()
	m.running[p.Name] = &running{caps: slices.Clone(p.Capabilities), stamp: stamp}
	m.mu.Unlock()
	if err := m.Systemd.Restart(ctx, Unit(p.Name)); err != nil {
		m.logger().Warn("plugins: start failed", "plugin", p.Name, "error", err)
		return
	}
	m.logger().Info("plugins: started", "plugin", p.Name, "capabilities", p.Capabilities)
}

func (m *Manager) stop(ctx context.Context, name string) {
	m.mu.Lock()
	delete(m.running, name)
	m.mu.Unlock()
	if err := m.Systemd.Stop(ctx, Unit(name)); err != nil {
		m.logger().Warn("plugins: stop failed", "plugin", name, "error", err)
	}
	m.logger().Info("plugins: stopped", "plugin", name)
}

// Identify returns what an enabled, running plugin may do; false for a
// plugin goblind did not start (disabled, or started by hand).
func (m *Manager) Identify(name string) (Identity, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.running[name]
	if !ok {
		return Identity{}, false
	}
	return Identity{Name: name, Capabilities: slices.Clone(r.caps)}, true
}

// Status is a plugin's state for the API.
type Status struct {
	Name         string         `json:"name"`
	Version      string         `json:"version,omitempty"`
	Description  string         `json:"description,omitempty"`
	Requested    []string       `json:"requested_capabilities"`
	Capabilities []string       `json:"capabilities"` // approved in the config
	Enabled      bool           `json:"enabled"`
	Settings     map[string]any `json:"settings,omitempty"`
	Schema       *Schema        `json:"settings_schema,omitempty"`
	UI           *UI            `json:"ui,omitempty"`
	// State: running | stopped | failed | invalid | not_installed
	State   string `json:"state"`
	Problem string `json:"problem,omitempty"`
}

// List reports every installed plugin plus configured ones that are gone.
func (m *Manager) List(ctx context.Context, cfg *config.Config) []Status {
	installed := m.Rescan()
	byName := map[string]config.Plugin{}
	for _, p := range cfg.Plugins {
		byName[p.Name] = p
	}
	var out []Status
	for name, in := range installed {
		p := byName[name]
		st := Status{Name: name, Enabled: p.Enabled, Capabilities: orEmpty(p.Capabilities), Settings: p.Settings, Requested: []string{}, Problem: in.Problem}
		if mf := in.Manifest; mf != nil {
			st.Version, st.Description, st.Schema, st.UI = mf.Version, mf.Description, mf.SettingsSchema, mf.UI
			st.Requested = orEmpty(mf.Capabilities)
		}
		switch {
		case in.Problem != "":
			st.State = "invalid"
		default:
			switch m.Systemd.State(ctx, Unit(name)) {
			case "active", "activating", "reloading":
				st.State = "running"
			case "failed":
				st.State = "failed"
			default:
				st.State = "stopped"
			}
		}
		out = append(out, st)
	}
	for name, p := range byName {
		if _, ok := installed[name]; !ok {
			out = append(out, Status{Name: name, Enabled: p.Enabled, Capabilities: orEmpty(p.Capabilities), Requested: []string{}, Settings: p.Settings, State: "not_installed"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
