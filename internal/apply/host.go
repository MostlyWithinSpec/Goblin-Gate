package apply

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultStateDir holds the manifest of managed files, the backup of the
// previous generation (prev/) and temporary files for config checks.
const DefaultStateDir = "/var/lib/goblingate/apply"

// DefaultLinkWait is Host.LinkWait when unset.
const DefaultLinkWait = 15 * time.Second

// Runner runs an external command and returns its combined output.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// Host applies rendered files to a filesystem rooted at Root and activates
// them with external commands (nft, networkctl, systemctl, ...).
//
// Apply is transactional per call:
//  1. every path must belong to a managed directory (see groups);
//  2. changed files are syntax-checked (nft -c, kea-dhcp4 -t,
//     unbound-checkconf) before anything is written;
//  3. the previous content of every changed file is copied to StateDir/prev/;
//  4. files are written atomically (temp file + rename) and files written by
//     the previous Apply but no longer rendered are removed;
//  5. only groups whose files changed are activated, in a fixed order
//     (sysctl, networkd, nft, kea, unbound); a group may first run prepare
//     commands (networkd deletes VLAN links whose id changed or whose netdev
//     is gone) and wait for the devices it needs (kea waits for VLANs).
//
// The group table in targets.go is the only list of writable directories and
// the commands run for them.
// If step 4 or 5 fails, the previous files are restored and the affected
// groups are activated again; the returned error describes both.
// The first Apply after start activates every group even when the files on
// disk are unchanged, because kernel state (the nft ruleset) is lost on reboot.
type Host struct {
	Root     string // filesystem root: "/" in production, a temp dir in tests
	StateDir string // absolute path below Root; DefaultStateDir when empty
	Runner   Runner
	Logger   *slog.Logger // nil = discard
	// LinkWait bounds how long activation waits for network devices a
	// service needs (VLANs networkd is still creating); 0 = DefaultLinkWait.
	LinkWait time.Duration

	mu        sync.Mutex
	activated bool // a full activation has succeeded in this process
}

// change is one file to write or remove.
type change struct {
	path    string
	group   int
	old     []byte
	existed bool
	data    []byte // new content; nil when remove
	remove  bool
}

// Apply writes files (keys are absolute Linux paths) and activates them.
func (h *Host) Apply(ctx context.Context, files map[string][]byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.apply(ctx, files); err != nil {
		return fmt.Errorf("apply: %w", err)
	}
	return nil
}

func (h *Host) apply(ctx context.Context, files map[string][]byte) error {
	paths := make([]string, 0, len(files))
	for p := range files {
		if _, err := groupFor(p); err != nil {
			return err
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)

	managed, err := h.loadManifest()
	if err != nil {
		return err
	}

	var changes []change
	changedGroups := map[int]bool{}
	for _, p := range paths {
		old, existed, err := h.read(p)
		if err != nil {
			return err
		}
		if existed && bytes.Equal(old, files[p]) {
			continue
		}
		g, _ := groupFor(p)
		changes = append(changes, change{path: p, group: g, old: old, existed: existed, data: files[p]})
		changedGroups[g] = true
	}
	for _, p := range managed {
		if _, keep := files[p]; keep {
			continue
		}
		g, err := groupFor(p)
		if err != nil {
			h.log().Warn("apply: ignoring unmanaged path in manifest", "path", p)
			continue
		}
		old, existed, err := h.read(p)
		if err != nil {
			return err
		}
		if existed {
			changes = append(changes, change{path: p, group: g, old: old, existed: true, remove: true})
			changedGroups[g] = true
		}
	}

	// Check, write and restore in group order, then by path.
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].group < changes[j].group })

	activate := changedGroups
	if !h.activated {
		activate = map[int]bool{}
		for _, p := range paths {
			g, _ := groupFor(p)
			activate[g] = true
		}
		for g := range changedGroups {
			activate[g] = true
		}
	}
	if len(changes) == 0 && len(activate) == 0 {
		return h.saveManifest(paths)
	}

	if err := h.check(ctx, changes); err != nil {
		return err
	}
	if len(changes) > 0 {
		if err := h.backup(changes); err != nil {
			return err
		}
	}
	h.log().Info("apply: writing files", "changed", len(changes), "groups", groupNames(activate))

	if err := h.writeAndActivate(ctx, changes, paths, activate); err != nil {
		rbErr := h.rollback(ctx, changes, managed, changedGroups)
		if rbErr != nil {
			return errors.Join(err, fmt.Errorf("rollback also failed: %w", rbErr))
		}
		return fmt.Errorf("%w (previous files restored)", err)
	}
	h.activated = true
	return h.saveManifest(paths)
}

