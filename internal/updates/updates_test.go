package updates

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeBox simulates dpkg/apt: an installed version, the versions the
// repository has, and what goblind reports after an install.
type fakeBox struct {
	installed string
	repo      map[string]bool // versions apt can download/install
	broken    map[string]bool // versions whose goblind never comes up
	failApt   bool            // apt-get install fails
	calls     []string
	// first-party plugins: installed versions and what the channel has
	plugins    map[string]string
	pluginRepo map[string]string
	// the full (beta) suite, reached only with the private APT options
	betaRepo    map[string]bool
	betaUpdated bool
}

func (f *fakeBox) exec(_ context.Context, env []string, name string, args ...string) ([]byte, error) {
	line := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	if name == "apt-get" {
		if len(args) < 2 || args[0] != "-o" || args[1] != "DPkg::Lock::Timeout=300" {
			return nil, errors.New("apt-get without the lock timeout: " + line)
		}
		args = args[2:]
		private := false
		for len(args) >= 2 && args[0] == "-o" {
			if strings.HasPrefix(args[1], "Dir::Etc::SourceList=") {
				private = true
			}
			args = args[2:]
		}
		if private {
			return f.privateApt(env, args)
		}
	}
	if name == "dpkg-deb" { // -f FILE Package Version: the fake .deb holds "pkg=version"
		b, err := os.ReadFile(args[1])
		if err != nil {
			return nil, err
		}
		pkg, v, ok := strings.Cut(string(b), "=")
		if !ok {
			pkg, v = Package, string(b)
		}
		return []byte("Package: " + pkg + "\nVersion: " + v + "\n"), nil
	}
	if name == "dpkg-query" && strings.HasSuffix(args[len(args)-1], "*") {
		var lines []string
		for pkg, v := range f.plugins {
			lines = append(lines, pkg+" "+v+" ii")
		}
		return []byte(strings.Join(lines, "\n")), nil
	}
	if name == "apt-cache" && strings.HasPrefix(args[len(args)-1], PluginPrefix) {
		pkg := args[len(args)-1]
		return []byte(pkg + ":\n  Installed: " + f.plugins[pkg] + "\n  Candidate: " + f.pluginRepo[pkg] + "\n"), nil
	}
	if name == "apt-get" && args[0] == "download" && strings.HasPrefix(args[1], PluginPrefix) {
		pkg, v, _ := strings.Cut(args[1], "=")
		dir := ""
		for _, e := range env {
			if strings.HasPrefix(e, "PWD=") {
				dir = strings.TrimPrefix(e, "PWD=")
			}
		}
		return nil, os.WriteFile(filepath.Join(dir, pkg+"_"+v+"_amd64.deb"), []byte(args[1]), 0o644)
	}
	switch {
	case name == "dpkg-query":
		return []byte(f.installed), nil
	case name == "dpkg" && args[0] == "--compare-versions":
		if args[1] > args[3] { // good enough for these test versions
			return nil, nil
		}
		return nil, errors.New("exit status 1")
	case name == "apt-get" && args[0] == "download":
		v := strings.TrimPrefix(args[1], Package+"=")
		if !f.repo[v] {
			return nil, errors.New("E: Version '" + v + "' for 'goblin-gate' was not found")
		}
		dir := ""
		for _, e := range env {
			if strings.HasPrefix(e, "PWD=") {
				dir = strings.TrimPrefix(e, "PWD=")
			}
		}
		return nil, os.WriteFile(filepath.Join(dir, "goblin-gate_"+v+"_amd64.deb"), []byte(v), 0o644)
	case name == "apt-get" && args[0] == "install":
		if f.failApt {
			return []byte("E: Sub-process /usr/bin/dpkg returned an error code (1)"), errors.New("exit status 100")
		}
		for _, a := range args {
			if pkg, v, ok := strings.Cut(a, "="); ok && strings.HasPrefix(pkg, PluginPrefix) {
				f.plugins[pkg] = v
			}
		}
		f.installed = strings.TrimPrefix(args[len(args)-1], Package+"=")
		return nil, nil
	case name == "dpkg" && args[0] == "-i":
		for _, a := range args[1:] {
			if !strings.HasSuffix(a, ".deb") {
				continue
			}
			b, _ := os.ReadFile(a)
			if pkg, v, ok := strings.Cut(string(b), "="); ok {
				f.plugins[pkg] = v
			} else {
				f.installed = string(b)
			}
		}
		return nil, nil
	case name == "apt-cache":
		best := f.installed
		for v := range f.repo {
			if v > best {
				best = v
			}
		}
		return []byte("goblin-gate:\n  Installed: " + f.installed + "\n  Candidate: " + best + "\n"), nil
	case name == "apt-get" && args[0] == "-s":
		return []byte("Inst libc6 [2.36-9] (2.36-9+deb12u9 Debian-Security:12/stable-security [amd64])\nInst goblin-gate [0.1] (0.2 goblin:beta [amd64])\n"), nil
	}
	return nil, nil
}

