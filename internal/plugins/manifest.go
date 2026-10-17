// Package plugins discovers installed plugins, checks their manifests and
// settings, runs the enabled ones as goblin-plugin@<name> units and
// authenticates their calls (docs/dev/plugins.md).
package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Default locations on the box.
const (
	ManifestDir = "/usr/share/goblin-gate/plugins" // <name>/plugin.json, <name>/ui/
	ProgramDir  = "/usr/lib/goblin-gate/plugins"   // <name>/run
)

// Manifest is a plugin's plugin.json.
type Manifest struct {
	Name           string   `json:"name"`
	Version        string   `json:"version"`
	Description    string   `json:"description,omitempty"`
	MinGoblind     string   `json:"min_goblind,omitempty"`
	Capabilities   []string `json:"capabilities,omitempty"`
	SettingsSchema *Schema  `json:"settings_schema,omitempty"`
	UI             *UI      `json:"ui,omitempty"`
}

// UI lists a plugin's web pages, served from its ui/ directory.
type UI struct {
	Pages []Page `json:"pages,omitempty"`
}

// Page is one plugin page: /plugin-ui/<name>/<path>.
type Page struct {
	Path  string `json:"path"`
	Title string `json:"title"`
}

// Schema is the supported subset of JSON Schema for settings: an object of
// typed properties.
type Schema struct {
	Type       string               `json:"type"`
	Properties map[string]*Property `json:"properties,omitempty"`
	Required   []string             `json:"required,omitempty"`
}

// Property is one setting.
type Property struct {
	Type        string   `json:"type"` // string | integer | number | boolean | array (of strings)
	Description string   `json:"description,omitempty"`
	Enum        []any    `json:"enum,omitempty"`
	Minimum     *float64 `json:"minimum,omitempty"`
	Maximum     *float64 `json:"maximum,omitempty"`
	MaxLength   *int     `json:"maxLength,omitempty"`
	Default     any      `json:"default,omitempty"`
	Items       *Items   `json:"items,omitempty"`
}

// Items describes array elements (strings only).
type Items struct {
	Type string `json:"type"`
}

