package api

import (
	"net/http"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/auth"
)

// Token scopes (task 102). Every GET needs auth.ScopeRead; every other
// route is listed in routeScopes (TestRouteScopes fails for a new route
// without an entry). scopeAdmin routes stay with full admins: a scoped
// token never reaches them. scopeCandidate defers to the config path the
// request changes (configScope).
const (
	scopeAdmin     = "admin"
	scopeCandidate = "candidate"
)

var routeScopes = map[string]string{
	// read-only POSTs
	"POST /api/v1/config/validate":        auth.ScopeRead,
	"POST /api/v1/templates/{id}/preview": auth.ScopeRead,
	"POST /api/v1/policy/simulate":        auth.ScopeRead,
	"POST /api/v1/stats/query":            auth.ScopeRead,
	"POST /api/v1/auth/logout":            auth.ScopeRead,

	"PATCH /api/v1/config/candidate": scopeCandidate,
	"PUT /api/v1/config/candidate":   scopeAdmin, // the whole document

	"POST /api/v1/commit":                   auth.ScopeCommit,
	"POST /api/v1/commit/confirm":           auth.ScopeCommit,
	"POST /api/v1/commit/discard":           auth.ScopeCommit,
	"POST /api/v1/revisions/{rev}/rollback": auth.ScopeCommit,

	"POST /api/v1/snapshots":              auth.ScopeWriteSystem,
	"POST /api/v1/checks/dismiss":         auth.ScopeWriteSystem,
	"DELETE /api/v1/checks/dismiss/{key}": auth.ScopeWriteSystem,
	"DELETE /api/v1/snapshots/{name}":     auth.ScopeWriteSystem,

	// actions: change no config, act on the box or ask it something
	"POST /api/v1/blocks":               auth.ScopeActions,
	"DELETE /api/v1/blocks/{ip}":        auth.ScopeActions,
	"PATCH /api/v1/devices/{mac}":       auth.ScopeActions,
	"DELETE /api/v1/devices/{mac}":      auth.ScopeActions,
	"POST /api/v1/devices/{mac}/wake":   auth.ScopeActions,
	"POST /api/v1/ddns/{name}/update":   auth.ScopeActions,
	"POST /api/v1/diag/ping":            auth.ScopeActions,
	"POST /api/v1/diag/traceroute":      auth.ScopeActions,
	"POST /api/v1/diag/dns":             auth.ScopeActions,
	"POST /api/v1/feeds/{name}/refresh": auth.ScopeActions,
	"POST /api/v1/geoip/refresh":        auth.ScopeActions,
	"POST /api/v1/notifications/test":   auth.ScopeActions,
	"PUT /api/v1/ui/dashboards":         auth.ScopeActions,
	"POST /api/v1/git-backup/push":      auth.ScopeActions,

	// full admins only: accounts, credentials, code, whole-box changes
	"POST /api/v1/admins":                                  scopeAdmin,
	"PATCH /api/v1/admins/{name}":                          scopeAdmin,
	"DELETE /api/v1/admins/{name}":                         scopeAdmin,
	"POST /api/v1/auth/tokens":                             scopeAdmin,
	"DELETE /api/v1/auth/tokens/{id}":                      scopeAdmin,
	"DELETE /api/v1/auth/sessions/{id}":                    scopeAdmin,
	"POST /api/v1/auth/password":                           scopeAdmin,
	"POST /api/v1/auth/2fa/setup":                          scopeAdmin,
	"POST /api/v1/auth/2fa/enable":                         scopeAdmin,
	"POST /api/v1/auth/2fa/disable":                        scopeAdmin,
	"POST /api/v1/system/restore":                          scopeAdmin,
	"POST /api/v1/snapshots/{name}/restore":                scopeAdmin,
	"POST /api/v1/import/pfsense":                          scopeAdmin,
	"POST /api/v1/templates/{id}/apply":                    scopeAdmin, // changes many sections at once
	"POST /api/v1/system/updates/apply":                    scopeAdmin,
	"POST /api/v1/system/reboot":                           scopeAdmin,
	"POST /api/v1/plugins/{name}/enable":                   scopeAdmin,
	"POST /api/v1/plugins/{name}/disable":                  scopeAdmin,
	"PUT /api/v1/plugins/{name}/settings":                  scopeAdmin,
	"POST /api/v1/git-backup/forget-host-key":              scopeAdmin,
	"PUT /api/v1/vpn/wireguard-clients/{name}/private-key": scopeAdmin,
	"POST /api/v1/vpn/wireguard/{name}/peers":              scopeAdmin, // answers with a private key
	"POST /api/v1/vpn/tailscale/login":                     scopeAdmin,
	"POST /api/v1/vpn/tailscale/logout":                    scopeAdmin,
	// a plugin's own API (any method): what it does is up to the plugin
	"/api/v1/plugins/{name}/api/{path...}": scopeAdmin,

	"POST /api/v1/capture":        scopeAdmin,
	"DELETE /api/v1/capture/{id}": scopeAdmin,
}

