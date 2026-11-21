// Package portal is the captive portal of guest networks: the let-in
// clients (sessions), the voucher batches and the nft sets that let clients
// out. The state lives in one JSON file, so sessions survive a restart; the
// sets are refilled from it after every ruleset load.
package portal

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/nft"
)

// Session is one let-in client.
type Session struct {
	Interface string    `json:"interface"`
	MAC       string    `json:"mac"`
	IP        string    `json:"ip"`
	Method    string    `json:"method"` // click | password | voucher
	Voucher   string    `json:"voucher,omitempty"`
	Batch     string    `json:"batch,omitempty"`
	Started   time.Time `json:"started"`
	Expires   time.Time `json:"expires"`
}

// Batch is a set of voucher codes made together.
type Batch struct {
	ID        string    `json:"id"`
	Note      string    `json:"note,omitempty"`
	Interface string    `json:"interface,omitempty"` // "" = any portal in voucher mode
	Minutes   int       `json:"minutes"`             // session length per use
	Uses      int       `json:"uses"`                // uses per code
	Created   time.Time `json:"created"`
	Expires   time.Time `json:"expires,omitzero"` // codes unusable after; zero = never
	Revoked   bool      `json:"revoked,omitempty"`
	Codes     []Code    `json:"codes"`
}

// Code is one voucher code.
type Code struct {
	Code      string    `json:"code"`
	Used      int       `json:"used"`
	FirstUsed time.Time `json:"first_used,omitzero"`
	Revoked   bool      `json:"revoked,omitempty"`
}

// State reports a code's state: unused, in_use (used, uses left), used_up,
// expired or revoked.
func (b Batch) State(c Code, now time.Time) string {
	switch {
	case b.Revoked || c.Revoked:
		return "revoked"
	case c.Used >= b.Uses:
		return "used_up"
	case !b.Expires.IsZero() && !now.Before(b.Expires):
		return "expired"
	case c.Used > 0:
		return "in_use"
	}
	return "unused"
}

type state struct {
	Sessions []Session `json:"sessions"`
	Batches  []Batch   `json:"batches"`
}

// Errors a guest sees (the portal page translates them).
var (
	ErrNoPortal      = errors.New("no portal on this network")
	ErrNoClient      = errors.New("the client's MAC address is unknown")
	ErrTerms         = errors.New("accept the terms first")
	ErrPassword      = errors.New("wrong password")
	ErrVoucher       = errors.New("unknown, used up or expired voucher")
	ErrTooMany       = errors.New("too many attempts, wait a minute")
	ErrNotFound      = errors.New("not found")
	ErrBatchTooLarge = errors.New("want 1-500 codes")
)

// Manager keeps the sessions and vouchers and the nft sets in step with
// them.
type Manager struct {
	Path   string // the state file (data_dir/portal/state.json)
	Logger *slog.Logger
	// Config returns the running config.
	Config func() (*config.Config, error)
	// RunNFT runs an `nft -f` script; nil in dev (nothing to set).
	RunNFT func(script []byte) error
	// Seen lists the "mac ip" keys in the traffic-seen set (idle timeouts).
	Seen func() (map[string]bool, error)
	Now  func() time.Time

	mu       sync.Mutex
	st       *state
	attempts map[string][]time.Time
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) log() *slog.Logger {
	if m.Logger != nil {
		return m.Logger
	}
	return slog.Default()
}

// load reads the state once; callers hold mu.
func (m *Manager) load() *state {
	if m.st != nil {
		return m.st
	}
	m.st = &state{}
	if b, err := os.ReadFile(m.Path); err == nil {
		if err := json.Unmarshal(b, m.st); err != nil {
			m.log().Warn("portal: state unreadable, starting empty", "path", m.Path, "error", err)
			m.st = &state{}
		}
	}
	return m.st
}

func (m *Manager) save() error {
	b, err := json.MarshalIndent(m.st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.Path), 0o700); err != nil {
		return err
	}
	tmp := m.Path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.Path)
}

func (m *Manager) nft(script []byte) {
	if m.RunNFT == nil || len(script) == 0 {
		return
	}
	if err := m.RunNFT(script); err != nil {
		m.log().Warn("portal: nft", "error", err)
	}
}

// prune drops ended sessions; callers hold mu.
func (m *Manager) prune(now time.Time) bool {
	st := m.load()
	n := len(st.Sessions)
	st.Sessions = slices.DeleteFunc(st.Sessions, func(s Session) bool { return !now.Before(s.Expires) })
	return len(st.Sessions) != n
}

// Find returns the client's session, if it is let in.
func (m *Manager) Find(ip string) (Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for _, s := range m.load().Sessions {
		if s.IP == ip && now.Before(s.Expires) {
			return s, true
		}
	}
	return Session{}, false
}

