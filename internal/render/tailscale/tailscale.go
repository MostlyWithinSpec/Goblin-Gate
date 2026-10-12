// Package tailscale renders the desired Tailscale state (task 041) for the
// applier, which hands it to `goblind tailscale-apply`.
package tailscale

import (
	"encoding/json"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	ts "github.com/networkgoblinlabs/goblin-gate/internal/tailscale"
)

// Renderer implements commit.Renderer.
type Renderer struct{}

// Name returns "tailscale".
func (Renderer) Name() string { return "tailscale" }

// Render always writes ts.PrefsPath (also when disabled, so turning
// Tailscale off is applied too). Advertised routes are the subnets of the
// named interfaces.
func (Renderer) Render(cfg *config.Config) (map[string][]byte, error) {
	t := cfg.VPN.Tailscale
	d := ts.Desired{
		Enabled: t.Enabled, Hostname: t.Hostname, ExitNode: t.ExitNode,
		AcceptRoutes: t.AcceptRoutes, AcceptDNS: t.AcceptDNS, LoginServer: t.LoginServer,
	}
	if d.Hostname == "" {
		d.Hostname = cfg.System.Hostname
	}
	for _, name := range t.AdvertiseRoutes {
		for _, i := range cfg.Interfaces {
			if i.Name == name && i.Address != nil {
				d.AdvertiseRoutes = append(d.AdvertiseRoutes, i.Address.Masked().String())
			}
		}
		for _, v := range cfg.VLANs {
			if v.Name == name && v.Address != nil {
				d.AdvertiseRoutes = append(d.AdvertiseRoutes, v.Address.Masked().String())
			}
		}
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, err
	}
	return map[string][]byte{ts.PrefsPath: append(b, '\n')}, nil
}
