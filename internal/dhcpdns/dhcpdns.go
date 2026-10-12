// Package dhcpdns gives DHCP clients DNS names (task 055): it reads Kea's
// lease file and keeps Unbound's local data in step at runtime with
// unbound-control, so "laptop.home.arpa" resolves without a restart.
package dhcpdns

import (
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Lease is one active DHCPv4 lease with a usable hostname.
type Lease struct {
	IP   netip.Addr
	Name string // sanitized single DNS label
}

// ParseLeases reads Kea's memfile CSV (columns by header name). The file is
// append-only between cleanups, so the last line per address wins; a lease
// counts while it has not expired and its state is 0 (1 = declined,
// 2 = expired-reclaimed).
func ParseLeases(r io.Reader, now time.Time) ([]Lease, error) {
	cr := csv.NewReader(bufio.NewReader(r))
	cr.FieldsPerRecord = -1
	header, err := cr.Read()
	if err == io.EOF {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dhcpdns: lease file header: %w", err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.TrimSpace(h)] = i
	}
	for _, need := range []string{"address", "expire", "hostname"} {
		if _, ok := col[need]; !ok {
			return nil, fmt.Errorf("dhcpdns: lease file has no %q column", need)
		}
	}
	get := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	latest := map[netip.Addr][]string{}
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue // a half-written last line
		}
		if ip, err := netip.ParseAddr(get(rec, "address")); err == nil {
			latest[ip] = rec
		}
	}
	var out []Lease
	for ip, rec := range latest {
		exp, _ := strconv.ParseInt(get(rec, "expire"), 10, 64)
		if exp != 0 && exp < now.Unix() {
			continue
		}
		if st := get(rec, "state"); st != "" && st != "0" {
			continue
		}
		if name := Sanitize(get(rec, "hostname")); name != "" {
			out = append(out, Lease{IP: ip, Name: name})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IP.Less(out[j].IP) })
	return out, nil
}

// Sanitize turns a client-supplied hostname into one lower-case DNS label
// ("Mikes-Laptop.local" → "mikes-laptop"), or "" if nothing usable is left.
func Sanitize(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h, _, _ = strings.Cut(h, ".")
	var b strings.Builder
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == ' ':
			b.WriteByte('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 63 {
		s = strings.TrimRight(s[:63], "-")
	}
	return s
}

// Settings is what the syncer needs from the running config.
type Settings struct {
	Enabled bool            // !dns.no_dhcp_names
	Domain  string          // config.EffectiveLocalDomain
	Taken   map[string]bool // FQDNs already defined (static leases, local records)
}

// Syncer keeps Unbound's runtime local data equal to the active leases.
type Syncer struct {
	LeaseFile string
	Settings  func() (Settings, error)
	Every     time.Duration // default 30 s
	Logger    *slog.Logger
	// Control runs unbound-control with these arguments (tests replace it).
	Control func(ctx context.Context, args ...string) error
	Now     func() time.Time

	mu      sync.Mutex
	current map[string]netip.Addr // fqdn -> ip as Unbound has it
}

func (s *Syncer) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Syncer) control(ctx context.Context, args ...string) error {
	if s.Control != nil {
		return s.Control(ctx, args...)
	}
	full := append([]string{"-c", "/etc/unbound/unbound.conf"}, args...)
	out, err := exec.CommandContext(ctx, "unbound-control", full...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("unbound-control %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Run syncs every interval until ctx ends.
func (s *Syncer) Run(ctx context.Context) {
	every := s.Every
	if every <= 0 {
		every = 30 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := s.Sync(ctx); err != nil {
			s.logger().Warn("dhcpdns: sync failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Resync forgets what Unbound holds (call after a commit restarts Unbound:
// runtime data is gone) and syncs again.
func (s *Syncer) Resync(ctx context.Context) {
	s.mu.Lock()
	s.current = nil
	s.mu.Unlock()
	if err := s.Sync(ctx); err != nil {
		s.logger().Warn("dhcpdns: resync failed", "error", err)
	}
}

// Sync adds names for new leases and removes names of gone ones.
func (s *Syncer) Sync(ctx context.Context) error {
	set, err := s.Settings()
	if err != nil {
		return err
	}
	want := map[string]netip.Addr{}
	if set.Enabled {
		f, err := os.Open(s.LeaseFile)
		switch {
		case os.IsNotExist(err):
		case err != nil:
			return err
		default:
			now := time.Now()
			if s.Now != nil {
				now = s.Now()
			}
			leases, perr := ParseLeases(f, now)
			f.Close()
			if perr != nil {
				return perr
			}
			for _, l := range leases {
				fqdn := l.Name + "." + strings.ToLower(set.Domain)
				if set.Taken[fqdn] {
					continue // a static lease or local record wins
				}
				if _, dup := want[fqdn]; dup {
					continue // two clients with one name: the lower address keeps it
				}
				want[fqdn] = l.IP
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var errs []error
	for name, ip := range s.current {
		if w, ok := want[name]; !ok || w != ip {
			if err := s.control(ctx, "local_data_remove", name+"."); err != nil {
				errs = append(errs, err)
				continue
			}
			if rev := reverseName(ip); rev != "" {
				s.control(ctx, "local_data_remove", rev)
			}
			delete(s.current, name)
		}
	}
	if s.current == nil {
		s.current = map[string]netip.Addr{}
	}
	names := make([]string, 0, len(want))
	for n := range want {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		ip := want[name]
		if cur, ok := s.current[name]; ok && cur == ip {
			continue
		}
		if err := s.control(ctx, "local_data", name+". 300 IN A "+ip.String()); err != nil {
			errs = append(errs, err)
			continue
		}
		if rev := reverseName(ip); rev != "" {
			s.control(ctx, "local_data", rev+" 300 IN PTR "+name+".")
		}
		s.current[name] = ip
	}
	if len(errs) > 0 {
		return fmt.Errorf("dhcpdns: %d unbound-control call(s) failed, first: %w", len(errs), errs[0])
	}
	return nil
}

// reverseName is the in-addr.arpa name of an IPv4 address.
func reverseName(ip netip.Addr) string {
	if !ip.Is4() {
		return ""
	}
	a := ip.As4()
	return fmt.Sprintf("%d.%d.%d.%d.in-addr.arpa.", a[3], a[2], a[1], a[0])
}