func pwd(env []string) string {
	for _, e := range env {
		if strings.HasPrefix(e, "PWD=") {
			return strings.TrimPrefix(e, "PWD=")
		}
	}
	return ""
}

func (f *fakeBox) privateApt(env []string, args []string) ([]byte, error) {
	switch args[len(args)-1] {
	case "update":
		f.betaUpdated = true
		return nil, nil
	}
	if args[0] != "download" || !f.betaUpdated {
		return nil, errors.New("unexpected private apt " + strings.Join(args, " "))
	}
	pkg, v, _ := strings.Cut(args[1], "=")
	if !f.betaRepo[v] {
		return nil, errors.New("E: Version '" + v + "' for '" + pkg + "' was not found")
	}
	body := v
	if pkg != Package {
		body = args[1]
	}
	return nil, os.WriteFile(filepath.Join(pwd(env), pkg+"_"+v+"_amd64.deb"), []byte(body), 0o644)
}

func (f *fakeBox) health(context.Context) (string, error) {
	if f.broken[f.installed] {
		return "", errors.New("connection refused")
	}
	return f.installed, nil
}

func manager(t *testing.T, f *fakeBox) *Manager {
	dir := t.TempDir()
	return &Manager{Dir: dir, Exec: f.exec, Health: f.health, Timeout: 50 * time.Millisecond, Poll: 5 * time.Millisecond,
		Sources: filepath.Join(dir, "no.sources"), AptCache: filepath.Join(dir, "no-cache")}
}

func TestUpdateSucceeds(t *testing.T) {
	f := &fakeBox{installed: "0.1.0~alpha.1", repo: map[string]bool{"0.1.0~alpha.1": true, "0.1.0~alpha.2": true}}
	m := manager(t, f)
	s := m.Run(context.Background(), "0.1.0~alpha.2", false)
	if s.State != "done" || f.installed != "0.1.0~alpha.2" {
		t.Fatalf("%+v installed=%s", s, f.installed)
	}
	if last := m.LastState(); last == nil || last.State != "done" || last.From != "0.1.0~alpha.1" {
		t.Fatalf("state file %+v", last)
	}
	if _, err := os.Stat(filepath.Join(m.Dir, "previous")); !os.IsNotExist(err) {
		t.Error("rollback copy not cleaned up after success")
	}
}

func TestBrokenUpdateRollsBack(t *testing.T) {
	f := &fakeBox{installed: "0.1.0~alpha.1", repo: map[string]bool{"0.1.0~alpha.1": true, "0.1.0~alpha.2": true},
		broken: map[string]bool{"0.1.0~alpha.2": true}}
	s := manager(t, f).Run(context.Background(), "0.1.0~alpha.2", false)
	if s.State != "rolled_back" || f.installed != "0.1.0~alpha.1" || !strings.Contains(s.Message, "not healthy") {
		t.Fatalf("%+v installed=%s", s, f.installed)
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "systemctl restart goblind.service") {
		t.Error("goblind not restarted after the rollback")
	}
}

func TestFailedInstallRollsBack(t *testing.T) {
	f := &fakeBox{installed: "0.1.0~alpha.1", repo: map[string]bool{"0.1.0~alpha.1": true, "0.1.0~alpha.2": true}, failApt: true}
	s := manager(t, f).Run(context.Background(), "0.1.0~alpha.2", false)
	if s.State != "rolled_back" || f.installed != "0.1.0~alpha.1" {
		t.Fatalf("%+v", s)
	}
}

func TestNoRollbackCopyMeansNoUpdate(t *testing.T) {
	f := &fakeBox{installed: "0.0.0+gabc", repo: map[string]bool{"0.1.0~alpha.2": true}}
	s := manager(t, f).Run(context.Background(), "0.1.0~alpha.2", false)
	if s.State != "failed" || f.installed != "0.0.0+gabc" || !strings.Contains(s.Message, "nothing was changed") {
		t.Fatalf("%+v", s)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "apt-get install") {
			t.Fatal("installed without a rollback copy")
		}
	}
}

