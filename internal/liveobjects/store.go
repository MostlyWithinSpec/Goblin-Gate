// Package liveobjects keeps the members of live address objects: address
// objects of type "live" whose addresses a plugin sets at runtime (capability
// objects:write), such as the VMs and containers a homelab plugin finds.
//
// The members never enter the config: they live in a state file, survive
// restarts, and reach nftables through the runtime set refill
// (geoip.Manager.RefillLive). An object nobody has filled yet is empty, so it
// matches nothing.
package liveobjects

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Limits on what one plugin may put in an object.
const (
	// MaxMembers per object.
	MaxMembers = 4096
	// MinBits4 and MinBits6 are the widest prefixes a member may be (/16 and
	// /48): a plugin keeps hosts and small networks, never "everything".
	MinBits4 = 16
	MinBits6 = 48
)

// Object is one live object's current state.
type Object struct {
	Name    string         `json:"name"`
	Plugin  string         `json:"plugin"`
	Members []netip.Prefix `json:"members"`
	Updated time.Time      `json:"updated,omitzero"`
}

// Store holds the members of every live object.
type Store struct {
	Path string // state file, e.g. /var/lib/goblingate/live-objects.json
	Now  func() time.Time

	mu   sync.Mutex
	objs map[string]*Object
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Load reads the state file; a missing file is an empty store.
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objs = map[string]*Object{}
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var list []Object
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("live objects: %s: %w", s.Path, err)
	}
	for _, o := range list {
		o := o
		s.objs[o.Name] = &o
	}
	return nil
}

func (s *Store) saveLocked() error {
	if s.Path == "" {
		return nil
	}
	list := make([]Object, 0, len(s.objs))
	for _, o := range s.objs {
		list = append(list, *o)
	}
	slices.SortFunc(list, func(a, b Object) int { return compare(a.Name, b.Name) })
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

func compare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Members returns an object's current members (nil when it has none).
func (s *Store) Members(name string) []netip.Prefix {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o, ok := s.objs[name]; ok {
		return slices.Clone(o.Members)
	}
	return nil
}

// Contains reports whether a is a current member of the object.
func (s *Store) Contains(name string, a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range s.Members(name) {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// Get returns one object's state.
func (s *Store) Get(name string) (Object, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objs[name]
	if !ok {
		return Object{}, false
	}
	c := *o
	c.Members = slices.Clone(o.Members)
	return c, true
}

// ParseMembers turns a plugin's member list into sorted, unique prefixes:
// hosts (IPv4 or IPv6) and prefixes no wider than MinBits4/MinBits6.
func ParseMembers(list []string) ([]netip.Prefix, error) {
	if len(list) > MaxMembers {
		return nil, fmt.Errorf("%d members, at most %d", len(list), MaxMembers)
	}
	out := make([]netip.Prefix, 0, len(list))
	for _, m := range list {
		var p netip.Prefix
		if a, err := netip.ParseAddr(m); err == nil {
			if a.Zone() != "" {
				return nil, fmt.Errorf("member %q: no zone", m)
			}
			a = a.Unmap()
			p = netip.PrefixFrom(a, a.BitLen())
		} else if q, err := netip.ParsePrefix(m); err == nil {
			p = q.Masked()
			if p.Addr().Is4In6() {
				return nil, fmt.Errorf("member %q: write IPv4 as plain IPv4", m)
			}
		} else {
			return nil, fmt.Errorf("member %q is not an address or prefix", m)
		}
		if p.Addr().Is4() && p.Bits() < MinBits4 || p.Addr().Is6() && p.Bits() < MinBits6 {
			return nil, fmt.Errorf("member %q is wider than /%d (IPv4) or /%d (IPv6)", m, MinBits4, MinBits6)
		}
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b netip.Prefix) int {
		if c := a.Addr().Compare(b.Addr()); c != 0 {
			return c
		}
		return a.Bits() - b.Bits()
	})
	return slices.Compact(out), nil
}

// Change is what a Set did.
type Change struct {
	Changed        bool
	Added, Removed int
	Members        int
}

// Set replaces an object's members on behalf of plugin. The caller has
// checked that plugin owns the object (Owned).
func (s *Store) Set(name, plugin string, members []netip.Prefix) (Change, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.objs == nil {
		s.objs = map[string]*Object{}
	}
	old := s.objs[name]
	var before []netip.Prefix
	if old != nil && old.Plugin == plugin {
		before = old.Members
	}
	ch := Change{Members: len(members)}
	for _, p := range members {
		if !slices.Contains(before, p) {
			ch.Added++
		}
	}
	for _, p := range before {
		if !slices.Contains(members, p) {
			ch.Removed++
		}
	}
	ch.Changed = ch.Added > 0 || ch.Removed > 0 || old == nil || old.Plugin != plugin
	s.objs[name] = &Object{Name: name, Plugin: plugin, Members: slices.Clone(members), Updated: s.now()}
	if err := s.saveLocked(); err != nil {
		s.objs[name] = old
		if old == nil {
			delete(s.objs, name)
		}
		return Change{}, err
	}
	return ch, nil
}

// Owned returns the live objects of cfg and their plugins.
func Owned(cfg *config.Config) map[string]string {
	out := map[string]string{}
	for _, a := range cfg.Addresses {
		if a.Type == "live" {
			out[a.Name] = a.Value
		}
	}
	return out
}

// Prune drops the members of objects the config no longer has, or that now
// belong to another plugin, so a renamed or reassigned object starts empty.
// It returns the names it dropped.
func (s *Store) Prune(owned map[string]string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var dropped []string
	for name, o := range s.objs {
		if owner, ok := owned[name]; !ok || owner != o.Plugin {
			delete(s.objs, name)
			dropped = append(dropped, name)
		}
	}
	if len(dropped) > 0 {
		_ = s.saveLocked()
	}
	slices.Sort(dropped)
	return dropped
}

// List returns the state of every object of owned (the running config's live
// objects), filled or not, sorted by name.
func (s *Store) List(owned map[string]string) []Object {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Object, 0, len(owned))
	for name, plugin := range owned {
		o := Object{Name: name, Plugin: plugin, Members: []netip.Prefix{}}
		if cur, ok := s.objs[name]; ok && cur.Plugin == plugin {
			o.Members, o.Updated = slices.Clone(cur.Members), cur.Updated
		}
		out = append(out, o)
	}
	slices.SortFunc(out, func(a, b Object) int { return compare(a.Name, b.Name) })
	return out
}
