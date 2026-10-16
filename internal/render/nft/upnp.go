package nft

import (
	"fmt"

	"github.com/networkgoblinlabs/goblin-gate/internal/render/upnp"
)

// Names miniupnpd writes its mappings into (task 059, internal/render/upnp).
const (
	UPnPForwardChain = upnp.ForwardChain // in table inet goblin
	UPnPNATTable     = upnp.NATTable     // table inet goblin_upnp
	UPnPDNATChain    = upnp.DNATChain
	UPnPSNATChain    = upnp.SNATChain
)

// upnpInput opens SSDP discovery, the UPnP control port and NAT-PMP/PCP on
// the interfaces UPnP serves.
func (r *ruleset) upnpInput() {
	u := r.cfg.UPnP
	if !u.Enabled {
		return
	}
	for _, name := range u.Interfaces {
		dev, ok := r.devs[name]
		if !ok {
			continue
		}
		in := "iifname " + quote(dev)
		r.input = append(r.input,
			fmt.Sprintf("%s udp dport %d accept comment %s", in, upnp.SSDPPort, quote(name+": upnp discovery")),
			fmt.Sprintf("%s tcp dport %d accept comment %s", in, upnp.HTTPPort, quote(name+": upnp control")))
		if u.NATPMPOn() {
			r.input = append(r.input, fmt.Sprintf("%s udp dport %d accept comment %s", in, upnp.NATPMPPort, quote(name+": nat-pmp/pcp")))
		}
	}
}
