package config

import (
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// ReverseProxy publishes web services from the LAN on public hostnames with
// certificates from Let's Encrypt (Caddy). Connections to ports 80 and 443
// on the WAN go to the proxy; everything else about the box stays as it is.
// IPv4 only for now (the NAT table is IPv4).
type ReverseProxy struct {
	Enabled bool `json:"enabled"`
	// Email is the contact for the Let's Encrypt account: expiry warnings
	// go there. Optional, recommended.
	Email string `json:"email,omitempty"`
	// Staging uses Let's Encrypt's staging CA: certificates browsers don't
	// trust, but no rate limits while testing a setup.
	Staging bool `json:"staging,omitempty"`
	// LANAccess also sends LAN clients that open a public name (it resolves
	// to the WAN address) to the proxy (nil = true).
	LANAccess *bool `json:"lan_access,omitempty"`
	// Hosts are the published services.
	Hosts []ProxyHost `json:"hosts,omitempty"`
}

// ProxyHost publishes one service under one or more hostnames.
type ProxyHost struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// Domains are the public hostnames (exact names, no wildcards: Let's
	// Encrypt checks each one over HTTP). Each must point at the WAN address.
	Domains []string `json:"domains"`
	// Upstream is where the service runs: http://192.168.1.10:8096 or
	// https://nas.home.lan:5001.
	Upstream string `json:"upstream"`
	// InsecureSkipVerify accepts any certificate from an https upstream
	// (self-signed, as on many NAS boxes and hypervisors).
	InsecureSkipVerify bool `json:"insecure_skip_verify,omitempty"`
	// Allow limits who may connect: address objects (host or subnet) or
	// CIDRs. Empty = everyone.
	Allow       []string `json:"allow,omitempty"`
	Description string   `json:"description,omitempty"`
}

// MaxProxyHosts is the maximum number of published services.
const MaxProxyHosts = 64

// LANAccessOn resolves the default (true).
func (p ReverseProxy) LANAccessOn() bool { return p.LANAccess == nil || *p.LANAccess }

// ProxyUpstream is a parsed, validated upstream URL.
type ProxyUpstream struct {
	TLS  bool   // https
	Host string // name or address, without brackets
	Port int
}

// Dial is the host:port to connect to.
func (u ProxyUpstream) Dial() string {
	if strings.Contains(u.Host, ":") {
		return "[" + u.Host + "]:" + strconv.Itoa(u.Port)
	}
	return u.Host + ":" + strconv.Itoa(u.Port)
}

// ParseProxyUpstream parses "http(s)://host[:port]" (no path, no user info).
func ParseProxyUpstream(s string) (ProxyUpstream, error) {
	u, err := url.Parse(s)
	if err != nil || u.Opaque != "" {
		return ProxyUpstream{}, fmt.Errorf("not a URL: want http://host:port or https://host:port")
	}
	var up ProxyUpstream
	switch u.Scheme {
	case "http":
		up.Port = 80
	case "https":
		up.TLS, up.Port = true, 443
	default:
		return ProxyUpstream{}, fmt.Errorf("scheme must be http or https")
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return ProxyUpstream{}, fmt.Errorf("only scheme, host and port: no path, query or user")
	}
	up.Host = u.Hostname()
	if !validHost(up.Host) {
		return ProxyUpstream{}, fmt.Errorf("invalid host %q", up.Host)
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return ProxyUpstream{}, fmt.Errorf("invalid port %q", p)
		}
		up.Port = n
	}
	return up, nil
}

// ProxyAllowPrefix resolves an allow entry: an address object of type host
// or subnet, or a literal address or CIDR.
func (c *Config) ProxyAllowPrefix(entry string) (netip.Prefix, error) {
	for _, a := range c.Addresses {
		if a.Name != entry {
			continue
		}
		switch a.Type {
		case "host":
			ip, err := netip.ParseAddr(a.Value)
			if err != nil {
				return netip.Prefix{}, fmt.Errorf("address %q: %v", entry, err)
			}
			ip = ip.Unmap()
			return netip.PrefixFrom(ip, ip.BitLen()), nil
		case "subnet":
			p, err := netip.ParsePrefix(a.Value)
			if err != nil {
				return netip.Prefix{}, fmt.Errorf("address %q: %v", entry, err)
			}
			return p.Masked(), nil
		default:
			return netip.Prefix{}, fmt.Errorf("address %q is a %s: use a host or subnet", entry, a.Type)
		}
	}
	if p, err := netip.ParsePrefix(entry); err == nil {
		return p.Masked(), nil
	}
	if ip, err := netip.ParseAddr(entry); err == nil {
		ip = ip.Unmap()
		return netip.PrefixFrom(ip, ip.BitLen()), nil
	}
	return netip.Prefix{}, fmt.Errorf("%q is not an address object, address or CIDR", entry)
}