var (
	versionRe  = regexp.MustCompile(`^[0-9][A-Za-z0-9.+~:-]*$`)
	pagePathRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*(/[a-z0-9][a-z0-9_.-]*)*$`)
	settingRe  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

// ParseManifest decodes and checks a plugin.json; dir is the directory
// name it was found in, which must equal the plugin's name.
func ParseManifest(data []byte, dir string) (*Manifest, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("plugin.json: %w", err)
	}
	if err := m.check(dir); err != nil {
		return &m, err
	}
	return &m, nil
}

func (m *Manifest) check(dir string) error {
	var errs []string
	add := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }
	if !config.ValidPluginName(m.Name) {
		add("name %q: want 1-32 of a-z, 0-9 and -, starting with a letter", m.Name)
	} else if dir != "" && m.Name != dir {
		add("name %q does not match its directory %q", m.Name, dir)
	}
	if !versionRe.MatchString(m.Version) {
		add("version %q is not a package version", m.Version)
	}
	if m.MinGoblind != "" && !versionRe.MatchString(m.MinGoblind) {
		add("min_goblind %q is not a package version", m.MinGoblind)
	}
	seen := map[string]bool{}
	for _, c := range m.Capabilities {
		if !slices.Contains(config.PluginCapabilities, c) {
			add("unknown capability %q", c)
		}
		if seen[c] {
			add("capability %q is listed twice", c)
		}
		seen[c] = true
	}
	if s := m.SettingsSchema; s != nil {
		if s.Type != "object" {
			add("settings_schema: type must be object")
		}
		for key, p := range s.Properties {
			if !settingRe.MatchString(key) {
				add("settings_schema: property %q: want a-z, 0-9 and _", key)
			}
			if p == nil {
				add("settings_schema: property %q is empty", key)
				continue
			}
			switch p.Type {
			case "string", "integer", "number", "boolean":
			case "array":
				if p.Items == nil || p.Items.Type != "string" {
					add("settings_schema: property %q: arrays must be of strings", key)
				}
			default:
				add("settings_schema: property %q: unsupported type %q", key, p.Type)
			}
			if p.Default != nil {
				if msg := p.check(p.Default); msg != "" {
					add("settings_schema: property %q: default %s", key, msg)
				}
			}
		}
		for _, r := range s.Required {
			if _, ok := s.Properties[r]; !ok {
				add("settings_schema: required %q is not a property", r)
			}
		}
	}
	if m.UI != nil {
		for _, p := range m.UI.Pages {
			if !pagePathRe.MatchString(p.Path) || strings.Contains(p.Path, "..") {
				add("ui page path %q: want a relative path of a-z, 0-9, _, - and .", p.Path)
			}
			if p.Title == "" {
				add("ui page %q needs a title", p.Path)
			}
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// CheckSettings validates a settings object against the manifest's schema
// and returns its problems as "key: message", sorted.
func (m *Manifest) CheckSettings(settings map[string]any) []string {
	var out []string
	s := m.SettingsSchema
	if s == nil {
		for key := range settings {
			out = append(out, key+": this plugin has no settings")
		}
		sort.Strings(out)
		return out
	}
	for key, v := range settings {
		p, ok := s.Properties[key]
		if !ok || p == nil {
			out = append(out, key+": unknown setting")
			continue
		}
		if msg := p.check(v); msg != "" {
			out = append(out, key+": "+msg)
		}
	}
	for _, r := range s.Required {
		if _, ok := settings[r]; !ok {
			if p := s.Properties[r]; p == nil || p.Default == nil {
				out = append(out, r+": required")
			}
		}
	}
	sort.Strings(out)
	return out
}

// Effective returns the settings with the schema's defaults filled in.
func (m *Manifest) Effective(settings map[string]any) map[string]any {
	out := map[string]any{}
	if m.SettingsSchema != nil {
		for key, p := range m.SettingsSchema.Properties {
			if p != nil && p.Default != nil {
				out[key] = p.Default
			}
		}
	}
	for k, v := range settings {
		out[k] = v
	}
	return out
}

// check validates one value; "" = fine.
func (p *Property) check(v any) string {
	switch p.Type {
	case "string":
		s, ok := v.(string)
		if !ok {
			return "must be a string"
		}
		if p.MaxLength != nil && len([]rune(s)) > *p.MaxLength {
			return fmt.Sprintf("at most %d characters", *p.MaxLength)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return "must be true or false"
		}
	case "integer", "number":
		f, ok := number(v)
		if !ok {
			return "must be a number"
		}
		if p.Type == "integer" && f != math.Trunc(f) {
			return "must be a whole number"
		}
		if p.Minimum != nil && f < *p.Minimum {
			return fmt.Sprintf("at least %v", *p.Minimum)
		}
		if p.Maximum != nil && f > *p.Maximum {
			return fmt.Sprintf("at most %v", *p.Maximum)
		}
	case "array":
		list, ok := v.([]any)
		if !ok {
			if ss, ok2 := v.([]string); ok2 {
				for _, s := range ss {
					list = append(list, s)
				}
				ok = true
			}
		}
		if !ok {
			return "must be a list"
		}
		for _, e := range list {
			s, ok := e.(string)
			if !ok {
				return "must be a list of strings"
			}
			if p.MaxLength != nil && len([]rune(s)) > *p.MaxLength {
				return fmt.Sprintf("entries of at most %d characters", *p.MaxLength)
			}
		}
	}
	if len(p.Enum) > 0 && p.Type != "array" {
		for _, e := range p.Enum {
			if equalValue(e, v) {
				return ""
			}
		}
		return fmt.Sprintf("must be one of %v", p.Enum)
	}
	return ""
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func equalValue(a, b any) bool {
	fa, oka := number(a)
	fb, okb := number(b)
	if oka && okb {
		return fa == fb
	}
	return a == b
}

// Installed is a plugin found on disk.
type Installed struct {
	Name     string
	Manifest *Manifest // nil when plugin.json could not be read
	Problem  string    // why it cannot run; "" = fine
	Dir      string    // its manifest directory
}

// Scan reads every <dir>/<name>/plugin.json. goblindVersion gates
// min_goblind ("" or a dev build skips the check); programDir/<name>/run
// must exist.
func Scan(dir, programDir, goblindVersion string) map[string]*Installed {
	out := map[string]*Installed{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		in := &Installed{Name: name, Dir: filepath.Join(dir, name)}
		data, err := os.ReadFile(filepath.Join(in.Dir, "plugin.json"))
		if err != nil {
			in.Problem = "no readable plugin.json"
			out[name] = in
			continue
		}
		m, err := ParseManifest(data, name)
		in.Manifest = m
		switch {
		case err != nil:
			in.Problem = err.Error()
		case m.MinGoblind != "" && goblindVersion != "" && !strings.Contains(goblindVersion, "dev") &&
			CompareVersions(goblindVersion, m.MinGoblind) < 0:
			in.Problem = fmt.Sprintf("needs Goblin Gate %s or newer (this is %s)", m.MinGoblind, goblindVersion)
		default:
			if st, err := os.Stat(filepath.Join(programDir, name, "run")); err != nil || st.IsDir() || (runtime.GOOS != "windows" && st.Mode()&0o111 == 0) {
				in.Problem = "its program " + filepath.Join(programDir, name, "run") + " is missing or not executable"
			}
		}
		if config.ValidPluginName(name) {
			out[name] = in
		}
	}
	return out
}

// Spec converts an installed plugin for config validation.
func (in *Installed) Spec() config.PluginSpec {
	spec := config.PluginSpec{Problem: in.Problem}
	if in.Manifest != nil {
		spec.Capabilities = in.Manifest.Capabilities
		spec.CheckSettings = in.Manifest.CheckSettings
	}
	return spec
}
