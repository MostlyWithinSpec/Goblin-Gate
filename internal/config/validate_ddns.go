package config

import (
	"fmt"
	"strings"
)

// MaxDDNSEntries is the maximum number of DDNS entries.
const MaxDDNSEntries = 16

func (v *validator) checkDDNS() {
	c := v.cfg
	if len(c.DDNS) > MaxDDNSEntries {
		v.add("ddns", "at most %d entries", MaxDDNSEntries)
	}
	seen := map[string]bool{}
	for i, d := range c.DDNS {
		p := fmt.Sprintf("ddns[%d]", i)
		if !validName(d.Name) {
			v.add(p+".name", "invalid name %q: must be 1-63 characters of [A-Za-z0-9_.-]", d.Name)
		} else if seen[d.Name] {
			v.add(p+".name", "duplicate DDNS name %q", d.Name)
		}
		seen[d.Name] = true

		if !validHostname(d.Hostname) {
			v.add(p+".hostname", "invalid hostname %q: must be a valid DNS name", d.Hostname)
		}

		iface := d.Interface
		if iface == "" {
			iface = "wan1"
		}
		role, ok := v.netifRole(iface)
		if !ok {
			v.add(p+".interface", "unknown interface %q", iface)
		} else if role != "wan" {
			v.add(p+".interface", "interface %q has role %q: DDNS needs a WAN interface", iface, role)
		}

		switch d.Provider {
		case "cloudflare":
			if d.Token == "" {
				v.add(p+".token", "token is required for cloudflare")
			}
			if d.ZoneID == "" {
				v.add(p+".zone_id", "zone_id is required for cloudflare")
			}
		case "duckdns":
			if d.Token == "" {
				v.add(p+".token", "token is required for duckdns")
			}
		case "custom":
			if d.URL == "" {
				v.add(p+".url", "url is required for custom provider")
			} else if !strings.HasPrefix(d.URL, "https://") {
				v.add(p+".url", "url must start with https://")
			} else if !strings.Contains(d.URL, "{ip}") {
				v.add(p+".url", "url must contain {ip}")
			}
		default:
			v.add(p+".provider", "unknown provider %q: want cloudflare, duckdns or custom", d.Provider)
		}
	}
}
