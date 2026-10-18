package ddns

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// TickInterval is how often the manager checks the public IP.
const TickInterval = 5 * time.Minute

// MaxAge is how long a successful update stays valid before re-updating.
const MaxAge = 24 * time.Hour

// Status is the per-entry view returned by the API. LastUpdate is the last
// successful push, LastAttempt the last try (failed or not).
type Status struct {
	Name        string    `json:"name"`
	Hostname    string    `json:"hostname"`
	IP          string    `json:"ip,omitempty"`
	LastUpdate  time.Time `json:"last_update,omitzero"`
	LastAttempt time.Time `json:"last_attempt,omitzero"`
	LastError   string    `json:"last_error,omitempty"`
}

type entry struct {
	cfg         config.DDNS
	lastIP      netip.Addr // the IP the provider has, after a successful push
	lastSuccess time.Time
	lastAttempt time.Time
	lastError   string
}

// Manager keeps DDNS entries pointing at the box: every tick it checks the
// public IP and pushes it to each enabled entry whose provider has a
// different IP, whose last push failed, whose config changed or whose last
// success is older than MaxAge. Set exported fields before Run.
type Manager struct {
	Updater *Updater
	Logger  *slog.Logger
	Now     func() time.Time

	mu      sync.Mutex
	entries map[string]*entry
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) logger() *slog.Logger {
	if m.Logger != nil {
		return m.Logger
	}
	return slog.Default()
}

// Run starts the DDNS update loop. entries returns the current DDNS config
// list (normally from the running config). It blocks until ctx is done.
func (m *Manager) Run(ctx context.Context, entries func() []config.DDNS) {
	t := time.NewTicker(TickInterval)
	defer t.Stop()
	m.tick(ctx, entries())
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.tick(ctx, entries())
		}
	}
}

func (m *Manager) tick(ctx context.Context, list []config.DDNS) {
	m.Sync(list)
	var names []string
	for _, c := range list {
		if c.Enabled {
			names = append(names, c.Name)
		}
	}
	if len(names) == 0 {
		return
	}
	sort.Strings(names)
	ip, err := m.resolveIP(ctx)
	if err != nil {
		m.logger().Warn("ddns: public IP check failed", "error", err)
		m.mu.Lock()
		for _, name := range names {
			if e := m.entries[name]; e != nil {
				e.lastAttempt, e.lastError = m.now(), err.Error()
			}
		}
		m.mu.Unlock()
		return
	}
	for _, name := range names {
		if ctx.Err() != nil {
			return
		}
		m.mu.Lock()
		e := m.entries[name]
		due := e != nil && m.dueLocked(e, ip)
		var cfg config.DDNS
		if e != nil {
			cfg = e.cfg
		}
		m.mu.Unlock()
		if due {
			m.push(ctx, cfg, ip)
		}
	}
}

// dueLocked reports whether the provider needs ip pushed.
func (m *Manager) dueLocked(e *entry, ip netip.Addr) bool {
	return !e.lastIP.IsValid() || e.lastIP != ip || m.now().Sub(e.lastSuccess) >= MaxAge
}

// push sends ip to the provider and records the outcome.
func (m *Manager) push(ctx context.Context, cfg config.DDNS, ip netip.Addr) error {
	err := m.Updater.Update(ctx, cfg, ip)
	if err != nil {
		m.logger().Warn("ddns: update failed", "name", cfg.Name, "error", err)
	}
	now := m.now()
	m.mu.Lock()
	if e := m.entries[cfg.Name]; e != nil && e.cfg == cfg {
		e.lastAttempt = now
		if err == nil {
			e.lastIP, e.lastSuccess, e.lastError = ip, now, ""
		} else {
			e.lastError = err.Error()
		}
	}
	m.mu.Unlock()
	return err
}

// doUpdate resolves the public IP and pushes it if the entry is due.
func (m *Manager) doUpdate(ctx context.Context, cfg config.DDNS) error {
	ip, err := m.resolveIP(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	e := m.entries[cfg.Name]
	due := e == nil || m.dueLocked(e, ip)
	m.mu.Unlock()
	if !due {
		m.logger().Debug("ddns: no update needed", "name", cfg.Name, "ip", ip.String())
		return nil
	}
	return m.push(ctx, cfg, ip)
}

// UpdateNow pushes the current public IP for the named entry, due or not,
// and returns the status after the attempt (error for an unknown name).
func (m *Manager) UpdateNow(ctx context.Context, name string) (Status, error) {
	m.mu.Lock()
	e, ok := m.entries[name]
	if !ok {
		m.mu.Unlock()
		return Status{}, fmt.Errorf("ddns: no such entry %q", name)
	}
	cfg := e.cfg
	m.mu.Unlock()

	ip, err := m.resolveIP(ctx)
	if err != nil {
		m.mu.Lock()
		if e := m.entries[name]; e != nil {
			e.lastAttempt, e.lastError = m.now(), err.Error()
		}
		m.mu.Unlock()
	} else {
		err = m.push(ctx, cfg, ip)
	}
	m.mu.Lock()
	st := m.statusLocked(name)
	m.mu.Unlock()
	return st, err
}

// resolveIP fetches the public IPv4 via IPCheckURL. It does not look at the
// WAN's own address (behind a carrier NAT that would be the wrong one).
func (m *Manager) resolveIP(ctx context.Context) (netip.Addr, error) {
	checkURL := m.Updater.IPCheckURL
	if checkURL == "" {
		checkURL = DefaultIPCheckURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checkURL, nil)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("ddns: bad ip check url: %w", err)
	}
	resp, err := m.Updater.do(req)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("ddns: ip check: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("ddns: ip check: %w", err)
	}
	text := strings.TrimSpace(string(body))
	addr, err := netip.ParseAddr(text)
	if err != nil || !addr.Is4() {
		return netip.Addr{}, fmt.Errorf("ddns: ip check returned %q, not an IPv4 address", truncate(text, 50))
	}
	return addr.Unmap(), nil
}

func (m *Manager) statusLocked(name string) Status {
	e := m.entries[name]
	if e == nil {
		return Status{}
	}
	st := Status{Name: e.cfg.Name, Hostname: e.cfg.Hostname, LastUpdate: e.lastSuccess, LastAttempt: e.lastAttempt, LastError: e.lastError}
	if e.lastIP.IsValid() {
		st.IP = e.lastIP.String()
	}
	return st
}

// Status returns all entry statuses sorted by name.
func (m *Manager) Status() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.entries))
	for name := range m.entries {
		out = append(out, m.statusLocked(name))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Sync loads the current entry list from config. An entry whose config
// changed (new token, hostname, provider...) is pushed again on the next
// check, even if the IP did not change.
func (m *Manager) Sync(list []config.DDNS) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries == nil {
		m.entries = map[string]*entry{}
	}
	keep := make(map[string]bool, len(list))
	for _, c := range list {
		keep[c.Name] = true
		if e, ok := m.entries[c.Name]; !ok {
			m.entries[c.Name] = &entry{cfg: c}
		} else if e.cfg != c {
			*e = entry{cfg: c}
		}
	}
	for name := range m.entries {
		if !keep[name] {
			delete(m.entries, name)
		}
	}
}
