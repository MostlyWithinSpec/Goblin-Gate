// Package caddy renders the reverse proxy's configuration: a Caddy JSON
// config (Debian's caddy package) that publishes reverse_proxy.hosts with
// certificates from Let's Encrypt.
//
// Caddy runs as its own unprivileged user (goblin-caddy.service) on high
// ports; the nft renderer redirects TCP 80 and 443 on the WAN, and on
// LAN-side interfaces towards a box address that is not a LAN address (a
// LAN client that opened a public name), to HTTPPort and HTTPSPort. The admin
// page keeps port 443 on the LAN. Caddy's admin API is off: the config is
// only what this package writes, and a commit restarts the service.
package caddy

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Paths, ports and the unit, shared with the nft renderer, the applier and
// the API.
const (
	Dir        = "/etc/goblingate/caddy/"
	ConfPath   = Dir + "caddy.json"
	StorageDir = "/var/lib/goblin-caddy" // certificates and the ACME account (StateDirectory of the unit)
	Unit       = "goblin-caddy.service"
	HTTPPort   = 10080 // where WAN port 80 goes: ACME HTTP challenges and the redirect to https
	HTTPSPort  = 10443 // where WAN port 443 goes

	letsEncrypt        = "https://acme-v02.api.letsencrypt.org/directory"
	letsEncryptStaging = "https://acme-staging-v02.api.letsencrypt.org/directory"
)

// Renderer implements commit.Renderer.
type Renderer struct{}

// Name returns "caddy".
func (Renderer) Name() string { return "caddy" }

// Render returns caddy.json while the proxy is enabled and has an enabled
// host; nothing otherwise (the applier then stops the service).
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

// Active reports whether the proxy runs: enabled, with at least one enabled
// host. The nft renderer only redirects ports while it does.
func Active(cfg *config.Config) bool {
	if !cfg.ReverseProxy.Enabled {
		return false
	}
	return slices.ContainsFunc(cfg.ReverseProxy.Hosts, func(h config.ProxyHost) bool { return h.Enabled })
}

// obj is a JSON object; Caddy's config is all nested objects.
type obj = map[string]any

func build(cfg *config.Config) (obj, error) {
	rp := cfg.ReverseProxy
	var routes []any
	var subjects []string
	for _, h := range rp.Hosts {
		if !h.Enabled {
			continue
		}
		up, err := config.ParseProxyUpstream(h.Upstream)
		if err != nil {
			return nil, fmt.Errorf("render caddy: host %q: %w", h.Name, err)
		}
		var inner []any
		if len(h.Allow) > 0 {
			ranges := make([]string, 0, len(h.Allow))
			for _, a := range h.Allow {
				p, err := cfg.ProxyAllowPrefix(a)
				if err != nil {
					return nil, fmt.Errorf("render caddy: host %q: %w", h.Name, err)
				}
				ranges = append(ranges, p.String())
			}
			inner = append(inner, obj{
				"match":    []any{obj{"not": []any{obj{"remote_ip": obj{"ranges": ranges}}}}},
				"handle":   []any{obj{"handler": "static_response", "status_code": 403, "body": "Forbidden"}},
				"terminal": true,
			})
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
	}

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

	return obj{
		"admin":   obj{"disabled": true},
		"storage": obj{"module": "file_system", "root": StorageDir},
		"logging": obj{"logs": obj{"default": obj{"level": "INFO", "writer": obj{"output": "stderr"}, "encoder": obj{"format": "json"}}}},
		"apps": obj{
			"http": obj{
				"http_port":  HTTPPort,
				"https_port": HTTPSPort,
				"servers": obj{
					"proxy": obj{
						"listen": []string{fmt.Sprintf(":%d", HTTPSPort)},
						"routes": routes,
						// the redirect server below sends http to https on the
						// public port (Caddy's own would name HTTPSPort)
						"automatic_https": obj{"disable_redirects": true},
					},
					"redirect": obj{
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
					},
				},
			},
			"tls": obj{
				"automation": obj{
					"policies": []any{obj{"subjects": subjects, "issuers": []any{issuer}}},
				},
			},
		},
	}, nil
}

func isAddr(host string) bool {
	_, err := netip.ParseAddr(host)
	return err == nil
}
