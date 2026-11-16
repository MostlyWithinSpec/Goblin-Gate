// Package caddy renders the reverse proxy's configuration: a Caddy JSON
// config (Debian's caddy package) that publishes reverse_proxy.hosts with
// certificates from Let's Encrypt or the internal CA, and runs the internal
// CA (Caddy's PKI app) with its ACME directory for the LAN.
//
// Caddy runs as its own unprivileged user (goblin-caddy.service) on high
// ports; the nft renderer redirects TCP 80 and 443 on the WAN, and on
// LAN-side interfaces towards a box address that is not a LAN address (a
// LAN client that opened a public name) or towards
// config.InternalProxyAddress (a name with an internal certificate), to
// HTTPPort and HTTPSPort. The admin page keeps port 443 on the LAN. The ACME
// directory listens on config.ACMEPort, which nft opens on LAN-side
// interfaces only. Caddy's admin API is off: the config is only what this
// package writes, and a commit restarts the service.
package caddy

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Paths, ports and the unit, shared with the nft renderer, the applier and
// the API.
const (
	Dir        = "/etc/goblingate/caddy/"
	ConfPath   = Dir + "caddy.json"
	StorageDir = "/var/lib/goblin-caddy" // certificates, the ACME account and the internal CA (StateDirectory of the unit)
	Unit       = "goblin-caddy.service"
	HTTPPort   = 10080 // where WAN port 80 goes: ACME HTTP challenges and the redirect to https
	HTTPSPort  = 10443 // where WAN port 443 goes

	// CAID is the internal CA's ID in Caddy's PKI app; its root certificate
	// is at RootPath.
	CAID              = "local"
	RootPath          = StorageDir + "/pki/authorities/" + CAID + "/root.crt"
	ACMEDirectoryPath = "/acme/" + CAID + "/directory"

	letsEncrypt        = "https://acme-v02.api.letsencrypt.org/directory"
	letsEncryptStaging = "https://acme-staging-v02.api.letsencrypt.org/directory"
)

// Renderer implements commit.Renderer.
type Renderer struct{}

// Name returns "caddy".
func (Renderer) Name() string { return "caddy" }

// Render returns caddy.json while the proxy or the internal CA runs; nothing
// otherwise (the applier then stops the service).
func (Renderer) Render(cfg *config.Config) (map[string][]byte, error) {
	if !Active(cfg) {
		return map[string][]byte{}, nil
	}
	doc, err := build(cfg)
	if err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("render caddy: %w", err)
	}
	return map[string][]byte{ConfPath: append(b, '\n')}, nil
}

// Active reports whether goblin-caddy runs: for the reverse proxy or for the
// internal CA.
func Active(cfg *config.Config) bool {
	return ProxyActive(cfg) || cfg.InternalCA.Enabled
}

// ProxyActive reports whether the proxy publishes hosts: enabled, with at
// least one enabled host. The nft renderer only redirects ports while it
// does.
func ProxyActive(cfg *config.Config) bool {
	if !cfg.ReverseProxy.Enabled {
		return false
	}
	return slices.ContainsFunc(cfg.ReverseProxy.Hosts, func(h config.ProxyHost) bool { return h.Enabled })
}

// InternalDomains are the names of the enabled hosts with internal
// certificates while the proxy runs: the box's DNS answers them with
// config.InternalProxyAddress.
func InternalDomains(cfg *config.Config) []string {
	if !ProxyActive(cfg) {
		return nil
	}
	var out []string
	for _, h := range cfg.ReverseProxy.Hosts {
		if h.Enabled && h.IsInternal() {
			out = append(out, h.Domains...)
		}
	}
	return out
}

// ACMEHosts are the names the internal ACME directory answers on: with a
// hostname <hostname>.<local domain>, and the box's LAN-side IPv4 addresses.
func ACMEHosts(cfg *config.Config) []string {
	var out []string
	if cfg.System.Hostname != "" {
		out = append(out, strings.ToLower(cfg.System.Hostname+"."+cfg.EffectiveLocalDomain()))
	}
	for _, a := range cfg.LANSideAddrs() {
		out = append(out, a.String())
	}
	return out
}

// ACMEDirectories are the URLs of the ACME directory, one per name (none
// while it is off).
func ACMEDirectories(cfg *config.Config) []string {
	if !cfg.InternalCA.Enabled || !cfg.InternalCA.ACME {
		return nil
	}
	var out []string
	for _, h := range ACMEHosts(cfg) {
		out = append(out, fmt.Sprintf("https://%s:%d%s", h, config.ACMEPort, ACMEDirectoryPath))
	}
	return out
}

// obj is a JSON object; Caddy's config is all nested objects.
type obj = map[string]any

// ranges resolves allow entries to CIDRs.
func ranges(cfg *config.Config, allow []string) ([]string, error) {
	out := make([]string, 0, len(allow))
	for _, a := range allow {
		p, err := cfg.ProxyAllowPrefix(a)
		if err != nil {
			return nil, err
		}
		out = append(out, p.String())
	}
	return out, nil
}

func lanRanges(cfg *config.Config) []string {
	var out []string
	for _, p := range cfg.LANSidePrefixes() {
		out = append(out, p.String())
	}
	return out
}

// forbidOthers answers 403 to clients outside ranges.
func forbidOthers(ranges []string) obj {
	return obj{
		"match":    []any{obj{"not": []any{obj{"remote_ip": obj{"ranges": ranges}}}}},
		"handle":   []any{obj{"handler": "static_response", "status_code": 403, "body": "Forbidden"}},
		"terminal": true,
	}
}

