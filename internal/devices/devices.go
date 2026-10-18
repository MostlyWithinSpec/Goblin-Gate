// Package devices keeps an inventory of the machines on the network (task
// 070). It merges DHCP leases (Kea) with the kernel neighbour table, looks
// up vendors by OUI, remembers first/last seen times and publishes a
// "new_device" notification when a MAC is seen for the first time.
package devices

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/dhcpdns"
)

//go:embed oui.txt.gz
var ouiGz embed.FS

// DefaultPath is where the store lives on a real installation.
const DefaultPath = "/var/lib/goblingate/devices.json"

// MaxDevices caps the inventory; the longest-unseen devices are dropped first.
const MaxDevices = 5000

// MaxIPs is how many addresses one device remembers, newest first.
const MaxIPs = 8

// Device is one machine as seen from the gate.
type Device struct {
	MAC        string    `json:"mac"`
	IPs        []string  `json:"ips"`                // newest first, v4 and v6
	Hostname   string    `json:"hostname,omitempty"` // from DHCP
	Vendor     string    `json:"vendor,omitempty"`
	Interface  string    `json:"interface,omitempty"` // config interface/VLAN name
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
	Name       string    `json:"name,omitempty"` // admin label
	Randomized bool      `json:"randomized,omitempty"`
}

// Store persists the inventory as JSON (atomically) and refreshes it from
// DHCP leases and the neighbour table.
type Store struct {
	Path string // JSON file; tests use a temp dir
	// LeaseFile is Kea's memfile CSV; "" = no lease source.
	LeaseFile string
	// Runner runs commands (the neighbour table); nil = built-in exec.
	Runner func(ctx context.Context, name string, args ...string) ([]byte, error)
	// Interfaces maps a kernel device to its config interface/VLAN name;
	// nil = every neighbour is filed under its kernel device name.
	Interfaces func() map[string]string
	// Notify receives the new_device events; nil = no notifications.
	Notify func(kind, key, title, message string)
	Now    func() time.Time
	Logger *slog.Logger

	mu       sync.Mutex
	devices  map[string]*Device
	loaded   bool
	firstRun bool // the first poll is over (no new_device events during it)
}

// New opens a store at path.
func New(path string) *Store {
	return &Store{Path: path}
}

func (s *Store) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// loadLocked reads the store file once. A missing file is an empty store; a
// corrupt one is logged and treated as empty (never fatal).
func (s *Store) loadLocked() {
	if s.loaded {
		return
	}
	s.loaded = true
	data, err := os.ReadFile(s.Path)
	switch {
	case err == nil:
		var list []Device
		if jerr := json.Unmarshal(data, &list); jerr != nil {
			s.logger().Warn("devices: store file is corrupt, starting empty", "path", s.Path, "error", jerr)
		} else {
			s.devices = make(map[string]*Device, len(list))
			for i := range list {
				d := &list[i]
				s.devices[d.MAC] = d
			}
		}
	case os.IsNotExist(err):
	default:
		s.logger().Warn("devices: cannot read store file", "path", s.Path, "error", err)
	}
	if s.devices == nil {
		s.devices = map[string]*Device{}
	}
}

