package config

import (
	"fmt"
	"regexp"
	"slices"
	"sync"
)

// Plugin enables an installed plugin (docs/dev/plugins.md). Capabilities
// are the ones the admin approved; the plugin gets no others. Settings are
// checked against the schema in the plugin's manifest.
type Plugin struct {
	Name         string         `json:"name"`
	Enabled      bool           `json:"enabled"`
	Capabilities []string       `json:"capabilities,omitempty"`
	Settings     map[string]any `json:"settings,omitempty"`
}

// PluginCapabilities are the capabilities a plugin can ask for.
var PluginCapabilities = []string{"events:read", "stats:read", "config:read", "notify:send", "blocks:read", "blocks:write"}

var pluginNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// ValidPluginName reports whether name can name a plugin (it becomes part of
// a systemd unit name and file paths).
func ValidPluginName(name string) bool { return pluginNameRe.MatchString(name) }

// PluginSpec is what validation needs to know about an installed plugin.
type PluginSpec struct {
	// Problem is why the plugin cannot run (an invalid manifest); "" = fine.
	Problem string
	// Capabilities are the ones its manifest asks for.
	Capabilities []string
	// CheckSettings returns the problems of a settings object, each
	// "key: message" (nil = valid).
	CheckSettings func(map[string]any) []string
}

var (
	catalogMu     sync.RWMutex
	pluginCatalog func(name string) (PluginSpec, bool)
)

// SetPluginCatalog makes Validate check plugins against the installed
// ones: lookup returns a plugin's spec, false when it is not installed.
// Without a catalog (tests, dev) only the format is checked.
func SetPluginCatalog(lookup func(name string) (PluginSpec, bool)) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	pluginCatalog = lookup
}

func (v *validator) checkPlugins() {
	seen := map[string]bool{}
	for i, p := range v.cfg.Plugins {
		path := fmt.Sprintf("plugins[%d]", i)
		if !ValidPluginName(p.Name) {
			v.add(path+".name", "plugin name must be 1-32 of a-z, 0-9 and -, starting with a letter")
			continue
		}
		if seen[p.Name] {
			v.add(path+".name", "plugin %q is listed twice", p.Name)
		}
		seen[p.Name] = true
		for j, c := range p.Capabilities {
			if !slices.Contains(PluginCapabilities, c) {
				v.add(fmt.Sprintf("%s.capabilities[%d]", path, j), "unknown capability %q", c)
			}
		}
	}
}

// CheckInstalledPlugins checks cfg's plugins against the installed ones
// (SetPluginCatalog): an enabled plugin must be installed and valid, its
// approved capabilities must be ones it asks for, and its settings must
// match its schema. It is separate from Validate on purpose: installing or
// upgrading a plugin package must never make an older config unusable for
// a rollback, so only edits and commits of the candidate run it; the
// plugin manager skips plugins that no longer fit at runtime.
func CheckInstalledPlugins(cfg *Config) []ValidationError {
	catalogMu.RLock()
	lookup := pluginCatalog
	catalogMu.RUnlock()
	if lookup == nil {
		return nil
	}
	v := &validator{cfg: cfg}
	for i, p := range cfg.Plugins {
		if !ValidPluginName(p.Name) {
			continue
		}
		path := fmt.Sprintf("plugins[%d]", i)
		spec, installed := lookup(p.Name)
		if !installed {
			if p.Enabled {
				v.add(path+".name", "plugin %q is not installed", p.Name)
			}
			continue
		}
		if spec.Problem != "" {
			if p.Enabled {
				v.add(path+".name", "plugin %q cannot run: %s", p.Name, spec.Problem)
			}
			continue
		}
		for j, c := range p.Capabilities {
			if slices.Contains(PluginCapabilities, c) && !slices.Contains(spec.Capabilities, c) {
				v.add(fmt.Sprintf("%s.capabilities[%d]", path, j), "plugin %q does not ask for %q", p.Name, c)
			}
		}
		if spec.CheckSettings != nil {
			for _, msg := range spec.CheckSettings(p.Settings) {
				v.add(path+".settings", "%s", msg)
			}
		}
	}
	return v.errs
}

// ValidateCandidate is Validate plus CheckInstalledPlugins: what an edit or
// commit of the candidate must pass.
func ValidateCandidate(cfg *Config) []ValidationError {
	return append(Validate(cfg), CheckInstalledPlugins(cfg)...)
}
