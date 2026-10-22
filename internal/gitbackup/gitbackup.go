// Package gitbackup records each committed config revision as a commit in a
// git repository on the box and pushes it to a remote over SSH with the
// box's own deploy key (task 098). Secrets never enter the repository
// (config.RedactSecrets), and a push never blocks or fails a commit.
package gitbackup

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// validRemote is config.ValidGitRemote; tests allow a local bare repository.
var validRemote = config.ValidGitRemote

// DefaultBranch is the branch pushed to when git_backup.branch is "".
const DefaultBranch = "main"

// Revisions is the part of config.Store the manager reads.
type Revisions interface {
	ListRevisions() ([]config.RevisionInfo, error)
	Revision(n int) (*config.Config, error)
}

// Status is GET /api/v1/git-backup.
type Status struct {
	Enabled        bool      `json:"enabled"`
	Remote         string    `json:"remote,omitempty"`
	Branch         string    `json:"branch"`
	PublicKey      string    `json:"public_key,omitempty"` // add it to the remote as a deploy key with write access
	HostKey        string    `json:"host_key,omitempty"`   // the remote's pinned SSH host key (SHA256 fingerprint)
	LastRevision   int       `json:"last_revision,omitempty"`
	PushedRevision int       `json:"pushed_revision,omitempty"`
	LastPush       time.Time `json:"last_push,omitzero"`
	Pending        bool      `json:"pending"` // committed revisions not pushed yet
	LastError      string    `json:"last_error,omitempty"`
}

type state struct {
	LastRevision   int       `json:"last_revision"`
	PushedRevision int       `json:"pushed_revision"`
	LastPush       time.Time `json:"last_push"`
	LastError      string    `json:"last_error,omitempty"`
	RemoteHost     string    `json:"remote_host,omitempty"` // known_hosts belongs to this host
}

// Manager owns the repository. Set the exported fields before use.
type Manager struct {
	Dir        string // e.g. /var/lib/goblingate/git-backup
	Store      Revisions
	Logger     *slog.Logger
	Git        string        // git binary, default "git"
	RetryEvery time.Duration // failed pushes are retried this often, default 5 min
	Hostname   func() string // for the key comment and committer, default os.Hostname

	mu    sync.Mutex // the repository and the state
	st    state
	cfg   config.GitBackup
	kick  chan struct{}
	ready bool
}

func (m *Manager) logger() *slog.Logger {
	if m.Logger != nil {
		return m.Logger
	}
	return slog.Default()
}

func (m *Manager) repoDir() string   { return filepath.Join(m.Dir, "repo") }
func (m *Manager) keyPath() string   { return filepath.Join(m.Dir, "key", "id_ed25519") }
func (m *Manager) hostsPath() string { return filepath.Join(m.Dir, "key", "known_hosts") }
func (m *Manager) statePath() string { return filepath.Join(m.Dir, "state.json") }
func (m *Manager) branch() string    { return orDefault(m.cfg.Branch, DefaultBranch) }

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func (m *Manager) hostname() string {
	if m.Hostname != nil {
		return m.Hostname()
	}
	h, _ := os.Hostname()
	return orDefault(h, "goblin-gate")
}

func (m *Manager) init() {
	if m.ready {
		return
	}
	m.ready = true
	m.kick = make(chan struct{}, 1)
	if b, err := os.ReadFile(m.statePath()); err == nil {
		json.Unmarshal(b, &m.st)
	}
}

func (m *Manager) saveState() {
	b, _ := json.MarshalIndent(m.st, "", "  ")
	if err := os.WriteFile(m.statePath(), b, 0o600); err != nil {
		m.logger().Warn("git backup: saving state", "error", err)
	}
}

// git runs git in the repository with an environment of its own: no user
// or system config, no prompts, and ssh with the deploy key and the pinned
// host key only.
func (m *Manager) git(ctx context.Context, args ...string) ([]byte, error) {
	bin := orDefault(m.Git, "git")
	cmd := exec.CommandContext(ctx, bin, append([]string{"-C", m.repoDir()}, args...)...)
	sshCmd := strings.Join([]string{"ssh", "-F", "/dev/null", "-i", shellQuote(m.keyPath()), "-o", "IdentitiesOnly=yes",
		"-o", "UserKnownHostsFile=" + shellQuote(m.hostsPath()), "-o", "StrictHostKeyChecking=accept-new",
		"-o", "BatchMode=yes", "-o", "ConnectTimeout=15"}, " ")
	cmd.Env = []string{"HOME=" + m.Dir, "PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		"GIT_SSH_COMMAND=" + sshCmd, "LC_ALL=C"}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err != nil {
		return out.Bytes(), fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(out.String()))
	}
	return out.Bytes(), nil
}

