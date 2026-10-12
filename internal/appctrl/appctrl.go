// Package appctrl identifies applications for application control
// (task 035).
//
// Decision (task 035): applications are recognized by the domains they
// use, and application control profiles are enforced by the web filter's
// DNS proxy (internal/webfilter) next to web filter profiles. The options
// considered:
//   - nDPI: Debian 12 ships an old version (4.2), and using it means a
//     separate C helper on NFQUEUE (cgo is not allowed in this build) plus
//     packaging and per-flow CPU on a home router.
//   - Suricata app-layer detection: only ~25 protocols (http, tls, quic,
//     ssh, bittorrent-dht, ...), not the applications people ask for
//     (Netflix, TikTok, Steam).
//   - Suricata 8's nDPI plugin: not packaged for Debian 12.
//
// Domain signatures cover the asked-for services on any port, with the
// filter that already applies per client subnet, shows a block page and
// logs. Their limits: apps that hard-code IP addresses or tunnel (VPNs,
// some games' match traffic) are not seen; protocol-level control can be
// added when Suricata 8 with nDPI is available.
package appctrl

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strings"
)

//go:embed apps.json
var catalogJSON []byte

// App is one application signature.
type App struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Group   string   `json:"group"`
	Domains []string `json:"domains"`
}

var (
	catalog  []App
	byDomain map[string]string // domain -> app id
	byID     map[string]App
)

func init() {
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		panic("appctrl: bad apps.json: " + err.Error())
	}
	byDomain, byID = map[string]string{}, map[string]App{}
	for _, a := range catalog {
		byID[a.ID] = a
		for _, d := range a.Domains {
			byDomain[d] = a.ID
		}
	}
	sort.Slice(catalog, func(i, j int) bool {
		if catalog[i].Group != catalog[j].Group {
			return catalog[i].Group < catalog[j].Group
		}
		return catalog[i].Name < catalog[j].Name
	})
}

// Catalog returns every known application, by group and name.
func Catalog() []App { return append([]App(nil), catalog...) }

// Known reports whether id is in the catalog.
func Known(id string) bool { _, ok := byID[id]; return ok }

// Get returns an application by id.
func Get(id string) (App, bool) { a, ok := byID[id]; return a, ok }

// Identify returns the application a DNS name belongs to (the name or a
// parent domain is one of its domains) and the matching domain.
func Identify(name string) (id, match string, ok bool) {
	d := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	for d != "" {
		if id, ok := byDomain[d]; ok {
			return id, d, true
		}
		i := strings.Index(d, ".")
		if i < 0 {
			break
		}
		d = d[i+1:]
	}
	return "", "", false
}