// check runs each changed group's syntax check on a temporary copy.
func (h *Host) check(ctx context.Context, changes []change) error {
	// stage the new files other files' checks read (group.checkStage)
	staged := map[string]string{} // real path -> native path of the staged copy
	defer func() {
		for _, tmp := range staged {
			_ = os.Remove(tmp)
		}
	}()
	for _, c := range changes {
		g := groups[c.group]
		if c.remove || g.checkStage == nil || !g.checkStage(c.path) {
			continue
		}
		tmp := path.Join(h.stateDir(), "check", "staged-"+g.name+"-"+path.Base(c.path))
		if err := h.writeAtomic(tmp, c.data); err != nil {
			return err
		}
		staged[c.path] = h.native(tmp)
	}
	for _, c := range changes {
		chk := groups[c.group].check
		if c.remove || chk == nil {
			continue
		}
		data := c.data
		for real, tmp := range staged {
			if groups[c.group].checkStage != nil {
				data = bytes.ReplaceAll(data, []byte(real), []byte(filepath.ToSlash(tmp)))
			}
		}
		if cc := groups[c.group].checkCopy; cc != nil {
			var err error
			if data, err = cc(c.data); err != nil {
				return fmt.Errorf("check %s: %w", c.path, err)
			}
		}
		tmp := path.Join(h.stateDir(), "check", groups[c.group].name+"-"+path.Base(c.path))
		cmd := chk(h.native(tmp))
		if cmd == nil {
			continue // this file of the group needs no check
		}
		if err := h.writeAtomic(tmp, data); err != nil {
			return err
		}
		err := h.run(ctx, cmd)
		_ = os.Remove(h.native(tmp))
		if err != nil {
			return fmt.Errorf("check %s: %w", c.path, err)
		}
	}
	return nil
}

// backup replaces StateDir/prev/ with the previous content of changed files.
func (h *Host) backup(changes []change) error {
	prev := path.Join(h.stateDir(), "prev")
	if err := os.RemoveAll(h.native(prev)); err != nil {
		return fmt.Errorf("clear backup: %w", err)
	}
	for _, c := range changes {
		if !c.existed {
			continue
		}
		if err := h.writeAtomic(path.Join(prev, c.path), c.old); err != nil {
			return fmt.Errorf("backup %s: %w", c.path, err)
		}
	}
	return nil
}

func (h *Host) writeAndActivate(ctx context.Context, changes []change, paths []string, activate map[int]bool) error {
	for _, c := range changes {
		if c.remove {
			if err := os.Remove(h.native(c.path)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("remove %s: %w", c.path, err)
			}
			continue
		}
		if err := h.writeAtomic(c.path, c.data); err != nil {
			return err
		}
	}
	return h.activate(ctx, activate, paths, forward(changes))
}

// transition is one file's content change as seen by group.prepare.
type transition struct {
	path     string
	old, new []byte
}

func forward(changes []change) []transition {
	var ts []transition
	for _, c := range changes {
		t := transition{path: c.path}
		if c.existed {
			t.old = c.old
		}
		if !c.remove {
			t.new = c.data
		}
		ts = append(ts, t)
	}
	return ts
}

func backward(changes []change) []transition {
	ts := forward(changes)
	for i := range ts {
		ts[i].old, ts[i].new = ts[i].new, ts[i].old
	}
	return ts
}

