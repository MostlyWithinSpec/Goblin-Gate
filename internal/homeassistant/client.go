package homeassistant

import (
	"context"

	"github.com/networkgoblinlabs/goblin-gate/pkg/pluginsdk"
)

// The goblind answers the bridge reads, from the plugin SDK.
type (
	WAN      = pluginsdk.WAN
	Gateway  = pluginsdk.Gateway
	System   = pluginsdk.System
	Device   = pluginsdk.Device
	Policy   = pluginsdk.Policy
	APIError = pluginsdk.APIError
)

// Denied reports whether err is goblind refusing a capability the admin did
// not approve.
func Denied(err error) bool { return pluginsdk.Denied(err) }

// Client is the bridge's API on top of the plugin SDK's client.
type Client struct {
	*pluginsdk.Client
}

// Gateways returns the monitored gateways (the bridge needs no more of the
// report).
func (c Client) Gateways(ctx context.Context) ([]Gateway, error) {
	r, err := c.Client.Gateways(ctx)
	return r.Gateways, err
}
