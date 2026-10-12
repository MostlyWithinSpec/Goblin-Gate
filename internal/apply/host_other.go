//go:build !linux

package apply

import (
	"errors"
	"log/slog"
)

// NewSystem fails on non-Linux systems: the data plane only exists on Linux.
// Use NewFake (goblind --dev) instead.
func NewSystem(*slog.Logger) (Applier, error) {
	return nil, errors.New("the system applier only runs on Linux; use --dev")
}

// syncDir is a no-op where directories cannot be fsynced (Windows).
func syncDir(string) {}
