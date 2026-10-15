package updates

import (
	"context"
	"errors"
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
}

func (f *fakeBox) exec(_ context.Context, env []string, name string, args ...string) ([]byte, error) {
	line := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	if name == "apt-get" {
		if len(args) < 2 || args[0] != "-o" || args[1] != "DPkg::Lock::Timeout=300" {
			return nil, errors.New("apt-get without the lock timeout: " + line)
		}
		args = args[2:]
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
		f.installed = strings.TrimPrefix(args[len(args)-1], Package+"=")
		return nil, nil
	case name == "dpkg" && args[0] == "-i":
		b, _ := os.ReadFile(args[len(args)-1])
		f.installed = string(b)
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

func (f *fakeBox) health(context.Context) (string, error) {
	if f.broken[f.installed] {
		return "", errors.New("connection refused")
	}
	return f.installed, nil
}

func manager(t *testing.T, f *fakeBox) *Manager {
	return &Manager{Dir: t.TempDir(), Exec: f.exec, Health: f.health, Timeout: 50 * time.Millisecond, Poll: 5 * time.Millisecond}
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
