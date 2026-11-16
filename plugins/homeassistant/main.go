// Command homeassistant is the Home Assistant plugin (internal/homeassistant):
// it publishes the box's state to an MQTT broker with Home Assistant's MQTT
// discovery and switches the policies the admin allowed.
package main

import (
	"context"

	"github.com/networkgoblinlabs/goblin-gate/internal/homeassistant"
	"github.com/networkgoblinlabs/goblin-gate/pkg/pluginsdk"
)

func main() {
	pluginsdk.Run(run)
}

func run(ctx context.Context, p *pluginsdk.Plugin) error {
	settings, err := homeassistant.SettingsFrom(p.Self.Settings)
	if err != nil {
		// a setup problem, not a crash: wait for the admin to fix the
		// settings (that restarts the plugin) instead of failing over and over
		p.Logger.Error("homeassistant: not started", "error", err)
		<-ctx.Done()
		return nil
	}
	for _, c := range []string{pluginsdk.CapStatsRead, pluginsdk.CapDevicesRead, pluginsdk.CapPoliciesToggle} {
		if !p.Self.Has(c) {
			p.Logger.Info("homeassistant: capability not approved, its entities are left out", "capability", c)
		}
	}
	b := &homeassistant.Bridge{
		API: homeassistant.Client{Client: p.Client}, Settings: settings, Capabilities: p.Self.Capabilities,
		Logger: p.Logger, Version: pluginsdk.InstalledVersion(p.Env.Name), StateDir: p.Env.StateDir,
	}
	p.Logger.Info("homeassistant: started", "broker", settings.Broker, "base_topic", settings.BaseTopic, "capabilities", p.Self.Capabilities)
	return b.Run(ctx)
}
