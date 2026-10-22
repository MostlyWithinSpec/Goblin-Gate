// Package updates installs Goblin Gate updates safely (task 051): it checks
// the signed APT repository for a newer goblin-gate package (and counts
// pending Debian updates), and installs one in a transient systemd unit that
// survives goblind restarting. Before installing it keeps the current package;
// if goblind does not come back healthy with the new version within Timeout,
// it reinstalls the kept package (rollback).
package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// DefaultDir keeps the state file and the rollback package.
const DefaultDir = "/var/lib/goblingate/updates"

// Package is the Debian package that is updated.
const Package = "goblin-gate"

// State is the last (or running) update, persisted in Dir/state.json.
type State struct {
	State     string    `json:"state"` // running | done | rolled_back | failed
	From      string    `json:"from,omitempty"`
	To        string    `json:"to,omitempty"`
	IncludeOS bool      `json:"include_os,omitempty"`
	Step      string    `json:"step,omitempty"` // what a running update is doing
	Message   string    `json:"message,omitempty"`
	Started   time.Time `json:"started"`
	Finished  time.Time `json:"finished,omitzero"`
}

// Status answers GET /api/v1/system/updates.
type Status struct {
	Installed  string    `json:"installed"`
	Available  string    `json:"available,omitempty"` // newer goblin-gate in the channel, "" = up to date
	ReleaseURL string    `json:"release_url,omitempty"`
	OSUpdates  int       `json:"os_updates"` // pending Debian package upgrades
	Checked    time.Time `json:"checked,omitzero"`
	CheckError string    `json:"check_error,omitempty"`
	Last       *State    `json:"last,omitempty"`
}

// Manager checks for and applies updates.
type Manager struct {
	Dir    string
	Logger *slog.Logger
	// Exec runs a command and returns its combined output (tests replace it).
	Exec func(ctx context.Context, env []string, name string, args ...string) ([]byte, error)
	// Health returns the version goblind reports, or an error while it is
	// not answering (the updater polls it after installing).
	Health  func(ctx context.Context) (string, error)
	Timeout time.Duration // how long goblind gets to come back; default 5 min
	Poll    time.Duration // health poll interval; default 3 s
	Now     func() time.Time
	// Self is the goblind binary started as the updater (default /usr/sbin/goblind).
	Self string

	mu      sync.Mutex
	cache   Status
	checkMu sync.Mutex // one apt-get update at a time (apt locks its lists)
}

func (m *Manager) dir() string {
	if m.Dir != "" {
		return m.Dir
	}
	return DefaultDir
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

// aptArgs makes apt-get wait for a lock held by another apt (an update
// check, apt-daily) instead of failing at once.
func aptArgs(name string, args []string) []string {
	if name != "apt-get" {
		return args
	}
	return append([]string{"-o", "DPkg::Lock::Timeout=300"}, args...)
}

// aptEnv keeps apt and dpkg from asking anything.
var aptEnv = []string{"DEBIAN_FRONTEND=noninteractive", "APT_LISTCHANGES_FRONTEND=none"}

func (m *Manager) exec(ctx context.Context, name string, args ...string) ([]byte, error) {
	args = aptArgs(name, args)
	if m.Exec != nil {
		return m.Exec(ctx, aptEnv, name, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), aptEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s: %w: %s", name, err, lastLines(string(out), 5))
	}
	return out, nil
}

// Installed returns the installed goblin-gate version.
func (m *Manager) Installed(ctx context.Context) (string, error) {
	out, err := m.exec(ctx, "dpkg-query", "-W", "-f=${Version}", Package)
	return strings.TrimSpace(string(out)), err
}

var candidateRe = regexp.MustCompile(`(?m)^\s*Candidate:\s*(\S+)`)

// Check refreshes the package lists and reports what can be updated. Results
// are cached for maxAge (0 = always check).
func (m *Manager) Check(ctx context.Context, maxAge time.Duration) Status {
	m.mu.Lock()
	cached := m.cache
	m.mu.Unlock()
	if maxAge > 0 && !cached.Checked.IsZero() && m.now().Sub(cached.Checked) < maxAge {
		cached.Last = m.LastState()
		return cached
	}
	asked := m.now()
	m.checkMu.Lock()
	defer m.checkMu.Unlock()
	m.mu.Lock()
	cached = m.cache
	m.mu.Unlock()
	if !cached.Checked.Before(asked) { // a check that ran while this one waited
		cached.Last = m.LastState()
		return cached
	}
	st := Status{Checked: m.now()}
	st.Installed, _ = m.Installed(ctx)
	if _, err := m.exec(ctx, "apt-get", "-q", "update"); err != nil {
		st.CheckError = "could not refresh the package lists: " + err.Error()
	}
	if out, err := m.exec(ctx, "apt-cache", "policy", Package); err == nil {
		if mm := candidateRe.FindStringSubmatch(string(out)); mm != nil && mm[1] != "(none)" && mm[1] != st.Installed {
			if newer, _ := m.newer(ctx, mm[1], st.Installed); newer {
				st.Available = mm[1]
				st.ReleaseURL = ReleaseURL(mm[1])
			}
		}
	}
	if out, err := m.exec(ctx, "apt-get", "-s", "-q", "upgrade"); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "Inst ") && !strings.HasPrefix(line, "Inst "+Package+" ") {
				st.OSUpdates++
			}
		}
	}
	m.mu.Lock()
	m.cache = st
	m.mu.Unlock()
	st.Last = m.LastState()
	return st
}

