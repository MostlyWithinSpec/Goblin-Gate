package gitbackup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func needGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit saves a revision like a commit does.
func commit(t *testing.T, s *config.Store, cfg *config.Config, user, comment string) {
	t.Helper()
	if _, err := s.SaveRevision(cfg, user, comment); err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T) (*Manager, *config.Store, *config.Config) {
	needGit(t)
	old := validRemote
	validRemote = func(string) bool { return true }
	t.Cleanup(func() { validRemote = old })
	store, err := config.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Admins = []config.Admin{{Username: "alice", Role: "admin", PasswordHash: "$argon2id$secret-hash"}}
	cfg.DDNS = []config.DDNS{{Name: "home", Provider: "duckdns", Hostname: "h.duckdns.org", Token: "ddns-secret"}}
	m := &Manager{Dir: t.TempDir(), Store: store, Hostname: func() string { return "gw1" }}
	return m, store, cfg
}

func TestRecordsRevisionsWithoutSecrets(t *testing.T) {
	m, store, cfg := setup(t)
	ctx := context.Background()
	commit(t, store, cfg, "alice", "first")
	cfg.System.Hostname = "gw2"
	commit(t, store, cfg, "alice", "rename")

	// off: nothing happens
	if err := m.Sync(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.repoDir()); err == nil {
		t.Fatal("repository created while off")
	}
	cfg.GitBackup = config.GitBackup{Enabled: true}
	commit(t, store, cfg, "bob", "turn on git history")
	if err := m.Sync(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	log := gitOut(t, m.repoDir(), "log", "--format=%an|%s")
	if want := "bob|r3: turn on git history\nalice|r2: rename\nalice|r1: first"; log != want {
		t.Fatalf("log:\n%s\nwant:\n%s", log, want)
	}
	all := gitOut(t, m.repoDir(), "log", "-p")
	for _, secret := range []string{"secret-hash", "ddns-secret"} {
		if strings.Contains(all, secret) {
			t.Fatalf("history contains %q", secret)
		}
	}
	if !strings.Contains(all, `"hostname": "gw2"`) {
		t.Fatal("config missing from the history")
	}
	// nothing new: no new commit; an identical revision adds none either
	commit(t, store, cfg, "bob", "no change")
	m.Sync(ctx, cfg)
	if n := gitOut(t, m.repoDir(), "rev-list", "--count", "HEAD"); n != "3" {
		t.Fatalf("%s commits, want 3", n)
	}
	if st := m.Status(cfg); st.LastRevision != 4 || st.Pending || !strings.HasPrefix(st.PublicKey, "ssh-ed25519 ") || !strings.HasSuffix(st.PublicKey, " goblin-gate@gw1") {
		t.Fatalf("status %+v", st)
	}
	if fi, _ := os.Stat(m.keyPath()); fi == nil || (fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/') {
		t.Fatal("deploy key missing or readable by others")
	}
}

func TestPushesToRemote(t *testing.T) {
	m, store, cfg := setup(t)
	ctx := context.Background()
	bare := filepath.Join(t.TempDir(), "remote.git")
	gitOut(t, ".", "init", "-q", "--bare", bare)
	cfg.GitBackup = config.GitBackup{Enabled: true, Remote: bare, Branch: "gw1-config"}
	commit(t, store, cfg, "alice", "first")
	if err := m.Sync(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if st := m.Status(cfg); !st.Pending {
		t.Fatalf("not pending before the push: %+v", st)
	}
	if err := m.Push(ctx); err != nil {
		t.Fatal(err)
	}
	if got, want := gitOut(t, bare, "rev-parse", "gw1-config"), gitOut(t, m.repoDir(), "rev-parse", "HEAD"); got != want {
		t.Fatalf("remote %s, local %s", got, want)
	}
	if st := m.Status(cfg); st.Pending || st.PushedRevision != 1 || st.LastError != "" || st.LastPush.IsZero() {
		t.Fatalf("after push: %+v", st)
	}

	// someone else pushed to the branch: refused, explained, not forced
	other := filepath.Join(t.TempDir(), "other")
	gitOut(t, ".", "clone", "-q", "-b", "gw1-config", bare, other)
	os.WriteFile(filepath.Join(other, "x"), []byte("x"), 0o644)
	gitOut(t, other, "add", "x")
	gitOut(t, other, "-c", "user.name=x", "-c", "user.email=x@x", "commit", "-qm", "x")
	gitOut(t, other, "push", "-q", "origin", "gw1-config")
	cfg.System.Hostname = "gw9"
	commit(t, store, cfg, "alice", "second")
	m.Sync(ctx, cfg)
	if err := m.Push(ctx); err == nil || !strings.Contains(err.Error(), "commits the box does not have") {
		t.Fatalf("push over foreign commits: %v", err)
	}
	if st := m.Status(cfg); !st.Pending || st.LastError == "" {
		t.Fatalf("status after a failed push: %+v", st)
	}
}

func TestPushNeedsRemote(t *testing.T) {
	m, _, cfg := setup(t)
	cfg.GitBackup = config.GitBackup{Enabled: true}
	m.Sync(context.Background(), cfg)
	if err := m.Push(context.Background()); err == nil {
		t.Fatal("pushed without a remote")
	}
}

func TestRemoteHost(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:me/x.git":           "github.com",
		"ssh://git@gitea.lan:2222/me/x.git": "gitea.lan:2222",
	} {
		if got := remoteHost(in); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
}
