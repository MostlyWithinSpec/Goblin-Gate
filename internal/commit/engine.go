// Package commit turns the candidate config into the running config:
// validate, render, apply, save a revision, with automatic rollback for
// "commit confirm".
package commit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/apply"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Renderer renders a config to a set of files keyed by absolute Linux path.
type Renderer interface {
	Name() string
	Render(cfg *config.Config) (map[string][]byte, error)
}

// ErrNoPendingConfirm is returned by Confirm when nothing is waiting.
var ErrNoPendingConfirm = errors.New("commit: no commit is waiting for confirmation")

// ValidationError is returned when the config to commit fails validation.
type ValidationError struct {
	Errors []config.ValidationError
}

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Errors))
	for i, ve := range e.Errors {
		parts[i] = ve.Error()
	}
	return "commit: config is invalid: " + strings.Join(parts, "; ")
}

// Engine runs commits one at a time.
type Engine struct {
	Store     *config.Store
	Renderers []Renderer
	Applier   apply.Applier
	Logger    *slog.Logger

	// AfterApply, if set, is called in its own goroutine after every apply
	// attempt with the config that is now live (the previous one when an
	// apply failed and was rolled back). Reloading the ruleset empties the
	// runtime-filled nft sets; the threat feed syncer refills them here.
	AfterApply func(live *config.Config)

	// BeforeApply, if set, runs before a config is rendered and applied;
	// an error aborts the commit. goblind creates WireGuard keys here
	// (networkd reads them when it loads the rendered files).
	BeforeApply func(cfg *config.Config) error

	clock clock
	mu    sync.Mutex // serializes commits, confirms and rollbacks

	pending *pendingConfirm
}

type pendingConfirm struct {
	revision int
	previous *config.Config
	deadline time.Time
	cancel   chan struct{}
}

// clock is injectable so tests never sleep.
type clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// New creates a commit engine.
func New(store *config.Store, renderers []Renderer, applier apply.Applier) *Engine {
	return &Engine{
		Store:     store,
		Renderers: renderers,
		Applier:   applier,
		Logger:    slog.Default(),
		clock:     realClock{},
	}
}

// Commit validates the candidate, renders and applies it, saves it as a new
// revision and clears the candidate. On any failure the running config is
// unchanged; if apply fails the previous running config is re-applied (best
// effort). A commit made while another commit waits for confirmation counts
// as confirming it.
func (e *Engine) Commit(ctx context.Context, user, comment string) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.clearPendingLocked()
	rev, _, err := e.commitCandidateLocked(ctx, user, comment)
	return rev, err
}

// CommitConfirm commits like Commit, then waits up to timeout for Confirm.
// Without it, the previous running config is restored automatically.
func (e *Engine) CommitConfirm(ctx context.Context, user, comment string, timeout time.Duration) (int, error) {
	if timeout <= 0 {
		return 0, fmt.Errorf("commit confirm: timeout must be positive")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.clearPendingLocked()
	rev, previous, err := e.commitCandidateLocked(ctx, user, comment)
	if err != nil {
		return 0, err
	}
	p := &pendingConfirm{
		revision: rev,
		previous: previous,
		deadline: e.clock.Now().Add(timeout),
		cancel:   make(chan struct{}),
	}
	e.pending = p
	timer := e.clock.After(timeout)
	go e.waitConfirm(p, timer)
	return rev, nil
}

func (e *Engine) waitConfirm(p *pendingConfirm, timer <-chan time.Time) {
	select {
	case <-p.cancel:
		return
	case <-timer:
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pending != p {
		return // confirmed or replaced while we waited for the lock
	}
	e.pending = nil
	comment := fmt.Sprintf("automatic rollback: revision %d was not confirmed", p.revision)
	rev, err := e.applyAndSaveLocked(context.Background(), p.previous, "system", comment)
	if err != nil {
		e.Logger.Error("commit confirm: automatic rollback failed", "revision", p.revision, "error", err)
		return
	}
	e.Logger.Warn("commit confirm: not confirmed, rolled back", "revision", p.revision, "new_revision", rev)
}

// Confirm keeps a pending commit-confirm.
func (e *Engine) Confirm() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pending == nil {
		return ErrNoPendingConfirm
	}
	e.clearPendingLocked()
	return nil
}

// PendingConfirm returns the rollback deadline of a pending commit-confirm.
func (e *Engine) PendingConfirm() (time.Time, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pending == nil {
		return time.Time{}, false
	}
	return e.pending.deadline, true
}

// Rollback re-applies the config stored at revision rev and saves it as a
// new revision. The candidate is left alone.
func (e *Engine) Rollback(ctx context.Context, rev int, user string) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cfg, err := e.Store.Revision(rev)
	if err != nil {
		return 0, fmt.Errorf("rollback: %w", err)
	}
	e.clearPendingLocked()
	return e.applyAndSaveLocked(ctx, cfg, user, fmt.Sprintf("rollback to revision %d", rev))
}