// Cached returns the result of the last check without running one; the
// zero Status when none ran yet.
func (m *Manager) Cached() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cache
}

// newer reports whether version a sorts after b in Debian order.
func (m *Manager) newer(ctx context.Context, a, b string) (bool, error) {
	if b == "" {
		return true, nil
	}
	_, err := m.exec(ctx, "dpkg", "--compare-versions", a, "gt", b)
	return err == nil, nil
}

// ReleaseURL is the GitHub release page of a package version
// (0.1.0~alpha.2 → tag v0.1.0-alpha.2). Untagged builds have none.
func ReleaseURL(version string) string {
	if strings.Contains(version, "+") {
		return ""
	}
	return "https://github.com/MostlyWithinSpec/Goblin-Gate/releases/tag/v" + strings.ReplaceAll(version, "~", "-")
}

func (m *Manager) statePath() string { return filepath.Join(m.dir(), "state.json") }

// LastState returns the last update, or nil if there was none.
func (m *Manager) LastState() *State {
	b, err := os.ReadFile(m.statePath())
	if err != nil {
		return nil
	}
	var s State
	if json.Unmarshal(b, &s) != nil {
		return nil
	}
	return &s
}

func (m *Manager) save(s State) {
	if err := os.MkdirAll(m.dir(), 0o750); err != nil {
		m.logger().Warn("updates: state dir", "error", err)
		return
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	tmp := m.statePath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o640); err == nil {
		os.Rename(tmp, m.statePath())
	}
}

// ErrRunning: an update is already in progress.
var ErrRunning = errors.New("updates: an update is already running")

// Start launches the updater in a transient systemd unit and returns at once.
// to = "" installs the channel's newest version.
func (m *Manager) Start(ctx context.Context, to string, includeOS bool) error {
	if last := m.LastState(); last != nil && last.State == "running" && m.now().Sub(last.Started) < 30*time.Minute {
		return ErrRunning
	}
	if to == "" {
		st := m.Check(ctx, 0)
		if st.Available == "" && !includeOS {
			return errors.New("updates: already up to date")
		}
		to = st.Available
	}
	from, _ := m.Installed(ctx)
	m.save(State{State: "running", From: from, To: to, IncludeOS: includeOS, Step: "starting", Started: m.now()})
	self := m.Self
	if self == "" {
		self = "/usr/sbin/goblind"
	}
	args := []string{"--unit=goblin-update-" + fmt.Sprint(m.now().Unix()), "--collect", "--quiet", self, "update-run", "--to", to}
	if includeOS {
		args = append(args, "--os")
	}
	if _, err := m.exec(ctx, "systemd-run", args...); err != nil {
		m.save(State{State: "failed", From: from, To: to, Message: "could not start the updater: " + err.Error(), Started: m.now(), Finished: m.now()})
		return err
	}
	return nil
}

