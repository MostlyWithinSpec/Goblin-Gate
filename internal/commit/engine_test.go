package commit

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/apply"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

type fakeRenderer struct {
	name string
	fail error
}

func (r *fakeRenderer) Name() string { return r.name }

func (r *fakeRenderer) Render(cfg *config.Config) (map[string][]byte, error) {
	if r.fail != nil {
		return nil, r.fail
	}
	return map[string][]byte{"/etc/" + r.name + ".conf": []byte("hostname " + cfg.System.Hostname)}, nil
}

// fakeClock fires timers only when the test says so.
type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	fires []chan time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }

func (c *fakeClock) After(time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.fires = append(c.fires, ch)
	return ch
}

func (c *fakeClock) fireAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ch := range c.fires {
		ch <- c.now
	}
	c.fires = nil
}

type env struct {
	store   *config.Store
	applier *apply.Fake
	render  *fakeRenderer
	engine  *Engine
	clock   *fakeClock
}

func newEnv(t *testing.T) *env {
	t.Helper()
	store, err := config.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &env{store: store, applier: apply.NewFake(), render: &fakeRenderer{name: "test"}, clock: &fakeClock{now: time.Unix(1_800_000_000, 0)}}
	e.engine = New(store, []Renderer{e.render}, e.applier)
	e.engine.clock = e.clock
	return e
}

func (e *env) setHostname(t *testing.T, name string) {
	t.Helper()
	cfg, err := e.store.Candidate()
	if err != nil {
		t.Fatal(err)
	}
	cfg.System.Hostname = name
	if err := e.store.SetCandidate(cfg); err != nil {
		t.Fatal(err)
	}
}

func (e *env) runningHostname(t *testing.T) string {
	t.Helper()
	cfg, err := e.store.Running()
	if err != nil {
		t.Fatal(err)
	}
	return cfg.System.Hostname
}

func (e *env) appliedHostname() string {
	return string(e.applier.Files()["/etc/test.conf"])
}

func TestCommitSuccess(t *testing.T) {
	e := newEnv(t)
	e.setHostname(t, "gate1")
	rev, err := e.engine.Commit(context.Background(), "alice", "first")
	if err != nil {
		t.Fatal(err)
	}
	if rev != 1 {
		t.Fatalf("rev = %d, want 1", rev)
	}
	if got := e.runningHostname(t); got != "gate1" {
		t.Fatalf("running hostname = %q", got)
	}
	if got := e.appliedHostname(); got != "hostname gate1" {
		t.Fatalf("applied = %q", got)
	}
	if diff := config.Diff(mustRunning(t, e), mustCandidate(t, e)); len(diff) != 0 {
		t.Fatalf("candidate should be cleared after commit, diff = %v", diff)
	}
	revs, _ := e.store.ListRevisions()
	if len(revs) != 1 || revs[0].User != "alice" || revs[0].Comment != "first" {
		t.Fatalf("revisions = %+v", revs)
	}
}

func TestCommitValidationFailure(t *testing.T) {
	e := newEnv(t)
	e.setHostname(t, "")
	_, err := e.engine.Commit(context.Background(), "alice", "")
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Errors) == 0 {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	if e.applier.Calls() != 0 {
		t.Fatal("nothing should be applied when validation fails")
	}
	if revs, _ := e.store.ListRevisions(); len(revs) != 0 {
		t.Fatalf("no revision expected, got %+v", revs)
	}
}

func TestCommitRenderFailure(t *testing.T) {
	e := newEnv(t)
	e.render.fail = errors.New("boom")
	e.setHostname(t, "gate1")
	if _, err := e.engine.Commit(context.Background(), "alice", ""); err == nil {
		t.Fatal("expected error")
	}
	if e.applier.Calls() != 0 {
		t.Fatal("nothing should be applied when render fails")
	}
	if got := e.runningHostname(t); got == "gate1" {
		t.Fatal("running config must not change")
	}
}

func TestCommitApplyFailureRestoresPrevious(t *testing.T) {
	e := newEnv(t)
	e.setHostname(t, "good")
	if _, err := e.engine.Commit(context.Background(), "alice", ""); err != nil {
		t.Fatal(err)
	}
	e.setHostname(t, "bad")
	e.applier.FailNext(1, errors.New("nft said no"))
	if _, err := e.engine.Commit(context.Background(), "alice", ""); err == nil {
		t.Fatal("expected apply error")
	}
	if got := e.runningHostname(t); got != "good" {
		t.Fatalf("running = %q, want good", got)
	}
	if got := e.appliedHostname(); got != "hostname good" {
		t.Fatalf("previous config should be re-applied, applied = %q", got)
	}
	if got := mustCandidate(t, e).System.Hostname; got != "bad" {
		t.Fatalf("candidate should be kept after a failed commit, got %q", got)
	}
}

func TestCommitApplyAndRestoreBothFail(t *testing.T) {
	e := newEnv(t)
	e.setHostname(t, "x")
	e.applier.SetFailApply(errors.New("down"))
	_, err := e.engine.Commit(context.Background(), "alice", "")
	if err == nil || !contains(err.Error(), "restoring previous config also failed") {
		t.Fatalf("expected both errors, got %v", err)
	}
}

