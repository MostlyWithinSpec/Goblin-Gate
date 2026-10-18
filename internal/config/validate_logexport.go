package config

import (
	"fmt"
	"net/url"
	"strings"
)

// MaxLogExportEntries is the maximum number of log export destinations.
const MaxLogExportEntries = 4

// validLogKind reports whether s is one of the event kinds a log export can filter on.
func validLogKind(s string) bool {
	return oneOf(s, "traffic", "ips", "dns", "webfilter", "system")
}

func (v *validator) checkLogExport() {
	c := v.cfg
	if len(c.LogExport) > MaxLogExportEntries {
		v.add("log_export", "at most %d entries", MaxLogExportEntries)
	}
	seen := map[string]bool{}
	for i, e := range c.LogExport {
		p := fmt.Sprintf("log_export[%d]", i)
		if !validName(e.Name) {
			v.add(p+".name", "invalid name %q: must be 1-63 characters of [A-Za-z0-9_.-]", e.Name)
		} else if seen[e.Name] {
			v.add(p+".name", "duplicate log export name %q", e.Name)
		}
		seen[e.Name] = true

		if !oneOf(e.Type, "syslog", "loki") {
			v.add(p+".type", "unknown type %q: want syslog or loki", e.Type)
		}
		for j, k := range e.Kinds {
			if !validLogKind(k) {
				v.add(fmt.Sprintf("%s.kinds[%d]", p, j), "unknown kind %q: want traffic, ips, dns, webfilter or system", k)
			}
		}

		switch e.Type {
		case "syslog":
			if e.Address == "" {
				v.add(p+".address", "address is required for syslog")
			} else if !validHostPort(e.Address) {
				v.add(p+".address", "invalid address %q: want host:port, e.g. 10.0.0.2:514", e.Address)
			}
			proto := e.Protocol
			if proto == "" {
				proto = "udp"
			}
			if !oneOf(proto, "udp", "tcp", "tls") {
				v.add(p+".protocol", "unknown protocol %q: want udp, tcp or tls", e.Protocol)
			}
		case "loki":
			if e.URL == "" {
				v.add(p+".url", "url is required for loki")
			} else if !strings.HasPrefix(e.URL, "https://") && !strings.HasPrefix(e.URL, "http://") {
				v.add(p+".url", "url must start with https:// or http://")
			} else if !strings.HasSuffix(e.URL, "/loki/api/v1/push") {
				v.add(p+".url", "url must end in /loki/api/v1/push")
			} else if u, err := url.Parse(e.URL); err != nil || u.User != nil || u.Host == "" {
				// the URL is shown to read-only users; credentials belong in
				// username/password or token, which are hidden
				v.add(p+".url", "put credentials in username and password (or token), not in the url")
			}
		}
	}
}
