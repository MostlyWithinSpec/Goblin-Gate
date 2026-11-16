// Command homelab is the homelab plugin (internal/homelab): it keeps live
// address objects filled with the addresses of Proxmox VE guests and Docker
// containers, chosen by tag, label or name.
package main

import (
	"context"

	"github.com/networkgoblinlabs/goblin-gate/internal/homelab"
	"github.com/networkgoblinlabs/goblin-gate/pkg/pluginsdk"
)

func main() {
	pluginsdk.Run(run)
}

func run(ctx context.Context, p *pluginsdk.Plugin) error {
	settings, err := homelab.SettingsFrom(p.Self.Settings)
	if err != nil {
		// a setup problem: wait for the admin to fix the settings (that
		// restarts the plugin) instead of failing over and over
		p.Logger.Error("homelab: not started", "error", err)
		<-ctx.Done()
		return nil
	}
	if !p.Self.Has(pluginsdk.CapObjectsWrite) {
		p.Logger.Error("homelab: not started: objects:write is not approved, there is nothing it may fill")
		<-ctx.Done()
		return nil
	}
	var sources []homelab.Source
	if settings.Proxmox != nil {
		sources = append(sources, homelab.NewProxmox(*settings.Proxmox))
	}
	for _, d := range settings.Docker {
		sources = append(sources, homelab.NewDocker(d))
	}
	names := make([]string, len(sources))
	for i, s := range sources {
		names[i] = s.Name()
	}
	leases := p.Self.Has(pluginsdk.CapDevicesRead)
	p.Logger.Info("homelab: started", "sources", names, "interval", settings.Interval.String(), "dhcp_leases", leases)
	s := &homelab.Syncer{API: p.Client, Sources: sources, Settings: settings, Leases: leases, Logger: p.Logger}
	return s.Run(ctx)
}