// rollback restores the previous content of every change and re-activates
// the changed groups with the files that exist afterwards.
func (h *Host) rollback(ctx context.Context, changes []change, managed []string, changed map[int]bool) error {
	h.log().Warn("apply: rolling back to previous files", "files", len(changes))
	var errs []error
	for _, c := range changes {
		var err error
		if c.existed {
			err = h.writeAtomic(c.path, c.old)
		} else if err = os.Remove(h.native(c.path)); errors.Is(err, fs.ErrNotExist) {
			err = nil
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("restore %s: %w", c.path, err))
		}
	}
	// The previous generation = the old manifest plus files that existed before.
	var prev []string
	seen := map[string]bool{}
	add := func(p string) {
		if seen[p] {
			return
		}
		seen[p] = true
		if _, ok, _ := h.read(p); ok {
			prev = append(prev, p)
		}
	}
	for _, p := range managed {
		add(p)
	}
	for _, c := range changes {
		if c.existed {
			add(c.path)
		}
	}
	sort.Strings(prev)
	if err := h.activate(ctx, changed, prev, backward(changes)); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// activate runs the activation commands of the selected groups in order,
// each preceded by its prepare commands for the files in ts.
// Each group decides what to run when it has no files left.
func (h *Host) activate(ctx context.Context, sel map[int]bool, paths []string, ts []transition) error {
	for i, g := range groups {
		if !sel[i] {
			continue
		}
		if g.prepare != nil {
			for _, t := range ts {
				if gi, err := groupFor(t.path); err != nil || gi != i {
					continue
				}
				if cmd := g.prepare(t.path, t.old, t.new); cmd != nil {
					if err := h.run(ctx, cmd); err != nil {
						h.log().Warn("apply: prepare failed", "group", g.name, "error", err)
					}
				}
			}
		}
		if g.activate == nil {
			continue
		}
		var files []string
		for _, p := range paths {
			if gi, err := groupFor(p); err == nil && gi == i {
				files = append(files, p)
			}
		}
		if g.links != nil {
			if err := h.waitLinks(ctx, g, files); err != nil {
				return fmt.Errorf("activate %s: %w", g.name, err)
			}
		}
		for _, cmd := range g.activate(files) {
			if err := h.run(ctx, cmd); err != nil {
				return fmt.Errorf("activate %s: %w", g.name, err)
			}
		}
	}
	return nil
}

// waitLinks waits until every network device named by files exists, for at
// most LinkWait.
func (h *Host) waitLinks(ctx context.Context, g group, files []string) error {
	var devs []string
	for _, p := range files {
		if b, ok, err := h.read(p); err == nil && ok {
			devs = append(devs, g.links(b)...)
		}
	}
	wait := h.LinkWait
	if wait == 0 {
		wait = DefaultLinkWait
	}
	deadline := time.Now().Add(wait)
	for {
		var missing []string
		for _, d := range devs {
			if _, err := os.Stat(h.native(path.Join("/sys/class/net", d))); err != nil {
				missing = append(missing, d)
			}
		}
		if len(missing) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("network devices %s did not appear within %s", strings.Join(missing, ", "), wait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (h *Host) run(ctx context.Context, cmd []string) error {
	h.log().Debug("apply: run", "cmd", strings.Join(cmd, " "))
	out, err := h.Runner.Run(ctx, cmd[0], cmd[1:]...)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 2000 {
			msg = "..." + msg[len(msg)-2000:] // tools print the error last
		}
		if msg != "" {
			return fmt.Errorf("%s: %w: %s", strings.Join(cmd, " "), err, msg)
		}
		return fmt.Errorf("%s: %w", strings.Join(cmd, " "), err)
	}
	return nil
}

func groupNames(sel map[int]bool) []string {
	var out []string
	for i, g := range groups {
		if sel[i] {
			out = append(out, g.name)
		}
	}
	return out
}

func (h *Host) log() *slog.Logger {
	if h.Logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return h.Logger
}

func (h *Host) stateDir() string {
	if h.StateDir == "" {
		return DefaultStateDir
	}
	return h.StateDir
}

// native maps an absolute Linux path to a host path below Root.
func (h *Host) native(p string) string {
	return filepath.Join(h.Root, filepath.FromSlash(p))
}