func TestCheck(t *testing.T) {
	f := &fakeBox{installed: "0.1.0~alpha.1", repo: map[string]bool{"0.1.0~alpha.1": true, "0.1.0~alpha.2": true}}
	m := manager(t, f)
	st := m.Check(context.Background(), 0)
	if st.Installed != "0.1.0~alpha.1" || st.Available != "0.1.0~alpha.2" || st.OSUpdates != 1 {
		t.Fatalf("%+v", st)
	}
	if st.ReleaseURL != "https://github.com/MostlyWithinSpec/Goblin-Gate/releases/tag/v0.1.0-alpha.2" {
		t.Fatalf("release url %s", st.ReleaseURL)
	}
	// cached within maxAge: no new apt-get update
	n := len(f.calls)
	m.Check(context.Background(), time.Hour)
	if len(f.calls) != n {
		t.Fatal("cached check ran commands")
	}
	if ReleaseURL("0.1.0~alpha.2+3.gabc") != "" {
		t.Error("nightly builds have no release page")
	}
}

func TestStartRefusesWhileRunning(t *testing.T) {
	f := &fakeBox{installed: "0.1.0~alpha.1", repo: map[string]bool{"0.1.0~alpha.1": true, "0.1.0~alpha.2": true}}
	m := manager(t, f)
	if err := m.Start(context.Background(), "", false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "systemd-run --unit=goblin-update-") {
		t.Fatalf("updater not started in a unit: %v", f.calls)
	}
	if err := m.Start(context.Background(), "", false); !errors.Is(err, ErrRunning) {
		t.Fatalf("second start: %v", err)
	}
}

func pluginBox(broken bool) *fakeBox {
	f := &fakeBox{installed: "0.1.0~beta.7", repo: map[string]bool{"0.1.0~beta.7": true, "0.1.0~beta.8": true},
		plugins:    map[string]string{"goblin-gate-plugin-sentry": "0.1.0~beta.2"},
		pluginRepo: map[string]string{"goblin-gate-plugin-sentry": "0.1.0~beta.8"}}
	if broken {
		f.broken = map[string]bool{"0.1.0~beta.8": true}
	}
	return f
}

// First-party plugins update together with goblin-gate.
func TestPluginsUpdateWithGoblinGate(t *testing.T) {
	f := pluginBox(false)
	s := manager(t, f).Run(context.Background(), "0.1.0~beta.8", false)
	if s.State != "done" || f.installed != "0.1.0~beta.8" || f.plugins["goblin-gate-plugin-sentry"] != "0.1.0~beta.8" {
		t.Fatalf("%+v installed=%s plugins=%v", s, f.installed, f.plugins)
	}
}

// ...and roll back with it.
func TestPluginsRollBackWithGoblinGate(t *testing.T) {
	f := pluginBox(true)
	s := manager(t, f).Run(context.Background(), "0.1.0~beta.8", false)
	if s.State != "rolled_back" || f.installed != "0.1.0~beta.7" || f.plugins["goblin-gate-plugin-sentry"] != "0.1.0~beta.2" {
		t.Fatalf("%+v installed=%s plugins=%v", s, f.installed, f.plugins)
	}
}

// A box that is current but whose plugin lags gets an update offered, and
// the plugin isn't counted as a Debian update.
func TestCheckOffersALaggingPlugin(t *testing.T) {
	f := pluginBox(false)
	f.installed = "0.1.0~beta.8"
	st := manager(t, f).Check(context.Background(), 0)
	if st.Available != "0.1.0~beta.8" || len(st.Plugins) != 1 || st.Plugins[0].Available != "0.1.0~beta.8" {
		t.Fatalf("%+v", st)
	}
}

// After an update, the last check's "available" is forgotten: it named the
// version that is installed now.
func TestCheckForgottenAfterUpdate(t *testing.T) {
	f := &fakeBox{installed: "0.1.0~alpha.1", repo: map[string]bool{"0.1.0~alpha.1": true, "0.1.0~alpha.2": true}}
	m := manager(t, f)
	if st := m.Check(context.Background(), 0); st.Available != "0.1.0~alpha.2" {
		t.Fatalf("%+v", st)
	}
	m.Run(context.Background(), "0.1.0~alpha.2", false)
	if c := m.Cached(); c.Available != "" {
		t.Fatalf("stale cache %+v", c)
	}
}