// saveLocked writes the store atomically (temp file + rename).
func (s *Store) saveLocked() error {
	list := make([]Device, 0, len(s.devices))
	for _, d := range s.devices {
		list = append(list, *d)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].MAC < list[j].MAC })
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("devices: encode: %w", err)
	}
	if dir := filepath.Dir(s.Path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.Path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// List returns a copy of the inventory sorted by last_seen, newest first.
func (s *Store) List() ([]Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	out := make([]Device, 0, len(s.devices))
	for _, d := range s.devices {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out, nil
}

// ByIP indexes the devices by each of their addresses (the newest device
// wins when an address moved), for naming addresses elsewhere (live flows).
func (s *Store) ByIP() map[netip.Addr]Device {
	list, _ := s.List()
	out := make(map[netip.Addr]Device, len(list))
	for i := len(list) - 1; i >= 0; i-- { // oldest first, so newer ones overwrite
		for _, ip := range list[i].IPs {
			if a, err := netip.ParseAddr(ip); err == nil {
				out[a] = list[i]
			}
		}
	}
	return out
}

// SetName labels a device ("" clears the label).
func (s *Store) SetName(mac, name string) (*Device, error) {
	mac = dhcpdns.NormalizeMAC(mac)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	d, ok := s.devices[mac]
	if !ok {
		return nil, fmt.Errorf("devices: %s not found", mac)
	}
	d.Name = name
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	out := *d
	return &out, nil
}

// Remove deletes a device (e.g. after the admin retires it).
func (s *Store) Remove(mac string) error {
	mac = dhcpdns.NormalizeMAC(mac)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	if _, ok := s.devices[mac]; !ok {
		return fmt.Errorf("devices: %s not found", mac)
	}
	delete(s.devices, mac)
	return s.saveLocked()
}

// Run refreshes the inventory until ctx ends. The first poll never emits
// new_device events (a fresh install must not alert for every device at
// once); every later appearance of an unknown MAC does.
func (s *Store) Run(ctx context.Context) {
	for {
		if err := s.Refresh(ctx); err != nil {
			s.logger().Warn("devices: refresh failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(60 * time.Second):
		}
	}
}

// Refresh polls both sources once and merges them into the store.
func (s *Store) Refresh(ctx context.Context) error {
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	type sighting struct {
		ip   string
		host string
		dev  string
	}
	seen := map[string]*sighting{} // mac -> merged facts

	addLease := func(rec dhcpdns.LeaseRecord) {
		if rec.MAC == "" || !rec.IP.IsValid() {
			return
		}
		sig, ok := seen[rec.MAC]
		if !ok {
			sig = &sighting{}
			seen[rec.MAC] = sig
		}
		sig.ip = rec.IP.String()
		if rec.Hostname != "" {
			sig.host = rec.Hostname
		}
	}

	f, err := os.Open(s.LeaseFile)
	switch {
	case os.IsNotExist(err):
	case err != nil:
		return fmt.Errorf("devices: open lease file: %w", err)
	default:
		recs, perr := dhcpdns.ParseLeaseRecords(f, now)
		f.Close()
		if perr != nil {
			return fmt.Errorf("devices: parse leases: %w", perr)
		}
		for _, rec := range recs {
			addLease(rec)
		}
	}

	neigh, err := s.neighbours(ctx)
	if err != nil {
		s.logger().Warn("devices: neighbour table unavailable", "error", err)
	} else {
		for _, n := range neigh {
			sig, ok := seen[n.MAC]
			if !ok {
				sig = &sighting{}
				seen[n.MAC] = sig
			}
			if sig.ip == "" {
				sig.ip = n.IP
			}
			if n.Dev != "" {
				sig.dev = n.Dev
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	first := !s.firstRun
	s.firstRun = true

	ifaces := map[string]string{}
	if s.Interfaces != nil {
		ifaces = s.Interfaces()
	}
	for mac, sig := range seen {
		d, ok := s.devices[mac]
		if !ok {
			d = &Device{MAC: mac, FirstSeen: now}
			s.devices[mac] = d
			d.Vendor, d.Randomized = vendorOf(mac)
		}
		if sig.ip != "" && !contains(d.IPs, sig.ip) {
			d.IPs = append([]string{sig.ip}, d.IPs...)
			if len(d.IPs) > MaxIPs {
				d.IPs = d.IPs[:MaxIPs]
			}
		}
		if sig.host != "" {
			d.Hostname = sig.host
		}
		if sig.dev != "" {
			if name, ok := ifaces[sig.dev]; ok {
				d.Interface = name
			} else {
				d.Interface = sig.dev
			}
		}
		d.LastSeen = now
		if !ok && !first && s.Notify != nil {
			title := "New device on " + d.Interface
			s.Notify("new_device", mac, title, fmt.Sprintf("%s (%s) is on %s — %s / %s", labelOf(d), d.Vendor, d.Interface, strings.Join(d.IPs, ", "), d.MAC))
		}
	}
	if len(s.devices) > MaxDevices {
		drop := make([]Device, 0, len(s.devices))
		for _, d := range s.devices {
			drop = append(drop, *d)
		}
		sort.Slice(drop, func(i, j int) bool { return drop[i].LastSeen.Before(drop[j].LastSeen) })
		excess := len(s.devices) - MaxDevices
		for i := 0; i < excess; i++ {
			delete(s.devices, drop[i].MAC)
		}
	}
	return s.saveLocked()
}

// neighbour is one entry of the kernel neighbour table.
type neighbour struct {
	IP  string
	MAC string
	Dev string
}

// neighbours reads "ip -j neigh show" and keeps the entries that have a link
// layer address and are not FAILED or INCOMPLETE.
func (s *Store) neighbours(ctx context.Context) ([]neighbour, error) {
	var out []byte
	var err error
	if s.Runner != nil {
		out, err = s.Runner(ctx, "ip", "-j", "neigh", "show")
	} else {
		out, err = defaultRunner(ctx, "ip", "-j", "neigh", "show")
	}
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Dst    string `json:"dst"`
		Dev    string `json:"dev"`
		LLAddr string `json:"lladdr"`
		State  string `json:"state"`
	}
	if jerr := json.Unmarshal(out, &rows); jerr != nil {
		return nil, fmt.Errorf("devices: neighbour table: %w", jerr)
	}
	var outN []neighbour
	for _, r := range rows {
		mac := dhcpdns.NormalizeMAC(r.LLAddr)
		if mac == "" || r.Dst == "" {
			continue
		}
		switch r.State {
		case "FAILED", "INCOMPLETE":
			continue
		}
		outN = append(outN, neighbour{IP: r.Dst, MAC: mac, Dev: r.Dev})
	}
	return outN, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// labelOf is the text a notification uses for a device.
func labelOf(d *Device) string {
	switch {
	case d.Name != "":
		return d.Name
	case d.Hostname != "":
		return d.Hostname
	default:
		return "Unknown device"
	}
}
