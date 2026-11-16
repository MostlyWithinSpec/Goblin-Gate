// Package homelab is the homelab plugin: it finds the VMs and containers of a
// Proxmox VE cluster and of Docker hosts and keeps the plugin's live address
// objects filled with their addresses, so policies follow them as they come,
// go and change address.
//
// A guest belongs to object X when it carries the tag (Proxmox) or label
// (Docker) gg-X, or when its name matches one of the admin's name patterns
// for X. Only the live objects the admin gave this plugin are filled.
package homelab

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

// Settings are the plugin's settings, checked.
type Settings struct {
	Interval time.Duration
	Proxmox  *ProxmoxSettings // nil: no Proxmox
	Docker   []DockerSettings
	Patterns []Pattern
	// Networks limits the addresses used to these prefixes (empty: any
	// address but loopback, link-local and multicast).
	Networks []netip.Prefix
}

// ProxmoxSettings: a Proxmox VE API endpoint and a read-only API token.
type ProxmoxSettings struct {
	URL         string // https://pve.lan:8006
	TokenID     string // user@realm!name
	TokenSecret string
	TLS         *tls.Config
}

// DockerSettings: one Docker Engine API endpoint over TLS with a client
// certificate (dockerd --tlsverify).
type DockerSettings struct {
	URL string // https://docker.lan:2376
	// Host is the address the Docker host's containers come from when they
	// sit behind its NAT (bridge networks); invalid = resolve URL's host.
	Host netip.Addr
	TLS  *tls.Config
}

// Pattern puts guests whose name matches Glob into Object.
type Pattern struct {
	Object, Glob string
}

// Matches reports whether a guest name matches the pattern (case-insensitive,
// path.Match syntax: * ? [a-z]).
func (p Pattern) Matches(name string) bool {
	ok, _ := path.Match(strings.ToLower(p.Glob), strings.ToLower(name))
	return ok
}

var tokenIDRe = regexp.MustCompile(`^[^@!\s]+@[^@!\s]+![A-Za-z][A-Za-z0-9._-]*$`)

// SettingsFrom checks the plugin's settings (pluginsdk.Self.Settings).
func SettingsFrom(m map[string]any) (Settings, error) {
	str := func(k string) string { v, _ := m[k].(string); return strings.TrimSpace(v) }
	list := func(k string) []string {
		var out []string
		if l, ok := m[k].([]any); ok {
			for _, v := range l {
				if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
					out = append(out, strings.TrimSpace(s))
				}
			}
		}
		return out
	}
	s := Settings{Interval: 60 * time.Second}
	if v, ok := m["interval_seconds"].(float64); ok && v > 0 {
		s.Interval = time.Duration(v) * time.Second
	}

	if u := str("proxmox_url"); u != "" {
		base, err := httpsURL(u, "8006")
		if err != nil {
			return s, fmt.Errorf("proxmox_url: %w", err)
		}
		p := &ProxmoxSettings{URL: base, TokenID: str("proxmox_token_id"), TokenSecret: str("proxmox_token_secret")}
		if !tokenIDRe.MatchString(p.TokenID) {
			return s, errors.New("proxmox_token_id: want the token's full ID, such as goblin@pve!gate")
		}
		if p.TokenSecret == "" {
			return s, errors.New("proxmox_token_secret: enter the API token's secret")
		}
		p.TLS, err = serverTLS(str("proxmox_ca"), str("proxmox_fingerprint"))
		if err != nil {
			return s, fmt.Errorf("proxmox: %w", err)
		}
		s.Proxmox = p
	}

	if u := str("docker_url"); u != "" {
		d, err := dockerSettings(u, str("docker_ca"), str("docker_cert"), str("docker_key"), str("docker_host_address"))
		if err != nil {
			return s, err
		}
		s.Docker = append(s.Docker, d)
	}

	if s.Proxmox == nil && len(s.Docker) == 0 {
		return s, errors.New("nothing to watch: set proxmox_url or docker_url")
	}

	for _, p := range list("name_patterns") {
		obj, glob, ok := strings.Cut(p, "=")
		obj, glob = strings.TrimSpace(obj), strings.TrimSpace(glob)
		if !ok || obj == "" || glob == "" {
			return s, fmt.Errorf("name_patterns: %q: want object=pattern, such as web=web-*", p)
		}
		if _, err := path.Match(glob, ""); err != nil {
			return s, fmt.Errorf("name_patterns: %q: %v", p, err)
		}
		s.Patterns = append(s.Patterns, Pattern{Object: obj, Glob: glob})
	}
	for _, n := range list("networks") {
		p, err := netip.ParsePrefix(n)
		if err != nil {
			return s, fmt.Errorf("networks: %q is not a prefix such as 192.168.20.0/24", n)
		}
		s.Networks = append(s.Networks, p.Masked())
	}
	return s, nil
}

