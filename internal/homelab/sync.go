package homelab

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/pkg/pluginsdk"
)

// TagPrefix marks a guest for an object: tag or label gg-<object>.
const TagPrefix = "gg-"

// API is what the syncer needs from goblind; *pluginsdk.Client has it.
type API interface {
	LiveObjects(ctx context.Context) ([]pluginsdk.LiveObject, error)
	SetLiveObject(ctx context.Context, name string, members []string) (pluginsdk.LiveChange, error)
	Devices(ctx context.Context) ([]pluginsdk.Device, error)
}

// Syncer fills the plugin's live objects from its sources.
type Syncer struct {
	API      API
	Sources  []Source
	Settings Settings
	// Leases: look guests without reported addresses up in the device
	// inventory by MAC (capability devices:read).
	Leases bool
	Logger *slog.Logger

	lastErr string
	noted   map[string]bool // things logged once
}

// Run syncs now and then every Settings.Interval until ctx ends.
func (s *Syncer) Run(ctx context.Context) error {
	t := time.NewTicker(s.Settings.Interval)
	defer t.Stop()
	for {
		s.report(s.Once(ctx))
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// report logs a failed sync once per distinct error, and the recovery.
func (s *Syncer) report(err error) {
	switch {
	case err != nil && err.Error() != s.lastErr:
		s.Logger.Warn("homelab: sync failed, the objects keep their members", "error", err)
		s.lastErr = err.Error()
	case err == nil && s.lastErr != "":
		s.Logger.Info("homelab: sync works again")
		s.lastErr = ""
	}
}

func (s *Syncer) once(key string, msg string, args ...any) {
	if s.noted == nil {
		s.noted = map[string]bool{}
	}
	if !s.noted[key] {
		s.noted[key] = true
		s.Logger.Info(msg, args...)
	}
}

// Once reads every source and updates the objects whose members changed. A
// source that fails leaves every object as it is: an outage must not empty
// the objects the policies rely on.
func (s *Syncer) Once(ctx context.Context) error {
	objs, err := s.API.LiveObjects(ctx)
	if err != nil {
		return fmt.Errorf("listing the live objects: %w", err)
	}
	if len(objs) == 0 {
		s.once("no-objects", "homelab: no live objects yet: add an address of type live with value homelab and use it in policies")
		return nil
	}
	var guests []Guest
	for _, src := range s.Sources {
		g, err := src.Guests(ctx)
		if err != nil {
			return err
		}
		guests = append(guests, g...)
	}
	if s.Leases {
		if err := s.fillFromLeases(ctx, guests); err != nil {
			return err
		}
	}

	byLower := map[string]string{}
	for _, o := range objs {
		byLower[strings.ToLower(o.Name)] = o.Name
	}
	want := map[string]map[netip.Addr]bool{}
	for _, o := range objs {
		want[o.Name] = map[netip.Addr]bool{}
	}
	for _, g := range guests {
		for _, obj := range s.objectsOf(g, byLower) {
			addrs := s.usable(g.Addrs)
			if len(addrs) == 0 {
				s.once("noaddr/"+g.Source+"/"+g.Name, "homelab: guest has no usable address yet", "guest", g.Name, "source", g.Source, "object", obj)
			}
			for _, a := range addrs {
				want[obj][a] = true
			}
		}
	}

	for _, o := range objs {
		members := sortedPrefixes(want[o.Name])
		if slices.Equal(members, o.Members) {
			continue
		}
		list := make([]string, len(members))
		for i, p := range members {
			list[i] = p.String()
		}
		ch, err := s.API.SetLiveObject(ctx, o.Name, list)
		if err != nil {
			return fmt.Errorf("updating %s: %w", o.Name, err)
		}
		if ch.Changed {
			s.Logger.Info("homelab: object updated", "object", o.Name, "members", ch.Members, "added", ch.Added, "removed", ch.Removed)
		}
	}
	return nil
}

// objectsOf returns the live objects guest g belongs to.
func (s *Syncer) objectsOf(g Guest, byLower map[string]string) []string {
	var out []string
	for _, t := range g.Tags {
		name, ok := strings.CutPrefix(t, TagPrefix)
		if !ok || name == "" {
			continue
		}
		obj, ok := byLower[name]
		if !ok {
			s.once("tag/"+name, "homelab: tag "+TagPrefix+name+" names no live object of this plugin", "guest", g.Name)
			continue
		}
		out = append(out, obj)
	}
	for _, p := range s.Settings.Patterns {
		if !p.Matches(g.Name) {
			continue
		}
		obj, ok := byLower[strings.ToLower(p.Object)]
		if !ok {
			s.once("pattern/"+p.Object, "homelab: name pattern for "+p.Object+", which is no live object of this plugin")
			continue
		}
		out = append(out, obj)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// usable keeps the addresses a policy can match: no loopback, link-local,
// multicast or unspecified ones, and only inside Settings.Networks when set.
func (s *Syncer) usable(addrs []netip.Addr) []netip.Addr {
	var out []netip.Addr
	for _, a := range addrs {
		a = a.Unmap()
		if !a.IsValid() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsMulticast() || a.IsUnspecified() || a.IsInterfaceLocalMulticast() {
			continue
		}
		if len(s.Settings.Networks) > 0 && !slices.ContainsFunc(s.Settings.Networks, func(p netip.Prefix) bool { return p.Contains(a) }) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// fillFromLeases gives guests that report no address the addresses the
// device inventory has for their MACs.
func (s *Syncer) fillFromLeases(ctx context.Context, guests []Guest) error {
	need := false
	for _, g := range guests {
		if len(g.Addrs) == 0 && len(g.MACs) > 0 {
			need = true
		}
	}
	if !need {
		return nil
	}
	devs, err := s.API.Devices(ctx)
	if pluginsdk.Denied(err) {
		s.once("no-devices", "homelab: devices:read is not approved: guests without a guest agent get no address")
		s.Leases = false
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading the device inventory: %w", err)
	}
	byMAC := map[string][]string{}
	for _, d := range devs {
		byMAC[strings.ToLower(d.MAC)] = d.IPs
	}
	for i := range guests {
		g := &guests[i]
		if len(g.Addrs) > 0 {
			continue
		}
		for _, mac := range g.MACs {
			for _, ip := range byMAC[mac] {
				if a, err := netip.ParseAddr(ip); err == nil {
					g.Addrs = append(g.Addrs, a)
				}
			}
		}
	}
	return nil
}

// sortedPrefixes returns the addresses as host prefixes, sorted like
// goblind sorts members.
func sortedPrefixes(set map[netip.Addr]bool) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(set))
	for a := range set {
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr().Less(out[j].Addr()) })
	return out
}
