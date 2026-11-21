package unifi

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/dhcpdns"
	"github.com/networkgoblinlabs/goblin-gate/pkg/pluginsdk"
)

// API is the part of goblind's plugin API the syncer uses.
type API interface {
	LiveObjects(ctx context.Context) ([]pluginsdk.LiveObject, error)
	SetLiveObject(ctx context.Context, name string, members []string) (pluginsdk.LiveChange, error)
	SetDeviceAnnotations(ctx context.Context, devices map[string]map[string]string) (bool, error)
}

// Source is the controller: Client, or a fake in tests.
type Source interface {
	Stations(ctx context.Context) ([]Station, error)
	Devices(ctx context.Context) ([]Device, error)
}

// Syncer copies what the controller knows into goblind.
type Syncer struct {
	API      API
	Source   Source
	Settings Settings
	Objects  bool // objects:write approved
	Annotate bool // devices:annotate approved
	Logger   *slog.Logger

	lastErr   string
	noted     map[string]bool
	annotated map[string]map[string]string // last set sent; nil = unknown
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

func (s *Syncer) report(err error) {
	switch {
	case err != nil && err.Error() != s.lastErr:
		s.Logger.Warn("unifi: sync failed, objects and device details stay as they were", "error", err)
		s.lastErr = err.Error()
	case err == nil && s.lastErr != "":
		s.Logger.Info("unifi: sync works again")
		s.lastErr = ""
	}
}

func (s *Syncer) once(key, msg string, args ...any) {
	if s.noted == nil {
		s.noted = map[string]bool{}
	}
	if !s.noted[key] {
		s.noted[key] = true
		s.Logger.Info(msg, args...)
	}
}

// Once reads the controller and updates objects and annotations. Nothing is
// changed unless the controller answered.
func (s *Syncer) Once(ctx context.Context) error {
	var objs []pluginsdk.LiveObject
	if s.Objects {
		var err error
		if objs, err = s.API.LiveObjects(ctx); err != nil {
			return fmt.Errorf("listing the live objects: %w", err)
		}
		if len(objs) == 0 {
			s.once("no-objects", "unifi: no live objects yet: add an address of type live with value unifi and use it in policies")
		}
	}
	if len(objs) == 0 && !s.Annotate {
		return nil
	}
	stations, err := s.Source.Stations(ctx)
	if err != nil {
		return err
	}
	if len(objs) > 0 {
		if err := s.fill(ctx, objs, stations); err != nil {
			return err
		}
	}
	if s.Annotate {
		// device names are a nicety: without them the uplink's own report
		// (last_uplink_name) stands in
		devs, err := s.Source.Devices(ctx)
		if err != nil {
			s.once("no-device-names", "unifi: could not read the device names, using the clients' uplink names", "error", err)
		}
		return s.annotate(ctx, stations, devs)
	}
	return nil
}

var noteTagRe = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9_-])gg-([A-Za-z0-9_.-]+)`)

// ObjectsOf returns the objects a client belongs to: by network, by SSID, by
// gg-<object> in its note. byLower maps lowercase object names to the real
// ones (only the plugin's own objects).
func (s *Syncer) ObjectsOf(st Station, byLower map[string]string) []string {
	var out []string
	add := func(obj string) {
		if real, ok := byLower[strings.ToLower(obj)]; ok && !slices.Contains(out, real) {
			out = append(out, real)
		}
	}
	for _, m := range s.Settings.Networks {
		if st.Network != "" && strings.EqualFold(st.Network, m.Name) {
			add(m.Object)
		}
	}
	for _, m := range s.Settings.SSIDs {
		if !st.Wired && st.ESSID != "" && st.ESSID == m.Name {
			add(m.Object)
		}
	}
	for _, m := range noteTagRe.FindAllStringSubmatch(st.Note, -1) {
		add(m[1])
	}
	return out
}

// addrs are a client's usable addresses.
func addrs(st Station) []netip.Addr {
	var out []netip.Addr
	for _, raw := range append([]string{st.IP}, st.IPv6...) {
		a, err := netip.ParseAddr(raw)
		if err != nil || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsMulticast() || a.IsUnspecified() {
			continue
		}
		out = append(out, a.Unmap())
	}
	return out
}

func (s *Syncer) fill(ctx context.Context, objs []pluginsdk.LiveObject, stations []Station) error {
	byLower := map[string]string{}
	want := map[string]map[netip.Addr]bool{}
	for _, o := range objs {
		byLower[strings.ToLower(o.Name)] = o.Name
		want[o.Name] = map[netip.Addr]bool{}
	}
	for _, st := range stations {
		for _, obj := range s.ObjectsOf(st, byLower) {
			for _, a := range addrs(st) {
				want[obj][a] = true
			}
		}
	}
	for _, o := range objs {
		members := make([]netip.Prefix, 0, len(want[o.Name]))
		for a := range want[o.Name] {
			members = append(members, netip.PrefixFrom(a, a.BitLen()))
		}
		slices.SortFunc(members, func(a, b netip.Prefix) int { return a.Addr().Compare(b.Addr()) })
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
			s.Logger.Info("unifi: object updated", "object", o.Name, "members", ch.Members, "added", ch.Added, "removed", ch.Removed)
		}
	}
	return nil
}

// Annotations builds the device details for every client.
func Annotations(stations []Station, devs []Device) map[string]map[string]string {
	names := map[string]string{}
	for _, d := range devs {
		if d.Name != "" {
			names[dhcpdns.NormalizeMAC(d.MAC)] = d.Name
		}
	}
	uplink := func(mac, fallback string) string {
		if n := names[dhcpdns.NormalizeMAC(mac)]; n != "" {
			return n
		}
		if fallback != "" {
			return fallback
		}
		return mac
	}
	out := map[string]map[string]string{}
	for _, st := range stations {
		mac := dhcpdns.NormalizeMAC(st.MAC)
		if len(mac) != 12 {
			continue
		}
		f := map[string]string{}
		set := func(k, v string) {
			if v = strings.TrimSpace(v); v != "" {
				if r := []rune(v); len(r) > 64 {
					v = string(r[:64])
				}
				f[k] = v
			}
		}
		num := func(k string, n int) {
			if n > 0 {
				f[k] = strconv.Itoa(n)
			}
		}
		if st.Wired {
			f["connection"] = "wired"
			if st.SwitchMAC != "" || st.LastUplinkName != "" {
				set("switch", uplink(st.SwitchMAC, st.LastUplinkName))
			}
			num("port", st.SwitchPort)
			num("link_mbps", st.WiredRateMbps)
		} else {
			f["connection"] = "wifi"
			if st.APMAC != "" || st.LastUplinkName != "" {
				set("ap", uplink(st.APMAC, st.LastUplinkName))
			}
			set("ssid", st.ESSID)
			// no signal or rate: they change on every poll, and these
			// details are for where a device is, not how it is doing
			set("band", band(st.Radio))
		}
		set("network", st.Network)
		num("vlan", st.VLAN)
		out[mac] = f
	}
	return out
}

func band(radio string) string {
	switch radio {
	case "ng":
		return "2.4 GHz"
	case "na":
		return "5 GHz"
	case "6e":
		return "6 GHz"
	}
	return ""
}

func (s *Syncer) annotate(ctx context.Context, stations []Station, devs []Device) error {
	set := Annotations(stations, devs)
	// a client that just went away keeps its details until the next poll
	// shows it gone; but the same set again is not even sent
	if s.annotated != nil && maps.EqualFunc(s.annotated, set, func(a, b map[string]string) bool { return maps.Equal(a, b) }) {
		return nil
	}
	if _, err := s.API.SetDeviceAnnotations(ctx, set); err != nil {
		return fmt.Errorf("updating the device details: %w", err)
	}
	s.annotated = set
	return nil
}