// Run performs the update (inside the transient unit started by Start).
func (m *Manager) Run(ctx context.Context, to string, includeOS bool) State {
	from, _ := m.Installed(ctx)
	s := State{State: "running", From: from, To: to, IncludeOS: includeOS, Started: m.now()}
	step := func(name string) {
		s.Step = name
		m.save(s)
		m.logger().Info("updates: "+name, "from", from, "to", to)
	}
	finish := func(state, msg string) State {
		s.State, s.Message, s.Step, s.Finished = state, msg, "", m.now()
		m.save(s)
		m.logger().Info("updates: finished", "state", state, "message", msg)
		return s
	}

	// 1. keep the current package for a rollback
	step("saving the current version for rollback")
	keep := filepath.Join(m.dir(), "previous")
	os.RemoveAll(keep)
	os.MkdirAll(keep, 0o750)
	rollback := ""
	if from != "" {
		if _, err := m.execIn(ctx, keep, "apt-get", "download", Package+"="+from); err == nil {
			if debs, _ := filepath.Glob(filepath.Join(keep, "*.deb")); len(debs) == 1 {
				rollback = debs[0]
			}
		}
	}
	if rollback == "" && to != "" {
		return finish("failed", "the installed version "+from+" could not be saved for a rollback (is it still in the repository?); nothing was changed")
	}

	// 2. Debian updates first (goblin-gate is held so it moves only in step 3)
	if includeOS {
		step("installing Debian updates")
		m.exec(ctx, "apt-mark", "hold", Package)
		_, err := m.exec(ctx, "apt-get", "-y", "-q", "-o", "Dpkg::Options::=--force-confold", "-o", "Dpkg::Options::=--force-confdef", "upgrade")
		m.exec(ctx, "apt-mark", "unhold", Package)
		if err != nil {
			return finish("failed", "Debian updates failed: "+err.Error())
		}
		if to == "" {
			return finish("done", "Debian updates installed")
		}
	}

	// 3. the new goblin-gate
	step("installing " + to)
	_, installErr := m.exec(ctx, "apt-get", "install", "-y", "-q", "--allow-downgrades",
		"-o", "Dpkg::Options::=--force-confold", "-o", "Dpkg::Options::=--force-confdef", Package+"="+to)

	// 4. is it healthy?
	if installErr == nil {
		step("waiting for goblind " + to)
		if err := m.waitHealthy(ctx, to); err == nil {
			os.RemoveAll(keep)
			return finish("done", "updated from "+from+" to "+to)
		} else {
			installErr = err
		}
	}

	// 5. roll back
	step("rolling back to " + from)
	if _, err := m.exec(ctx, "dpkg", "-i", "--force-confold", rollback); err != nil {
		return finish("failed", fmt.Sprintf("update failed (%v) and the rollback failed too (%v): reinstall %s from the console", installErr, err, rollback))
	}
	// a crash-looping new goblind may have hit the unit's start limit
	m.exec(ctx, "systemctl", "reset-failed", "goblind.service")
	m.exec(ctx, "systemctl", "restart", "goblind.service")
	if err := m.waitHealthy(ctx, from); err != nil {
		return finish("failed", fmt.Sprintf("update failed (%v); %s was reinstalled but goblind is not answering (%v)", installErr, from, err))
	}
	return finish("rolled_back", fmt.Sprintf("update to %s failed (%v); %s was restored", to, installErr, from))
}

// waitHealthy polls Health until goblind reports want, or Timeout passes.
func (m *Manager) waitHealthy(ctx context.Context, want string) error {
	timeout, poll := m.Timeout, m.Poll
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	if poll <= 0 {
		poll = 3 * time.Second
	}
	deadline := time.Now().Add(timeout)
	var last error
	for {
		got, err := m.Health(ctx)
		switch {
		case err == nil && sameVersion(got, want):
			return nil
		case err == nil:
			last = fmt.Errorf("goblind reports version %s, want %s", got, want)
		default:
			last = err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("goblind not healthy after %s: %w", timeout, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

// sameVersion compares a goblind version string with a package version
// (the binary carries the package version; tolerate a leading v).
func sameVersion(binary, pkg string) bool {
	return strings.TrimPrefix(binary, "v") == strings.TrimPrefix(pkg, "v")
}

func (m *Manager) execIn(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	args = aptArgs(name, args)
	if m.Exec != nil {
		return m.Exec(ctx, append(aptEnv, "PWD="+dir), name, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), aptEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s: %w: %s", name, err, lastLines(string(out), 5))
	}
	return out, nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}
