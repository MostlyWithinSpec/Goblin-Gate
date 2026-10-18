//go:build linux

package devices

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// defaultRunner runs a command and returns its combined output. The gate is
// a Linux product, so the neighbour table comes from iproute2's "ip".
func defaultRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("devices: %s %s: %w", name, strings.Join(args, " "), err)
	}
	return out, nil
}
