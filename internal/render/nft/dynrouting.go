package nft

import (
	"fmt"
	"slices"
	"strings"
)

// dynamicRouting opens BGP (TCP 179) to the configured neighbours only, and
// OSPF (IP protocol 89) on the interfaces OSPF runs on, passive ones
// excepted: they announce their network without talking OSPF.
func (r *ruleset) dynamicRouting() {
	rt := r.cfg.Routing
	if rt.BGP.Enabled {
		var v4, v6 []string
		for _, n := range rt.BGP.Neighbors {
			if !n.Enabled {
				continue
			}
			if n.Address.Is4() {
				v4 = append(v4, n.Address.String())
			} else {
				v6 = append(v6, n.Address.String())
			}
		}
		for _, fam := range []struct {
			ip    string
			addrs []string
		}{{"ip", v4}, {"ip6", v6}} {
			if len(fam.addrs) == 0 {
				continue
			}
			slices.Sort(fam.addrs)
			set := fam.addrs[0]
			if len(fam.addrs) > 1 {
				set = "{ " + strings.Join(fam.addrs, ", ") + " }"
			}
			r.input = append(r.input, fmt.Sprintf("%s saddr %s tcp dport 179 accept comment %s", fam.ip, set, quote("bgp neighbours")))
		}
	}
	if rt.OSPF.Enabled {
		var devs []string
		for _, a := range rt.OSPF.Areas {
			for _, in := range a.Interfaces {
				if d, ok := r.devs[in.Name]; ok && !in.Passive {
					devs = append(devs, d)
				}
			}
		}
		if len(devs) > 0 {
			slices.Sort(devs)
			r.input = append(r.input, fmt.Sprintf("iifname %s meta l4proto 89 accept comment %s", devSet(slices.Compact(devs)), quote("ospf")))
		}
	}
}