// sectionScopes maps each top-level config section to the scope that may
// change it with PATCH. Sections that grant root or reach outside the box
// (admins, plugins, git history, the update source, SSH keys) stay with
// full admins.
var sectionScopes = map[string]string{
	"addresses": auth.ScopeWriteFirewall, "address_groups": auth.ScopeWriteFirewall,
	"services": auth.ScopeWriteFirewall, "service_groups": auth.ScopeWriteFirewall,
	"policies": auth.ScopeWriteFirewall, "schedules": auth.ScopeWriteFirewall,
	"nat": auth.ScopeWriteFirewall, "profiles": auth.ScopeWriteFirewall,
	"feeds": auth.ScopeWriteFirewall, "geoip": auth.ScopeWriteFirewall,
	"upnp": auth.ScopeWriteFirewall, "custom_rules": auth.ScopeWriteFirewall,
	"crowdsec": auth.ScopeWriteFirewall,
	"ups":      auth.ScopeWriteSystem,
	"ips":      auth.ScopeWriteFirewall,

	"interfaces": auth.ScopeWriteNetwork, "vlans": auth.ScopeWriteNetwork,
	"dhcp": auth.ScopeWriteNetwork, "dns": auth.ScopeWriteNetwork,
	"gateways": auth.ScopeWriteNetwork, "gateway_groups": auth.ScopeWriteNetwork,
	"routes": auth.ScopeWriteNetwork, "default_gateway": auth.ScopeWriteNetwork,
	"vpn": auth.ScopeWriteNetwork, "ddns": auth.ScopeWriteNetwork,

	"system": auth.ScopeWriteSystem, "notifications": auth.ScopeWriteSystem,
	"log_export": auth.ScopeWriteSystem,

	"schema_version": scopeAdmin, "admins": scopeAdmin, "plugins": scopeAdmin,
	"git_backup": scopeAdmin,
}

// adminSystemKeys are the system settings only full admins may change:
// root logins and where the box installs software from.
var adminSystemKeys = map[string]bool{"ssh_keys": true, "update_url": true, "update_channel": true}

// configScope returns the scope a PATCH of config path p needs. Unknown
// sections and the whole "system" object need a full admin.
func configScope(p string) string {
	top, rest := p, ""
	if i := strings.IndexAny(p, ".["); i >= 0 {
		top, rest = p[:i], p[i:]
	}
	sc, ok := sectionScopes[top]
	if !ok {
		return scopeAdmin
	}
	if top == "system" {
		key := strings.TrimPrefix(rest, ".")
		if i := strings.IndexAny(key, ".["); i >= 0 {
			key = key[:i]
		}
		if key == "" || adminSystemKeys[key] {
			return scopeAdmin
		}
	}
	return sc
}

// routeScope returns the scope a request to pattern needs.
func routeScope(method, pattern string) string {
	if sc, ok := routeScopes[pattern]; ok {
		return sc
	}
	if method == http.MethodGet || method == http.MethodHead {
		return auth.ScopeRead
	}
	return scopeAdmin // a route without an entry is admin-only (and fails TestRouteScopes)
}

// allowedScope reports whether id may use a route needing scope.
func allowedScope(id auth.Identity, scope string) bool {
	switch scope {
	case scopeAdmin:
		return id.CanWrite()
	case scopeCandidate:
		// the handler checks the path (configScope); here: may write anything at all
		return id.CanWrite() || id.Has(auth.ScopeWriteFirewall) || id.Has(auth.ScopeWriteNetwork) || id.Has(auth.ScopeWriteSystem)
	}
	return id.Has(scope)
}