// shellQuote quotes a path for GIT_SSH_COMMAND (git runs it through sh).
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// ensure creates the repository and the deploy key.
func (m *Manager) ensure(ctx context.Context) error {
	for _, d := range []string{m.Dir, filepath.Dir(m.keyPath())} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	if err := m.ensureKey(); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(m.repoDir(), ".git")); err == nil {
		return nil
	}
	if err := os.MkdirAll(m.repoDir(), 0o700); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", DefaultBranch},
		{"config", "user.name", "Goblin Gate"},
		{"config", "user.email", "goblin-gate@" + m.hostname()},
		{"config", "commit.gpgsign", "false"},
	} {
		if _, err := m.git(ctx, args...); err != nil {
			return err
		}
	}
	readme := "# Goblin Gate configuration history\n\nOne commit per committed revision of " + m.hostname() +
		". Secrets (password hashes, two-factor secrets, tokens, notification credentials) are removed;\n" +
		"WireGuard private keys never were in the configuration.\n"
	return os.WriteFile(filepath.Join(m.repoDir(), "README.md"), []byte(readme), 0o600)
}

func (m *Manager) ensureKey() error {
	if _, err := os.Stat(m.keyPath()); err == nil {
		return nil
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	block, err := ssh.MarshalPrivateKey(priv, "goblin-gate@"+m.hostname())
	if err != nil {
		return err
	}
	return os.WriteFile(m.keyPath(), pem.EncodeToMemory(block), 0o600)
}

func (m *Manager) publicKey() string {
	b, err := os.ReadFile(m.keyPath())
	if err != nil {
		return ""
	}
	signer, err := ssh.ParsePrivateKey(b)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) + " goblin-gate@" + m.hostname()
}

func (m *Manager) hostKey() string {
	b, err := os.ReadFile(m.hostsPath())
	if err != nil {
		return ""
	}
	var fps []string
	for len(b) > 0 {
		_, hosts, key, _, rest, err := ssh.ParseKnownHosts(b)
		if err != nil {
			break
		}
		fps = append(fps, strings.Join(hosts, ",")+" "+key.Type()+" "+ssh.FingerprintSHA256(key))
		b = rest
	}
	return strings.Join(fps, "\n")
}

// Sync records the revisions committed since the last one (all stored
// revisions the first time) and schedules a push. Call it after every
// apply with the running config.
func (m *Manager) Sync(ctx context.Context, live *config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	m.cfg = live.GitBackup
	if !m.cfg.Enabled {
		return nil
	}
	if err := m.ensure(ctx); err != nil {
		return m.fail(fmt.Errorf("prepare the repository: %w", err))
	}
	if err := m.setRemote(ctx); err != nil {
		return m.fail(err)
	}
	revs, err := m.Store.ListRevisions()
	if err != nil {
		return m.fail(err)
	}
	for _, r := range revs {
		if r.Revision <= m.st.LastRevision {
			continue
		}
		if err := m.record(ctx, r); err != nil {
			return m.fail(fmt.Errorf("revision %d: %w", r.Revision, err))
		}
		m.st.LastRevision = r.Revision
	}
	m.saveState()
	if m.cfg.Remote != "" && m.st.PushedRevision < m.st.LastRevision {
		select {
		case m.kick <- struct{}{}:
		default:
		}
	}
	return nil
}

func (m *Manager) fail(err error) error {
	m.st.LastError = err.Error()
	m.saveState()
	m.logger().Warn("git backup", "error", err)
	return err
}

// setRemote points origin at the configured remote; a new host forgets the
// pinned host key (the next push pins the new host's).
func (m *Manager) setRemote(ctx context.Context) error {
	remotes, _ := m.git(ctx, "remote")
	has := strings.Contains("\n"+string(remotes), "\norigin\n")
	if m.cfg.Remote == "" {
		if has {
			_, err := m.git(ctx, "remote", "remove", "origin")
			return err
		}
		return nil
	}
	if !validRemote(m.cfg.Remote) { // validation refuses these; never hand one to git
		return errors.New("invalid remote")
	}
	verb := "add"
	if has {
		verb = "set-url"
	}
	if _, err := m.git(ctx, "remote", verb, "origin", m.cfg.Remote); err != nil {
		return err
	}
	if h := remoteHost(m.cfg.Remote); h != m.st.RemoteHost {
		os.Remove(m.hostsPath())
		m.st.RemoteHost = h
		m.st.PushedRevision = 0 // a new remote gets the whole history
	}
	return nil
}

func remoteHost(remote string) string {
	if u, err := url.Parse(remote); err == nil && u.Scheme == "ssh" {
		return u.Host
	}
	if _, rest, ok := strings.Cut(remote, "@"); ok {
		h, _, _ := strings.Cut(rest, ":")
		return h
	}
	return remote
}

