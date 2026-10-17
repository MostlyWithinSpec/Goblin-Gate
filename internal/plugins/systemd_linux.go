//go:build linux

package plugins

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Systemctl drives plugin units with systemctl.
type Systemctl struct{}

func run(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// Restart (re)starts a unit, clearing an earlier failure.
func (Systemctl) Restart(ctx context.Context, unit string) error {
	_, _ = run(ctx, "reset-failed", unit)
	_, err := run(ctx, "restart", unit)
	return err
}

// Stop stops a unit.
func (Systemctl) Stop(ctx context.Context, unit string) error {
	_, err := run(ctx, "stop", unit)
	return err
}

// State returns the unit's ActiveState.
func (Systemctl) State(ctx context.Context, unit string) string {
	out, _ := exec.CommandContext(ctx, "systemctl", "show", "-P", "ActiveState", unit).Output()
	return strings.TrimSpace(string(out))
}