func dockerSettings(u, ca, cert, key, host string) (DockerSettings, error) {
	var d DockerSettings
	if strings.HasPrefix(u, "tcp://") {
		u = "https://" + strings.TrimPrefix(u, "tcp://")
	}
	base, err := httpsURL(u, "2376")
	if err != nil {
		return d, fmt.Errorf("docker_url: %w (dockerd must listen with --tlsverify)", err)
	}
	d.URL = base
	if ca == "" || cert == "" || key == "" {
		return d, errors.New("docker: docker_ca, docker_cert and docker_key are all needed: the Docker API without a client certificate is root on that host for anyone")
	}
	pool, err := certPool(ca)
	if err != nil {
		return d, fmt.Errorf("docker_ca: %w", err)
	}
	pair, err := tls.X509KeyPair(normalizePEM(cert), normalizePEM(key))
	if err != nil {
		return d, fmt.Errorf("docker_cert/docker_key: %w", err)
	}
	d.TLS = &tls.Config{RootCAs: pool, Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	if host != "" {
		if d.Host, err = netip.ParseAddr(host); err != nil {
			return d, fmt.Errorf("docker_host_address: %q is not an address", host)
		}
		d.Host = d.Host.Unmap()
	}
	return d, nil
}

// httpsURL checks an https URL and returns its scheme://host:port, adding
// defPort when there is none.
func httpsURL(raw, defPort string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%q is not a URL such as https://host:%s", raw, defPort)
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("%q: only https", raw)
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(strings.Trim(u.Hostname(), "[]"), defPort)
	}
	return "https://" + host, nil
}

// serverTLS verifies a server against a CA (PEM), a pinned SHA-256
// certificate fingerprint, or, with neither, the system's roots.
func serverTLS(ca, fingerprint string) (*tls.Config, error) {
	c := &tls.Config{MinVersion: tls.VersionTLS12}
	switch {
	case fingerprint != "":
		want, err := hex.DecodeString(strings.NewReplacer(":", "", " ", "").Replace(fingerprint))
		if err != nil || len(want) != sha256.Size {
			return nil, fmt.Errorf("fingerprint: want the certificate's SHA-256 fingerprint, 32 hex bytes such as AB:CD:...")
		}
		// a pinned certificate stands in for the chain; VerifyConnection
		// also runs on resumed sessions
		c.InsecureSkipVerify = true
		c.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("no server certificate")
			}
			got := sha256.Sum256(cs.PeerCertificates[0].Raw)
			if !strings.EqualFold(hex.EncodeToString(got[:]), hex.EncodeToString(want)) {
				return fmt.Errorf("server certificate fingerprint %s is not the one in the settings", colonHex(got[:]))
			}
			return nil
		}
	case ca != "":
		pool, err := certPool(ca)
		if err != nil {
			return nil, fmt.Errorf("ca: %w", err)
		}
		c.RootCAs = pool
	}
	return c, nil
}

func colonHex(b []byte) string {
	parts := make([]string, len(b))
	for i, x := range b {
		parts[i] = fmt.Sprintf("%02X", x)
	}
	return strings.Join(parts, ":")
}

func certPool(p string) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(normalizePEM(p)) {
		return nil, errors.New("no PEM certificate in it")
	}
	return pool, nil
}

var pemBlockRe = regexp.MustCompile(`(?s)-----BEGIN ([A-Z0-9 ]+)-----(.*?)-----END ([A-Z0-9 ]+)-----`)

// normalizePEM restores PEM text whose line breaks a one-line form field
// turned into spaces (or removed): it rewraps every block's base64 body.
func normalizePEM(s string) []byte {
	s = strings.TrimSpace(s)
	if b, _ := pem.Decode([]byte(s)); b != nil {
		return []byte(s)
	}
	var out strings.Builder
	for _, m := range pemBlockRe.FindAllStringSubmatch(s, -1) {
		if m[1] != m[3] {
			continue
		}
		body := strings.Join(strings.Fields(m[2]), "")
		out.WriteString("-----BEGIN " + m[1] + "-----\n")
		for len(body) > 64 {
			out.WriteString(body[:64] + "\n")
			body = body[64:]
		}
		if body != "" {
			out.WriteString(body + "\n")
		}
		out.WriteString("-----END " + m[1] + "-----\n")
	}
	return []byte(out.String())
}