func (m *Manager) record(ctx context.Context, r config.RevisionInfo) error {
	cfg, err := m.Store.Revision(r.Revision)
	if err != nil {
		return err
	}
	config.RedactSecrets(cfg)
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(m.repoDir(), "config.json"), append(b, '\n'), 0o600); err != nil {
		return err
	}
	if _, err := m.git(ctx, "add", "config.json", "README.md"); err != nil {
		return err
	}
	if _, err := m.git(ctx, "diff", "--cached", "--quiet"); err == nil {
		return nil // the same config as the last commit
	}
	user := sanitize(orDefault(r.User, "unknown"))
	msg := "r" + strconv.Itoa(r.Revision)
	if c := strings.TrimSpace(r.Comment); c != "" {
		msg += ": " + c
	}
	msg += "\n\nRevision " + strconv.Itoa(r.Revision) + " committed by " + user + " on " + m.hostname() + "."
	args := []string{"commit", "-q", "-m", msg, "--author", user + " <" + user + "@" + m.hostname() + ">"}
	if r.Time != "" {
		args = append(args, "--date", r.Time)
	}
	_, err = m.git(ctx, args...)
	return err
}

// sanitize keeps a user name usable in a git author line.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '<' || r == '>' || r == '\n' || r == '\r' || r == 0 {
			return '_'
		}
		return r
	}, s)
}

// Run pushes after each Sync that recorded something, and retries a failed
// push every RetryEvery.
func (m *Manager) Run(ctx context.Context) {
	m.mu.Lock()
	m.init()
	kick := m.kick
	m.mu.Unlock()
	every := m.RetryEvery
	if every <= 0 {
		every = 5 * time.Minute
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-kick:
		case <-t.C:
		}
		m.mu.Lock()
		due := m.cfg.Enabled && m.cfg.Remote != "" && m.st.PushedRevision < m.st.LastRevision
		m.mu.Unlock()
		if due {
			m.Push(ctx)
		}
	}
}

// Push pushes the history now ("Test push" in the UI); the error is also
// kept in the status.
func (m *Manager) Push(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	if !m.cfg.Enabled || m.cfg.Remote == "" {
		return errors.New("set a remote and turn the git history on first (and commit)")
	}
	if _, err := os.Stat(filepath.Join(m.repoDir(), ".git")); err != nil {
		return errors.New("nothing recorded yet: commit once with the git history on")
	}
	pctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if _, err := m.git(pctx, "push", "-q", "origin", "HEAD:refs/heads/"+m.branch()); err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "REMOTE HOST IDENTIFICATION HAS CHANGED") || strings.Contains(msg, "Host key verification failed"):
			msg = "the remote's SSH host key is not the one pinned at the first push; if the server really changed, use Forget host key: " + msg
		case strings.Contains(msg, "Permission denied") || strings.Contains(msg, "publickey"):
			msg = "the remote refused the box's key: add the public key as a deploy key with write access: " + msg
		case strings.Contains(msg, "non-fast-forward") || strings.Contains(msg, "fetch first"):
			msg = "the remote branch has commits the box does not have; push to a new branch or empty repository: " + msg
		}
		m.st.LastError = msg
		m.saveState()
		m.logger().Warn("git backup: push failed", "error", msg)
		return errors.New(msg)
	}
	m.st.PushedRevision, m.st.LastPush, m.st.LastError = m.st.LastRevision, time.Now().UTC(), ""
	m.saveState()
	m.logger().Info("git backup: pushed", "revision", m.st.LastRevision)
	return nil
}

// ForgetHostKey drops the pinned host key (the server was really
// reinstalled or moved); the next push pins the new one.
func (m *Manager) ForgetHostKey() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.Remove(m.hostsPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// PushState reports the last successful push and the last error, without
// the side effects of Status (no deploy key is created).
func (m *Manager) PushState() (lastPush time.Time, lastError string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	return m.st.LastPush, m.st.LastError
}

// Status reports the history and the push state for live's settings. The
// deploy key is created here when needed, so it can be added to the
// remote before the first commit.
func (m *Manager) Status(live *config.Config) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	g := live.GitBackup
	if err := os.MkdirAll(filepath.Dir(m.keyPath()), 0o700); err == nil {
		m.ensureKey()
	}
	return Status{
		Enabled: g.Enabled, Remote: g.Remote, Branch: orDefault(g.Branch, DefaultBranch),
		PublicKey: m.publicKey(), HostKey: m.hostKey(),
		LastRevision: m.st.LastRevision, PushedRevision: m.st.PushedRevision, LastPush: m.st.LastPush,
		Pending:   g.Enabled && g.Remote != "" && m.st.PushedRevision < m.st.LastRevision,
		LastError: m.st.LastError,
	}
}
