//go:build linux

package apply

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"strconv"
)

// ExecRunner runs commands on the real system.
type ExecRunner struct{}

// Run executes name with args and returns combined stdout and stderr.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	return cmd.CombinedOutput()
}

// NewLinux returns the applier for the real system: files under /, state in
// DefaultStateDir, commands via exec. Needs root (CAP_NET_ADMIN + file access).
func NewLinux(logger *slog.Logger) *Host {
	return &Host{Root: "/", StateDir: DefaultStateDir, Runner: ExecRunner{}, Logger: logger}
}

// NewSystem returns the applier for this OS: NewLinux on Linux.
func NewSystem(logger *slog.Logger) (Applier, error) {
	return NewLinux(logger), nil
}

// groupID finds a system group's id.
func groupID(name string) (int, bool) {
	g, err := user.LookupGroup(name)
	if err != nil {
		return 0, false
	}
	id, err := strconv.Atoi(g.Gid)
	return id, err == nil
}

// syncDir fsyncs a directory so a rename survives a crash.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}
