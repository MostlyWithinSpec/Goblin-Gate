//go:build !linux

package devices

import (
	"context"
	"fmt"
)

// defaultRunner has no neighbour table off Linux (dev machines, tests); the
// store works from DHCP leases alone. A Runner override always wins.
func defaultRunner(_ context.Context, name string, args ...string) ([]byte, error) {
	return nil, fmt.Errorf("devices: %s is only available on Linux", name)
}