func build(cfg *config.Config) (obj, error) {
	rp := cfg.ReverseProxy
	ca := cfg.InternalCA
	lifetime := fmt.Sprintf("%dh", ca.Lifetime())
	var routes []any
	var subjects, public, internal []string
	if ProxyActive(cfg) {
		for _, h := range rp.Hosts {
			if !h.Enabled {
				continue
			}
			up, err := config.ParseProxyUpstream(h.Upstream)
			if err != nil {
				return nil, fmt.Errorf("render caddy: host %q: %w", h.Name, err)
			}
			allow, err := ranges(cfg, h.Allow)
			if err != nil {
				return nil, fmt.Errorf("render caddy: host %q: %w", h.Name, err)
			}
			if len(allow) == 0 && h.IsInternal() {
				// a name inside the network is for the network
				allow = lanRanges(cfg)
			}
			var inner []any
			if len(allow) > 0 {
				inner = append(inner, forbidOthers(allow))
			}
			proxy := obj{"handler": "reverse_proxy", "upstreams": []any{obj{"dial": up.Dial()}}}
			if up.TLS {
				tls := obj{}
				if h.InsecureSkipVerify {
					tls["insecure_skip_verify"] = true
				} else if !isAddr(up.Host) {
					tls["server_name"] = up.Host
				}
				proxy["transport"] = obj{"protocol": "http", "tls": tls}
			}
			inner = append(inner, obj{"handle": []any{proxy}})
			routes = append(routes, obj{
				"match":    []any{obj{"host": h.Domains}},
				"handle":   []any{obj{"handler": "subroute", "routes": inner}},
				"terminal": true,
			})
			subjects = append(subjects, h.Domains...)
			if h.IsInternal() {
				internal = append(internal, h.Domains...)
			} else {
				public = append(public, h.Domains...)
			}
		}
	}

	servers := obj{}
	if len(routes) > 0 {
		servers["proxy"] = obj{
			"listen": []string{fmt.Sprintf(":%d", HTTPSPort)},
			"routes": routes,
			// the redirect server below sends http to https on the public
			// port (Caddy's own would name HTTPSPort)
			"automatic_https": obj{"disable_redirects": true},
		}
		servers["redirect"] = obj{
			"listen": []string{fmt.Sprintf(":%d", HTTPPort)},
			// ACME HTTP challenges are answered before these routes
			"routes": []any{obj{
				"match": []any{obj{"host": subjects}},
				"handle": []any{obj{
					"handler":     "static_response",
					"status_code": 308,
					"headers":     obj{"Location": []string{"https://{http.request.host}{http.request.uri}"}},
				}},
			}},
		}
	}

	apps := obj{}
	var policies []any
	if len(public) > 0 {
		issuer := obj{
			"module": "acme",
			"challenges": obj{
				// Let's Encrypt connects to 80 and 443; nft hands those to these ports
				"http":     obj{"alternate_port": HTTPPort},
				"tls-alpn": obj{"alternate_port": HTTPSPort},
			},
		}
		if rp.Staging {
			issuer["ca"] = letsEncryptStaging
		} else {
			issuer["ca"] = letsEncrypt
		}
		if rp.Email != "" {
			issuer["email"] = rp.Email
		}
		policies = append(policies, obj{"subjects": public, "issuers": []any{issuer}})
	}

	if ca.Enabled {
		name := ca.CAName()
		apps["pki"] = obj{"certificate_authorities": obj{CAID: obj{
			"name":                     name,
			"root_common_name":         name + " Root",
			"intermediate_common_name": name + " Intermediate",
			// never touch the box's own trust store
			"install_trust": false,
		}}}
		if ca.ACME {
			hosts := ACMEHosts(cfg)
			allow, err := ranges(cfg, ca.ACMEAllow)
			if err != nil {
				return nil, fmt.Errorf("render caddy: internal_ca.acme_allow: %w", err)
			}
			if len(allow) == 0 {
				allow = lanRanges(cfg)
			}
			servers["acme"] = obj{
				"listen": []string{fmt.Sprintf(":%d", config.ACMEPort)},
				"routes": []any{obj{
					"match": []any{obj{"host": hosts}},
					"handle": []any{obj{"handler": "subroute", "routes": []any{
						forbidOthers(allow),
						// Caddy 2.6 (Debian) has no lifetime for acme_server: ACME
						// certificates get its default, 12 hours
						obj{"handle": []any{obj{"handler": "acme_server", "ca": CAID}}},
					}}},
					"terminal": true,
				}},
				"automatic_https": obj{"disable_redirects": true},
			}
			internal = append(internal, hosts...)
		}
		if len(internal) > 0 {
			policies = append(policies, obj{"subjects": internal, "issuers": []any{obj{"module": "internal", "ca": CAID, "lifetime": lifetime}}})
		}
	}

	if len(servers) > 0 {
		apps["http"] = obj{"http_port": HTTPPort, "https_port": HTTPSPort, "servers": servers}
	}
	if len(policies) > 0 {
		apps["tls"] = obj{"automation": obj{"policies": policies}}
	}
	return obj{
		"admin":   obj{"disabled": true},
		"storage": obj{"module": "file_system", "root": StorageDir},
		"logging": obj{"logs": obj{"default": obj{"level": "INFO", "writer": obj{"output": "stderr"}, "encoder": obj{"format": "json"}}}},
		"apps":    apps,
	}, nil
}

func isAddr(host string) bool {
	_, err := netip.ParseAddr(host)
	return err == nil
}