// ApplyRunning re-applies the current running config without changing it.
// Used on daemon start to make sure the data plane matches what is on disk,
// and when time-dependent output changes (schedules across a daylight
// saving switch). Returns true only if a running config existed and was
// applied. It waits for a commit in progress.
func (e *Engine) ApplyRunning(ctx context.Context) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cfg, err := e.Store.Running()
	if err != nil {
		return false, nil
	}
	if e.BeforeApply != nil {
		if err := e.BeforeApply(cfg); err != nil {
			return false, fmt.Errorf("apply running: prepare: %w", err)
		}
	}
	files, err := e.render(cfg)
	if err != nil {
		return false, fmt.Errorf("apply running: render: %w", err)
	}
	if err := e.Applier.Apply(ctx, files); err != nil {
		return false, fmt.Errorf("apply running: %w", err)
	}
	e.afterApply(cfg)
	e.Logger.Info("applied running config", "files", len(files))
	return true, nil
}

func (e *Engine) clearPendingLocked() {
	if e.pending != nil {
		close(e.pending.cancel)
		e.pending = nil
	}
}

// commitCandidateLocked commits the candidate and returns the new revision
// plus the config that was running before.
func (e *Engine) commitCandidateLocked(ctx context.Context, user, comment string) (int, *config.Config, error) {
	candidate, err := e.Store.Candidate()
	if err != nil {
		return 0, nil, fmt.Errorf("commit: read candidate: %w", err)
	}
	previous, err := e.Store.Running()
	if err != nil {
		return 0, nil, fmt.Errorf("commit: read running: %w", err)
	}
	rev, err := e.applyAndSaveLocked(ctx, candidate, user, comment)
	if err != nil {
		return 0, nil, err
	}
	if err := e.Store.DiscardCandidate(); err != nil {
		e.Logger.Warn("commit: could not clear candidate", "error", err)
	}
	return rev, previous, nil
}

// applyAndSaveLocked validates, renders and applies cfg, then saves it as a
// new revision (which also makes it the running config).
func (e *Engine) applyAndSaveLocked(ctx context.Context, cfg *config.Config, user, comment string) (int, error) {
	if errs := config.Validate(cfg); len(errs) > 0 {
		return 0, &ValidationError{Errors: errs}
	}
	if e.BeforeApply != nil {
		if err := e.BeforeApply(cfg); err != nil {
			return 0, fmt.Errorf("commit: prepare: %w", err)
		}
	}
	files, err := e.render(cfg)
	if err != nil {
		return 0, err
	}
	previous, err := e.Store.Running()
	if err != nil {
		return 0, fmt.Errorf("commit: read running: %w", err)
	}
	if err := e.Applier.Apply(ctx, files); err != nil {
		applyErr := fmt.Errorf("commit: apply: %w", err)
		if restoreErr := e.restore(ctx, previous); restoreErr != nil {
			return 0, errors.Join(applyErr, fmt.Errorf("commit: restoring previous config also failed: %w", restoreErr))
		}
		return 0, applyErr
	}
	e.afterApply(cfg)
	rev, err := e.Store.SaveRevision(cfg, user, comment)
	if err != nil {
		return 0, fmt.Errorf("commit: save revision: %w", err)
	}
	e.Logger.Info("commit applied", "revision", rev, "user", user, "files", len(files))
	return rev, nil
}

// restore re-applies previous after a failed apply. Either way previous is
// what is live afterwards (the applier rolls back its own files).
func (e *Engine) restore(ctx context.Context, previous *config.Config) error {
	defer e.afterApply(previous)
	files, err := e.render(previous)
	if err != nil {
		return err
	}
	return e.Applier.Apply(ctx, files)
}

func (e *Engine) afterApply(live *config.Config) {
	if e.AfterApply != nil && live != nil {
		go e.AfterApply(live)
	}
}

// render runs every renderer and merges their files. Two renderers writing
// the same path is a bug and is reported.
func (e *Engine) render(cfg *config.Config) (map[string][]byte, error) {
	files := make(map[string][]byte)
	owner := make(map[string]string)
	for _, r := range e.Renderers {
		out, err := r.Render(cfg)
		if err != nil {
			return nil, fmt.Errorf("commit: render %s: %w", r.Name(), err)
		}
		paths := make([]string, 0, len(out))
		for p := range out {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			if prev, dup := owner[p]; dup {
				return nil, fmt.Errorf("commit: renderers %s and %s both write %s", prev, r.Name(), p)
			}
			owner[p] = r.Name()
			files[p] = out[p]
		}
	}
	return files, nil
}