// stableBox: a beta installed while the channel is stable (task 163).
func stableBox(t *testing.T, f *fakeBox) (*Manager, *bytes.Buffer) {
	m := manager(t, f)
	m.Sources = filepath.Join(t.TempDir(), "goblin-gate.sources")
	os.WriteFile(m.Sources, []byte("# Managed by Goblin Gate\nTypes: deb\nURIs: https://repo.example/apt\nSuites: stable\nComponents: main\nSigned-By: /usr/share/keyrings/goblin-gate-archive.gpg\n"), 0o644)
	m.AptCache = t.TempDir()
	var log bytes.Buffer
	m.Logger = slog.New(slog.NewTextHandler(&log, nil))
	return m, &log
}

func TestRollbackCopyFromChannel(t *testing.T) {
	f := &fakeBox{installed: "0.2.0", repo: map[string]bool{"0.2.0": true, "0.2.1": true}}
	m, _ := stableBox(t, f)
	if s := m.Run(context.Background(), "0.2.1", false); s.State != "done" {
		t.Fatalf("%+v", s)
	}
	if f.betaUpdated {
		t.Error("full suite used although the channel had the version")
	}
}

func TestRollbackCopyFromAptCache(t *testing.T) {
	f := &fakeBox{installed: "0.2.0~beta.1", repo: map[string]bool{"0.2.0": true}, broken: map[string]bool{"0.2.0": true}}
	m, log := stableBox(t, f)
	// a file whose name lies is skipped; the right one is used
	os.WriteFile(filepath.Join(m.AptCache, "goblin-gate_0.2.0~beta.1_arm64.deb"), []byte("0.1.9"), 0o644)
	os.WriteFile(filepath.Join(m.AptCache, "goblin-gate_0.2.0~beta.1_amd64.deb"), []byte("0.2.0~beta.1"), 0o644)
	s := m.Run(context.Background(), "0.2.0", false)
	if s.State != "rolled_back" || f.installed != "0.2.0~beta.1" {
		t.Fatalf("%+v installed=%s", s, f.installed)
	}
	if f.betaUpdated {
		t.Error("full suite used although the cache had the version")
	}
	if !strings.Contains(log.String(), "package=goblin-gate") || !strings.Contains(log.String(), "was not found") {
		t.Errorf("core download failure not logged:\n%s", log)
	}
}

func TestRollbackCopyFromBetaSuite(t *testing.T) {
	f := &fakeBox{installed: "0.2.0~beta.1", repo: map[string]bool{"0.2.0": true}, broken: map[string]bool{"0.2.0": true},
		betaRepo:   map[string]bool{"0.2.0~beta.1": true, "0.1.0~beta.2": true},
		plugins:    map[string]string{"goblin-gate-plugin-sentry": "0.1.0~beta.2"},
		pluginRepo: map[string]string{"goblin-gate-plugin-sentry": "0.2.0"}}
	m, _ := stableBox(t, f)
	s := m.Run(context.Background(), "0.2.0", false)
	if s.State != "rolled_back" || f.installed != "0.2.0~beta.1" || f.plugins["goblin-gate-plugin-sentry"] != "0.1.0~beta.2" {
		t.Fatalf("%+v installed=%s plugins=%v", s, f.installed, f.plugins)
	}
	for _, c := range f.calls {
		if strings.Contains(c, "SourceList=") && !strings.Contains(c, "Dir::State::Lists="+filepath.Join(m.Dir, "full-suite")) {
			t.Errorf("private apt without its own lists: %s", c)
		}
	}
	if _, err := os.Stat(filepath.Join(m.Dir, "full-suite")); !os.IsNotExist(err) {
		t.Error("private APT state left behind")
	}
}

func TestRollbackCopyMissingEverywhere(t *testing.T) {
	f := &fakeBox{installed: "0.2.0~beta.1", repo: map[string]bool{"0.2.0": true}, betaRepo: map[string]bool{}}
	m, log := stableBox(t, f)
	s := m.Run(context.Background(), "0.2.0", false)
	want := "0.2.0~beta.1 is not in the stable channel; nothing was changed. Switch to beta under System → Updates or update from the console"
	if s.State != "failed" || s.Message != want || f.installed != "0.2.0~beta.1" {
		t.Fatalf("%+v", s)
	}
	if n := strings.Count(log.String(), "package=goblin-gate "); n != 3 {
		t.Errorf("want 3 logged attempts for goblin-gate, got %d:\n%s", n, log)
	}
}
