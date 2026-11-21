package nft

import (
	"fmt"
	"slices"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// radius opens RADIUS (authentication and accounting) to the configured
// switches and access points only, from any interface but the WAN.
func (r *ruleset) radius() {
	if !r.cfg.RADIUSActive() {
		return
	}
	var v4, v6 []string
	for _, c := range r.cfg.RADIUS.Clients {
		p, ok := c.Prefix()
		if !ok {
			continue
		}
		if p.Addr().Is4() {
			v4 = append(v4, p.String())
		} else {
			v6 = append(v6, p.String())
		}
	}
	slices.Sort(v4)
	v4 = slices.Compact(v4)
	slices.Sort(v6)
	v6 = slices.Compact(v6)
	wan := ""
	if w := r.wanDevs(); len(w) > 0 {
		wan = "iifname != " + devSet(w) + " "
	}
	ports := fmt.Sprintf("udp dport { %d, %d }", config.RADIUSAuthPort, config.RADIUSAcctPort)
	if len(v4) > 0 {
		r.input = append(r.input, fmt.Sprintf("%sip saddr { %s } %s accept comment %s", wan, strings.Join(v4, ", "), ports, quote("RADIUS clients")))
	}
	if len(v6) > 0 {
		r.input = append(r.input, fmt.Sprintf("%sip6 saddr { %s } %s accept comment %s", wan, strings.Join(v6, ", "), ports, quote("RADIUS clients")))
	}
}
