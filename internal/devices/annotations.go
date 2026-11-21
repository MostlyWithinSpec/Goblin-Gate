package devices

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/networkgoblinlabs/goblin-gate/internal/dhcpdns"
)

// Annotations are a few named fields plugins attach to devices (capability
// devices:annotate), such as the switch port or the Wi-Fi network a UniFi
// controller reports. They are shown next to the device, read-only, and
// never used for matching. Each plugin owns its own set and replaces it as a
// whole; they live in a state file of their own, not in the inventory.

// Limits on one plugin's annotations.
const (
	MaxAnnotatedDevices = 4096
	MaxAnnotationFields = 8
	MaxAnnotationValue  = 64 // characters
)

var annotationKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,23}$`)

// Fields are one device's annotations from one plugin.
type Fields map[string]string

// PluginAnnotations are one plugin's annotations, by MAC.
type PluginAnnotations struct {
	Devices map[string]Fields `json:"devices"`
	Updated time.Time         `json:"updated,omitzero"`
}

// AnnotationStore keeps every plugin's annotations.
type AnnotationStore struct {
	Path string // state file, e.g. /var/lib/goblingate/device-annotations.json
	Now  func() time.Time

	mu sync.Mutex
	by map[string]*PluginAnnotations
}

// Load reads the state file; a missing file is an empty store.
func (s *AnnotationStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.by = map[string]*PluginAnnotations{}
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &s.by); err != nil {
		return fmt.Errorf("device annotations: %s: %w", s.Path, err)
	}
	return nil
}

// CheckAnnotations normalizes and checks a plugin's full set: MACs take the
// inventory's form (12 lowercase hex digits), empty field lists are dropped.
func CheckAnnotations(in map[string]Fields) (map[string]Fields, error) {
	if len(in) > MaxAnnotatedDevices {
		return nil, fmt.Errorf("at most %d devices", MaxAnnotatedDevices)
	}
	out := make(map[string]Fields, len(in))
	for raw, f := range in {
		hw, err := net.ParseMAC(raw)
		if err != nil || len(hw) != 6 {
			return nil, fmt.Errorf("%q is not a MAC address", raw)
		}
		mac := dhcpdns.NormalizeMAC(hw.String()) // the inventory's form
		if len(f) == 0 {
			continue
		}
		if len(f) > MaxAnnotationFields {
			return nil, fmt.Errorf("%s: at most %d fields", mac, MaxAnnotationFields)
		}
		clean := make(Fields, len(f))
		for k, v := range f {
			if !annotationKeyRe.MatchString(k) {
				return nil, fmt.Errorf("%s: field name %q: lowercase letters, digits and _, at most 24", mac, k)
			}
			v = strings.TrimSpace(v)
			if !utf8.ValidString(v) || utf8.RuneCountInString(v) > MaxAnnotationValue || strings.ContainsFunc(v, unicode.IsControl) {
				return nil, fmt.Errorf("%s: field %s: at most %d printable characters", mac, k, MaxAnnotationValue)
			}
			if v != "" {
				clean[k] = v
			}
		}
		if len(clean) > 0 {
			out[mac] = clean
		}
	}
	return out, nil
}

// Get returns a copy of one plugin's annotations.
func (s *AnnotationStore) Get(plugin string) PluginAnnotations {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.by[plugin]
	if p == nil {
		return PluginAnnotations{Devices: map[string]Fields{}}
	}
	return PluginAnnotations{Devices: cloneSet(p.Devices), Updated: p.Updated}
}

// Set replaces one plugin's annotations (already checked) and reports
// whether anything changed.
func (s *AnnotationStore) Set(plugin string, set map[string]Fields) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.by == nil {
		s.by = map[string]*PluginAnnotations{}
	}
	if cur := s.by[plugin]; cur != nil && sameSet(cur.Devices, set) {
		return false, nil
	}
	if cur := s.by[plugin]; cur == nil && len(set) == 0 {
		return false, nil
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	s.by[plugin] = &PluginAnnotations{Devices: cloneSet(set), Updated: now.UTC()}
	return true, s.saveLocked()
}

// For returns the annotations of one device by the plugins in keep (nil =
// none).
func (s *AnnotationStore) For(mac string, keep func(plugin string) bool) map[string]Fields {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out map[string]Fields
	for plugin, p := range s.by {
		if f, ok := p.Devices[mac]; ok && keep(plugin) {
			if out == nil {
				out = map[string]Fields{}
			}
			out[plugin] = maps.Clone(f)
		}
	}
	return out
}

// Updated returns when each plugin in keep last changed its annotations.
func (s *AnnotationStore) Updated(keep func(plugin string) bool) map[string]time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]time.Time{}
	for plugin, p := range s.by {
		if keep(plugin) {
			out[plugin] = p.Updated
		}
	}
	return out
}

// Prune drops the annotations of plugins not in keep, returning their names.
func (s *AnnotationStore) Prune(keep func(plugin string) bool) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var dropped []string
	for plugin := range s.by {
		if !keep(plugin) {
			delete(s.by, plugin)
			dropped = append(dropped, plugin)
		}
	}
	if len(dropped) > 0 {
		slices.Sort(dropped)
		_ = s.saveLocked()
	}
	return dropped
}

func (s *AnnotationStore) saveLocked() error {
	if s.Path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s.by, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o750); err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

func cloneSet(in map[string]Fields) map[string]Fields {
	out := make(map[string]Fields, len(in))
	for k, v := range in {
		out[k] = maps.Clone(v)
	}
	return out
}

func sameSet(a, b map[string]Fields) bool {
	return maps.EqualFunc(a, b, func(x, y Fields) bool { return maps.Equal(x, y) })
}
