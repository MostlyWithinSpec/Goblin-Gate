// Command unifi is the UniFi plugin (internal/unifi): it fills live address
// objects with the clients of UniFi networks and SSIDs, and shows in the
// device inventory which switch port or access point each device is on.
package main

import (
	"context"

	"github.com/networkgoblinlabs/goblin-gate/internal/unifi"
	"github.com/networkgoblinlabs/goblin-gate/pkg/pluginsdk"
)

func main() {
	pluginsdk.Run(run)
}

func run(ctx context.Context, p *pluginsdk.Plugin) error {
	settings, err := unifi.SettingsFrom(p.Self.Settings)
	if err != nil {
		// a setup problem: wait for the admin to fix the settings (that
		// restarts the plugin) instead of failing over and over
		p.Logger.Error("unifi: not started", "error", err)
		<-ctx.Done()
		return nil
	}
	objects, annotate := p.Self.Has(pluginsdk.CapObjectsWrite), p.Self.Has(pluginsdk.CapDevicesAnnotate)
	if !objects && !annotate {
		p.Logger.Error("unifi: not started: approve objects:write, devices:annotate or both, there is nothing it may do")
		<-ctx.Done()
		return nil
	}
	p.Logger.Info("unifi: started", "console", settings.URL, "site", settings.Site, "interval", settings.Interval.String(),
		"objects", objects, "device_details", annotate)
	s := &unifi.Syncer{API: p.Client, Source: &unifi.Client{Settings: settings}, Settings: settings,
		Objects: objects, Annotate: annotate, Logger: p.Logger}
	return s.Run(ctx)
}
