package pluginsdk

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// Helpers for plugins that talk to appliances on the LAN, which mostly have
// self-signed certificates.

// HTTPSURL checks an https URL and returns its scheme://host:port, adding
// defPort when there is none.
func HTTPSURL(raw, defPort string) (string, error) {
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

// ServerTLS verifies a server against a CA (PEM), a pinned SHA-256
// certificate fingerprint, or, with neither, the system's roots.
func ServerTLS(ca, fingerprint string) (*tls.Config, error) {
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
				return fmt.Errorf("server certificate fingerprint %s is not the one in the settings", ColonHex(got[:]))
			}
			return nil
		}
	case ca != "":
		pool, err := CertPool(ca)
		if err != nil {
			return nil, fmt.Errorf("ca: %w", err)
		}
		c.RootCAs = pool
	}
	return c, nil
}

// ColonHex formats a fingerprint as AB:CD:...
func ColonHex(b []byte) string {
	parts := make([]string, len(b))
	for i, x := range b {
		parts[i] = fmt.Sprintf("%02X", x)
	}
	return strings.Join(parts, ":")
}

func CertPool(p string) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(NormalizePEM(p)) {
		return nil, errors.New("no PEM certificate in it")
	}
	return pool, nil
}

var pemBlockRe = regexp.MustCompile(`(?s)-----BEGIN ([A-Z0-9 ]+)-----(.*?)-----END ([A-Z0-9 ]+)-----`)

// NormalizePEM restores PEM text whose line breaks a one-line form field
// turned into spaces (or removed): it rewraps every block's base64 body.
func NormalizePEM(s string) []byte {
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
