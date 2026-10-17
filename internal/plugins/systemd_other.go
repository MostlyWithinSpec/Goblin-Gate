//go:build !linux

package plugins

import "context"

// Systemctl does nothing off Linux (dev builds).
type Systemctl struct{}

// Restart does nothing.
func (Systemctl) Restart(context.Context, string) error { return nil }

// Stop does nothing.
func (Systemctl) Stop(context.Context, string) error { return nil }

// State reports every unit inactive.
func (Systemctl) State(context.Context, string) string { return "inactive" }