// Login is a guest's answer on the portal page: accept (click-through), a
// password or a voucher code. It lets the client in and returns its session.
func (m *Manager) Login(iface string, p config.CaptivePortal, ip netip.Addr, mac net.HardwareAddr, accept bool, password, voucher string) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if len(mac) == 0 {
		return Session{}, ErrNoClient
	}
	if p.Mode != "click" && m.tooMany(ip.String(), now) {
		return Session{}, ErrTooMany
	}
	s := Session{Interface: iface, MAC: mac.String(), IP: ip.String(), Method: p.Mode, Started: now,
		Expires: now.Add(time.Duration(p.Session()) * time.Minute)}
	// terms to accept: always for click-through, else when there is a text
	if !accept && (p.Mode == "click" || strings.TrimSpace(p.Text) != "") {
		return Session{}, ErrTerms
	}
	switch p.Mode {
	case "click":
	case "password":
		if password != p.Password || p.Password == "" {
			m.fail(ip.String(), now)
			return Session{}, ErrPassword
		}
	case "voucher":
		b, c, ok := m.voucher(iface, voucher, now)
		if !ok {
			m.fail(ip.String(), now)
			return Session{}, ErrVoucher
		}
		c.Used++
		if c.FirstUsed.IsZero() {
			c.FirstUsed = now
		}
		s.Voucher, s.Batch = c.Code, b.ID
		s.Expires = now.Add(time.Duration(b.Minutes) * time.Minute)
	default:
		return Session{}, ErrNoPortal
	}
	m.prune(now)
	st := m.load()
	// one session per client: a new login replaces the old one
	st.Sessions = slices.DeleteFunc(st.Sessions, func(o Session) bool { return o.MAC == s.MAC || o.IP == s.IP })
	st.Sessions = append(st.Sessions, s)
	if err := m.save(); err != nil {
		return Session{}, err
	}
	m.nft(nft.PortalScript([]nft.PortalClient{{MAC: s.MAC, IP: s.IP, Timeout: s.Expires.Sub(now)}}))
	m.log().Info("portal: client let in", "interface", iface, "mac", s.MAC, "ip", s.IP, "method", s.Method, "voucher", s.Voucher)
	return s, nil
}

// voucher finds a usable code (case and dashes do not matter); callers hold mu.
func (m *Manager) voucher(iface, code string, now time.Time) (*Batch, *Code, bool) {
	want := normCode(code)
	if want == "" {
		return nil, nil, false
	}
	st := m.load()
	for i := range st.Batches {
		b := &st.Batches[i]
		if b.Interface != "" && b.Interface != iface {
			continue
		}
		for j := range b.Codes {
			c := &b.Codes[j]
			if normCode(c.Code) == want {
				st := b.State(*c, now)
				return b, c, st == "unused" || st == "in_use"
			}
		}
	}
	return nil, nil, false
}

func normCode(s string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(s)))
}

const maxAttempts = 10

func (m *Manager) tooMany(ip string, now time.Time) bool {
	recent := slices.DeleteFunc(m.attempts[ip], func(t time.Time) bool { return now.Sub(t) > time.Minute })
	if m.attempts != nil {
		m.attempts[ip] = recent
	}
	return len(recent) >= maxAttempts
}

func (m *Manager) fail(ip string, now time.Time) {
	if m.attempts == nil {
		m.attempts = map[string][]time.Time{}
	}
	m.attempts[ip] = append(m.attempts[ip], now)
}

// Clients returns the let-in clients, newest first.
func (m *Manager) Clients() []Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.prune(m.now()) {
		_ = m.save()
	}
	out := slices.Clone(m.load().Sessions)
	slices.SortFunc(out, func(a, b Session) int { return b.Started.Compare(a.Started) })
	return out
}

// Kick ends the session of the client with this MAC.
func (m *Manager) Kick(mac string) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.kick(func(s Session) bool { return strings.EqualFold(s.MAC, mac) })
}

// kick removes the matching sessions; callers hold mu. It returns the first.
func (m *Manager) kick(match func(Session) bool) (Session, error) {
	st := m.load()
	var gone []Session
	st.Sessions = slices.DeleteFunc(st.Sessions, func(s Session) bool {
		if match(s) {
			gone = append(gone, s)
			return true
		}
		return false
	})
	if len(gone) == 0 {
		return Session{}, ErrNotFound
	}
	if err := m.save(); err != nil {
		return Session{}, err
	}
	for _, s := range gone {
		for _, l := range nft.PortalRemoveScript(s.MAC, s.IP) {
			if m.RunNFT != nil {
				_ = m.RunNFT([]byte(l + "\n")) // a missing element is fine
			}
		}
		m.log().Info("portal: client removed", "interface", s.Interface, "mac", s.MAC, "ip", s.IP)
	}
	return gone[0], nil
}

// Resync puts the sessions back into the nft sets (after a ruleset load) and
// ends the sessions of networks that no longer have a portal.
func (m *Manager) Resync(live *config.Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	active := live.ActivePortals()
	changed := m.prune(now)
	st := m.load()
	n := len(st.Sessions)
	st.Sessions = slices.DeleteFunc(st.Sessions, func(s Session) bool { _, ok := active[s.Interface]; return !ok })
	if changed || n != len(st.Sessions) {
		if err := m.save(); err != nil {
			m.log().Warn("portal: saving the state", "error", err)
		}
	}
	var cl []nft.PortalClient
	for _, s := range st.Sessions {
		cl = append(cl, nft.PortalClient{MAC: s.MAC, IP: s.IP, Timeout: s.Expires.Sub(now)})
	}
	m.nft(nft.PortalScript(cl))
}

