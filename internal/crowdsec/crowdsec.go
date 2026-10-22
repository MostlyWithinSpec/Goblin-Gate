// Package crowdsec is the box's CrowdSec bouncer (task 104): it pulls the
// decisions stream of a CrowdSec Local API and keeps the bans in the nft
// sets of internal/render/nft (crowdsec.go) with their remaining time.
package crowdsec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/nft"
)

// MaxBans bounds the decisions kept (the community blocklist is a few
// tens of thousands).
const MaxBans = 200000

// Manager pulls decisions and applies them.
type Manager struct {
	// Config returns the running config's CrowdSec settings.
	Config func() (config.CrowdSec, error)
	Client *http.Client // nil = 20 s timeout
	Logger *slog.Logger
	Now    func() time.Time
	// RunNFT applies an nft script (nil = nft -f -).
	RunNFT func(ctx context.Context, script []byte) error
	// Drops reads the drop counter (nil = no count in Status).
	Drops func(ctx context.Context) (uint64, error)

	mu        sync.Mutex
	bans      map[netip.Prefix]time.Time // prefix -> expiry
	conn      string                     // url + key hash the bans came from
	started   bool                       // the startup list was pulled for conn
	connected bool
	lastPull  time.Time
	lastErr   string
}

// Status is GET /api/v1/crowdsec.
type Status struct {
	Enabled   bool      `json:"enabled"`
	Connected bool      `json:"connected"`
	LastPull  time.Time `json:"last_pull,omitzero"`
	Active    int       `json:"active"`
	Dropped   uint64    `json:"dropped_packets"`
	LastError string    `json:"last_error,omitempty"`
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

func (m *Manager) client() *http.Client {
	if m.Client != nil {
		return m.Client
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func (m *Manager) run(ctx context.Context, script []byte) error {
	if len(script) == 0 {
		return nil
	}
	if m.RunNFT != nil {
		return m.RunNFT(ctx, script)
	}
	cmd := exec.CommandContext(ctx, "nft", "-f", "-")
	cmd.Stdin = bytes.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("nft: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Run pulls decisions until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	for {
		wait := 10 * time.Second
		if c, err := m.Config(); err == nil {
			wait = time.Duration(c.Poll()) * time.Second
			m.Pull(ctx, c)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// decision is one entry of the LAPI decisions stream.
type decision struct {
	Type     string `json:"type"`
	Scope    string `json:"scope"`
	Value    string `json:"value"`
	Duration string `json:"duration"`
}

type stream struct {
	New     []decision `json:"new"`
	Deleted []decision `json:"deleted"`
}

// Pull fetches one round of decisions for c and applies it. Off, it drops
// every ban. A changed URL or key starts over with the startup list.
func (m *Manager) Pull(ctx context.Context, c config.CrowdSec) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !c.Enabled || c.URL == "" || c.APIKey == "" {
		m.clearLocked(ctx)
		m.conn, m.started, m.connected, m.lastErr = "", false, false, ""
		return
	}
	conn := c.URL + "\x00" + c.APIKey
	if conn != m.conn {
		m.clearLocked(ctx)
		m.conn, m.started = conn, false
	}
	// the request runs unlocked (Status must not wait for a slow API);
	// pulls themselves only come from Run, one at a time
	wantStartup := !m.started
	m.mu.Unlock()
	s, err := m.fetch(ctx, c, wantStartup)
	m.mu.Lock()
	if m.conn != conn {
		return // settings changed meanwhile: the next pull starts over
	}
	if err != nil {
		m.connected, m.lastErr = false, err.Error()
		m.started = false // pull the full list again once it answers
		m.logger().Warn("crowdsec: pull failed", "error", err)
		return
	}
	startup := wantStartup
	m.started, m.connected, m.lastErr, m.lastPull = true, true, "", m.now()
	if m.bans == nil {
		m.bans = map[netip.Prefix]time.Time{}
	}
	now := m.now()
	var add []nft.CrowdSecBan
	var remove []netip.Prefix
	if startup {
		// the startup list is the whole truth: anything else goes
		keep := map[netip.Prefix]bool{}
		for _, d := range s.New {
			if p, _, ok := parse(d); ok {
				keep[p] = true
			}
		}
		for p := range m.bans {
			if !keep[p] {
				remove = append(remove, p)
				delete(m.bans, p)
			}
		}
	}
	for _, d := range s.Deleted {
		if p, _, ok := parse(d); ok {
			if _, had := m.bans[p]; had {
				remove = append(remove, p)
				delete(m.bans, p)
			}
		}
	}
	for _, d := range s.New {
		p, left, ok := parse(d)
		if !ok || left <= 0 {
			continue
		}
		if _, had := m.bans[p]; !had && len(m.bans) >= MaxBans {
			continue
		}
		m.bans[p] = now.Add(left)
		add = append(add, nft.CrowdSecBan{Prefix: p, Left: left})
	}
	m.applyLocked(ctx, add, remove)
}

// parse returns a ban decision's prefix and duration; ok is false for
// other types and values that are not addresses or networks.
func parse(d decision) (netip.Prefix, time.Duration, bool) {
	if !strings.EqualFold(d.Type, "ban") {
		return netip.Prefix{}, 0, false
	}
	var p netip.Prefix
	switch strings.ToLower(d.Scope) {
	case "ip":
		a, err := netip.ParseAddr(d.Value)
		if err != nil {
			return netip.Prefix{}, 0, false
		}
		a = a.Unmap()
		p = netip.PrefixFrom(a, a.BitLen())
	case "range":
		q, err := netip.ParsePrefix(d.Value)
		if err != nil {
			return netip.Prefix{}, 0, false
		}
		p = q.Masked()
	default:
		return netip.Prefix{}, 0, false
	}
	left, err := time.ParseDuration(d.Duration)
	if err != nil {
		left = 0
	}
	return p, left, true
}

// applyLocked runs the script; a failed batch (an overlapping range, say)
// is retried element by element so one bad entry does not stop the rest.
func (m *Manager) applyLocked(ctx context.Context, add []nft.CrowdSecBan, remove []netip.Prefix) {
	if len(add) == 0 && len(remove) == 0 {
		return
	}
	err := m.run(ctx, nft.CrowdSecScript(add, remove))
	if err == nil {
		return
	}
	failed := 0
	for _, b := range add {
		if m.run(ctx, nft.CrowdSecScript([]nft.CrowdSecBan{b}, nil)) != nil {
			failed++
		}
	}
	for _, p := range remove {
		_ = m.run(ctx, nft.CrowdSecScript(nil, []netip.Prefix{p}))
	}
	if failed > 0 {
		m.logger().Warn("crowdsec: some decisions could not be applied", "failed", failed, "error", err)
	}
}

func (m *Manager) clearLocked(ctx context.Context) {
	if len(m.bans) == 0 {
		return
	}
	var remove []netip.Prefix
	for p := range m.bans {
		remove = append(remove, p)
	}
	m.bans = nil
	m.applyLocked(ctx, nil, remove)
}

// Resync puts every live ban back with its remaining time: a commit
// reloads the ruleset, which empties the sets.
func (m *Manager) Resync(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	var add []nft.CrowdSecBan
	for p, exp := range m.bans {
		if left := exp.Sub(now); left > 0 {
			add = append(add, nft.CrowdSecBan{Prefix: p, Left: left})
		} else {
			delete(m.bans, p)
		}
	}
	m.applyLocked(ctx, add, nil)
}

// Status reports the connection and the live bans.
func (m *Manager) Status(ctx context.Context) Status {
	c, _ := m.Config()
	m.mu.Lock()
	st := Status{Enabled: c.Enabled, Connected: m.connected, LastPull: m.lastPull, LastError: m.lastErr}
	now := m.now()
	for _, exp := range m.bans {
		if exp.After(now) {
			st.Active++
		}
	}
	m.mu.Unlock()
	if m.Drops != nil && c.Enabled {
		if n, err := m.Drops(ctx); err == nil {
			st.Dropped = n
		}
	}
	return st
}

// fetch asks the LAPI for the decisions stream. Errors never contain the
// key, and name the URL without credentials or query.
func (m *Manager) fetch(ctx context.Context, c config.CrowdSec, startup bool) (*stream, error) {
	u := strings.TrimRight(c.URL, "/") + "/v1/decisions/stream?scopes=ip,range"
	if startup {
		u += "&startup=true"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, errors.New("invalid Local API URL")
	}
	req.Header.Set("X-Api-Key", c.APIKey)
	req.Header.Set("User-Agent", "goblin-gate-bouncer")
	resp, err := m.client().Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("Local API unreachable: %v", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized:
		return nil, errors.New("the Local API refused the bouncer key (cscli bouncers list)")
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("Local API answered HTTP %d", resp.StatusCode)
	}
	var s stream
	dec := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 64<<20))
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("Local API answer not understood: %v", err)
	}
	return &s, nil
}