func TestCommitConfirmConfirmed(t *testing.T) {
	e := newEnv(t)
	e.setHostname(t, "base")
	mustCommit(t, e)
	e.setHostname(t, "trial")
	rev, err := e.engine.CommitConfirm(context.Background(), "alice", "try", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	deadline, ok := e.engine.PendingConfirm()
	if !ok || !deadline.Equal(e.clock.now.Add(5*time.Minute)) {
		t.Fatalf("pending = %v %v", deadline, ok)
	}
	if err := e.engine.Confirm(); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.engine.PendingConfirm(); ok {
		t.Fatal("still pending after confirm")
	}
	e.clock.fireAll()
	time.Sleep(20 * time.Millisecond)
	if got := e.runningHostname(t); got != "trial" {
		t.Fatalf("running = %q, want trial (rev %d)", got, rev)
	}
	if err := e.engine.Confirm(); !errors.Is(err, ErrNoPendingConfirm) {
		t.Fatalf("second confirm: %v", err)
	}
}

func TestCommitConfirmTimeoutRollsBack(t *testing.T) {
	e := newEnv(t)
	e.setHostname(t, "base")
	mustCommit(t, e)
	e.setHostname(t, "trial")
	if _, err := e.engine.CommitConfirm(context.Background(), "alice", "try", time.Minute); err != nil {
		t.Fatal(err)
	}
	e.clock.fireAll()
	waitFor(t, func() bool { _, ok := e.engine.PendingConfirm(); return !ok && e.runningHostname(t) == "base" })
	if got := e.appliedHostname(); got != "hostname base" {
		t.Fatalf("applied = %q", got)
	}
	revs, _ := e.store.ListRevisions()
	if len(revs) != 3 || revs[2].User != "system" {
		t.Fatalf("revisions = %+v", revs)
	}
}

func TestCommitWhilePendingConfirms(t *testing.T) {
	e := newEnv(t)
	e.setHostname(t, "trial")
	if _, err := e.engine.CommitConfirm(context.Background(), "alice", "", time.Minute); err != nil {
		t.Fatal(err)
	}
	e.setHostname(t, "next")
	mustCommit(t, e)
	if _, ok := e.engine.PendingConfirm(); ok {
		t.Fatal("a new commit should confirm the pending one")
	}
	e.clock.fireAll()
	time.Sleep(20 * time.Millisecond)
	if got := e.runningHostname(t); got != "next" {
		t.Fatalf("running = %q", got)
	}
}

func TestRollback(t *testing.T) {
	e := newEnv(t)
	e.setHostname(t, "one")
	mustCommit(t, e)
	e.setHostname(t, "two")
	mustCommit(t, e)
	rev, err := e.engine.Rollback(context.Background(), 1, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if rev != 3 || e.runningHostname(t) != "one" || e.appliedHostname() != "hostname one" {
		t.Fatalf("rev=%d running=%q applied=%q", rev, e.runningHostname(t), e.appliedHostname())
	}
	if _, err := e.engine.Rollback(context.Background(), 99, "bob"); err == nil {
		t.Fatal("expected error for missing revision")
	}
}

func TestRendererConflict(t *testing.T) {
	e := newEnv(t)
	e.engine.Renderers = append(e.engine.Renderers, &fakeRenderer{name: "test"})
	e.setHostname(t, "x")
	if _, err := e.engine.Commit(context.Background(), "a", ""); err == nil {
		t.Fatal("expected duplicate path error")
	}
}

func mustCommit(t *testing.T, e *env) {
	t.Helper()
	if _, err := e.engine.Commit(context.Background(), "alice", ""); err != nil {
		t.Fatal(err)
	}
}

func mustRunning(t *testing.T, e *env) *config.Config {
	t.Helper()
	c, err := e.store.Running()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustCandidate(t *testing.T, e *env) *config.Config {
	t.Helper()
	c, err := e.store.Candidate()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}

func TestAfterApplyHook(t *testing.T) {
	e := newEnv(t)
	live := make(chan string, 4)
	e.engine.AfterApply = func(c *config.Config) { live <- c.System.Hostname }
	next := func() string {
		select {
		case h := <-live:
			return h
		case <-time.After(5 * time.Second):
			t.Fatal("AfterApply not called")
			return ""
		}
	}
	e.setHostname(t, "good")
	if _, err := e.engine.Commit(context.Background(), "alice", ""); err != nil {
		t.Fatal(err)
	}
	if h := next(); h != "good" {
		t.Fatalf("after commit: %q", h)
	}
	// a failed apply reports the restored config as live
	e.setHostname(t, "bad")
	e.applier.FailNext(1, errors.New("nft said no"))
	if _, err := e.engine.Commit(context.Background(), "alice", ""); err == nil {
		t.Fatal("expected apply error")
	}
	if h := next(); h != "good" {
		t.Fatalf("after failed apply: %q", h)
	}
}

func TestBeforeApplyHook(t *testing.T) {
	e := newEnv(t)
	var seen string
	e.engine.BeforeApply = func(c *config.Config) error { seen = c.System.Hostname; return nil }
	e.setHostname(t, "gate1")
	if _, err := e.engine.Commit(context.Background(), "alice", ""); err != nil || seen != "gate1" {
		t.Fatalf("%v %q", err, seen)
	}
	e.engine.BeforeApply = func(*config.Config) error { return errors.New("no keys") }
	e.setHostname(t, "gate2")
	if _, err := e.engine.Commit(context.Background(), "alice", ""); err == nil {
		t.Fatal("BeforeApply error ignored")
	}
	if got := e.runningHostname(t); got != "gate1" {
		t.Fatalf("running changed: %q", got)
	}
}
