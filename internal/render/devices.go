package render

import (
	"fmt"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// VLANDevice returns the kernel device name for a VLAN sub-interface:
// "<parent device>.<vlan id>", e.g. "eth1.10". Every renderer that refers to
// a VLAN by device (netif, nft, kea, ...) must use this so they agree.
func VLANDevice(parentDevice string, vlanID int) string {
	return fmt.Sprintf("%s.%d", parentDevice, vlanID)
}

// Devices maps every logical interface and VLAN name in cfg to its kernel
// device name. VLANs whose parent does not exist are left out; validation
// (config.Validate) reports them.
func Devices(cfg *config.Config) map[string]string {
	m := make(map[string]string, len(cfg.Interfaces)+len(cfg.VLANs))
	for _, ifc := range cfg.Interfaces {
		m[ifc.Name] = ifc.Device
	}
	for _, v := range cfg.VLANs {
		for _, ifc := range cfg.Interfaces {
			if ifc.Name == v.Parent {
				m[v.Name] = VLANDevice(ifc.Device, v.VLANID)
				break
			}
		}
	}
	if cfg.VPN.Tailscale.Enabled {
		m[config.TailscaleInterface] = config.TailscaleDevice
	}
	for _, w := range cfg.VPN.WireGuard {
		if w.Enabled {
			m[w.Name] = w.Name
		}
	}
	for _, w := range cfg.VPN.WireGuardClients {
		if w.Enabled {
			m[w.Name] = w.Name
		}
	}
	return m
}