// Sweep ends idle sessions: those older than their portal's idle timeout
// with no traffic seen within it. goblind runs it every 30 s.
func (m *Manager) Sweep() {
	if m.Seen == nil || m.Config == nil {
		return
	}
	cfg, err := m.Config()
	if err != nil {
		return
	}
	active := cfg.ActivePortals()
	idle := false
	for _, p := range active {
		idle = idle || p.IdleMinutes > 0
	}
	if !idle {
		return
	}
	seen, err := m.Seen()
	if err != nil {
		m.log().Debug("portal: traffic-seen set", "error", err)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	_, _ = m.kick(func(s Session) bool {
		p, ok := active[s.Interface]
		if !ok || p.IdleMinutes == 0 || now.Sub(s.Started) < time.Duration(p.IdleMinutes)*time.Minute {
			return false
		}
		if seen[s.MAC+" "+s.IP] {
			return false
		}
		m.log().Info("portal: idle session ended", "mac", s.MAC, "ip", s.IP)
		return true
	})
}

// NewBatch makes count voucher codes.
func (m *Manager) NewBatch(count, minutes, uses, validDays int, note, iface string) (Batch, error) {
	if count < 1 || count > 500 {
		return Batch{}, ErrBatchTooLarge
	}
	if minutes < 1 || minutes > 43200 {
		return Batch{}, fmt.Errorf("want 1-43200 minutes per use")
	}
	if uses == 0 {
		uses = 1
	}
	if uses < 1 || uses > 100 {
		return Batch{}, fmt.Errorf("want 1-100 uses per code")
	}
	if validDays < 0 || validDays > 3650 {
		return Batch{}, fmt.Errorf("want 0-3650 days (0 = no end)")
	}
	if len([]rune(note)) > 80 {
		return Batch{}, fmt.Errorf("the note is at most 80 characters")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	b := Batch{ID: randomCode(8), Note: note, Interface: iface, Minutes: minutes, Uses: uses, Created: now.UTC()}
	if validDays > 0 {
		b.Expires = now.Add(time.Duration(validDays) * 24 * time.Hour).UTC()
	}
	st := m.load()
	taken := map[string]bool{}
	for _, ob := range st.Batches {
		for _, c := range ob.Codes {
			taken[normCode(c.Code)] = true
		}
	}
	for len(b.Codes) < count {
		c := randomCode(8)
		c = c[:4] + "-" + c[4:]
		if !taken[normCode(c)] {
			taken[normCode(c)] = true
			b.Codes = append(b.Codes, Code{Code: c})
		}
	}
	st.Batches = append(st.Batches, b)
	return b, m.save()
}

// Batches returns the voucher batches, newest first.
func (m *Manager) Batches() []Batch {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := slices.Clone(m.load().Batches)
	slices.SortStableFunc(out, func(a, b Batch) int { return b.Created.Compare(a.Created) })
	return out
}

// Revoke revokes a batch, or one code of it when code is not "". Clients
// let in with a revoked code are removed.
func (m *Manager) Revoke(id, code string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.load()
	for i := range st.Batches {
		b := &st.Batches[i]
		if b.ID != id {
			continue
		}
		if code == "" {
			b.Revoked = true
		} else {
			found := false
			for j := range b.Codes {
				if normCode(b.Codes[j].Code) == normCode(code) {
					b.Codes[j].Revoked, found = true, true
				}
			}
			if !found {
				return ErrNotFound
			}
		}
		if err := m.save(); err != nil {
			return err
		}
		_, _ = m.kick(func(s Session) bool { return s.Batch == id && (code == "" || normCode(s.Voucher) == normCode(code)) })
		return nil
	}
	return ErrNotFound
}

// DeleteBatch removes a batch (its clients stay until their time is up).
func (m *Manager) DeleteBatch(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.load()
	n := len(st.Batches)
	st.Batches = slices.DeleteFunc(st.Batches, func(b Batch) bool { return b.ID == id })
	if n == len(st.Batches) {
		return ErrNotFound
	}
	return m.save()
}

// codeAlphabet leaves out 0/O and 1/I/L, which look alike on paper.
const codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// randomCode draws n letters without modulo bias (bytes from 248 up are
// dropped: 248 is the largest multiple of the alphabet's 31 letters).
func randomCode(n int) string {
	out := make([]byte, 0, n)
	buf := make([]byte, 2*n)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			panic(err)
		}
		for _, c := range buf {
			if int(c) < 256-256%len(codeAlphabet) && len(out) < n {
				out = append(out, codeAlphabet[int(c)%len(codeAlphabet)])
			}
		}
	}
	return string(out)
}
