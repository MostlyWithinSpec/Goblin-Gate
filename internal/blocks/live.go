package blocks

import (
	"context"
	"encoding/json"
	"net"
	"net/netip"
	"os/exec"
	"time"
)

// LiveAddrs returns the box's current addresses (including ones learned by
// DHCP) and the gateways of its default routes in every routing table
// (multi-WAN keeps one table per gateway). Errors leave parts out; the
// config-based checks in Protected still apply.
func LiveAddrs() []netip.Addr {
	var out []netip.Addr
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if p, err := netip.ParsePrefix(a.String()); err == nil {
				out = append(out, p.Addr())
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, fam := range []string{"-4", "-6"} {
		data, err := exec.CommandContext(ctx, "ip", "-j", fam, "route", "show", "table", "all", "default").Output()
		if err != nil {
			continue
		}
		var routes []struct {
			Gateway string `json:"gateway"`
		}
		if json.Unmarshal(data, &routes) != nil {
			continue
		}
		for _, r := range routes {
			if a, err := netip.ParseAddr(r.Gateway); err == nil {
				out = append(out, a)
			}
		}
	}
	return out
}
