package reverseproxy

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// writeCert stores a certificate for domain the way Caddy does, with its
// file time set to obtained.
func writeCert(t *testing.T, dir, issuerDir, domain string, notAfter, obtained time.Time) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: domain},
		Issuer:       pkix.Name{CommonName: "R11", Organization: []string{"Let's Encrypt"}},
		DNSNames:     []string{domain},
		NotBefore:    notAfter.Add(-90 * 24 * time.Hour),
		NotAfter:     notAfter,
	}
	// self-signed: the issuer is the subject, so give the subject the issuer's name
	tmpl.Subject = tmpl.Issuer
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "certificates", issuerDir, domain, domain+".crt")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, obtained, obtained); err != nil {
		t.Fatal(err)
	}
}

func logLine(level string, at time.Time, logger, domain, msg, errText string) string {
	return fmt.Sprintf(`{"level":%q,"ts":%d.5,"logger":%q,"msg":%q,"identifier":%q,"error":%q}`,
		level, at.Unix(), logger, msg, domain, errText)
}

func TestStatus(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	const prod = "acme-v02.api.letsencrypt.org-directory"
	writeCert(t, dir, prod, "media.example.com", now.Add(60*24*time.Hour), now.Add(-30*24*time.Hour))
	// an older one of the same domain from another issuer: the later expiry wins
	writeCert(t, dir, "acme-staging-v02.api.letsencrypt.org-directory", "media.example.com", now.Add(10*24*time.Hour), now.Add(-80*24*time.Hour))
	writeCert(t, dir, "acme-staging-v02.api.letsencrypt.org-directory", "test.example.com", now.Add(80*24*time.Hour), now.Add(-time.Hour))
	writeCert(t, dir, prod, "nas.example.com", now.Add(5*24*time.Hour), now.Add(-85*24*time.Hour))

	journal := []string{
		"not json",
		logLine("error", now.Add(-40*24*time.Hour), "tls.obtain", "media.example.com", "could not get certificate", "old, before the cert"),
		logLine("error", now.Add(-2*time.Hour), "tls.renew", "nas.example.com", "could not get certificate from issuer", "HTTP 400 urn:ietf:params:acme:error:connection"),
		logLine("error", now.Add(-3*time.Hour), "tls.renew", "nas.example.com", "older", ""),
		logLine("info", now, "tls.obtain", "files.example.com", "certificate obtained successfully", ""),
		logLine("error", now.Add(-time.Hour), "http.log.error", "files.example.com", "not a tls logger", ""),
		logLine("error", now.Add(-time.Minute), "tls.obtain", "new.example.com", "could not get certificate", "DNS problem: NXDOMAIN"),
	}
	r := &Reader{StorageDir: dir, Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "systemctl":
			return []byte("active\n"), nil
		case "journalctl":
			out := ""
			for _, l := range journal {
				out += l + "\n"
			}
			return []byte(out), nil
		}
		return nil, errors.New("unexpected " + name)
	}}
	cfg := config.Default()
	cfg.ReverseProxy = config.ReverseProxy{Enabled: true, Hosts: []config.ProxyHost{
		{Name: "jellyfin", Enabled: true, Domains: []string{"media.example.com", "test.example.com"}, Upstream: "http://192.168.1.10:8096"},
		{Name: "nas", Enabled: false, Domains: []string{"nas.example.com", "files.example.com", "new.example.com", "../etc"}, Upstream: "https://192.168.1.20:5001"},
	}}
	st := r.Status(context.Background(), cfg)
	if !st.Enabled || st.State != "active" || len(st.Hosts) != 2 {
		t.Fatalf("%+v", st)
	}
	if st.Hosts[1].Enabled || st.Hosts[1].Upstream != "https://192.168.1.20:5001" {
		t.Errorf("host 1: %+v", st.Hosts[1])
	}

	media := st.Hosts[0].Domains[0]
	if c := media.Certificate; c == nil || !c.NotAfter.Equal(now.Add(60*24*time.Hour)) || c.Staging || c.Issuer != "Let's Encrypt R11" || !c.Obtained.Equal(now.Add(-30*24*time.Hour)) {
		t.Errorf("media cert: %+v", media.Certificate)
	}
	if media.Error != nil {
		t.Errorf("an error from before the certificate is shown: %+v", media.Error)
	}
	if c := st.Hosts[0].Domains[1].Certificate; c == nil || !c.Staging {
		t.Errorf("staging cert: %+v", c)
	}

	nas := st.Hosts[1].Domains[0]
	if nas.Certificate == nil || nas.Error == nil || nas.Error.Message != "could not get certificate from issuer: HTTP 400 urn:ietf:params:acme:error:connection" {
		t.Errorf("nas: %+v %+v", nas.Certificate, nas.Error)
	}
	if !nas.Error.Time.Equal(now.Add(-2 * time.Hour).Add(500 * time.Millisecond)) {
		t.Errorf("nas error time %v", nas.Error.Time)
	}
	files := st.Hosts[1].Domains[1]
	if files.Certificate != nil || files.Error != nil {
		t.Errorf("files (info line, non-tls logger): %+v %+v", files.Certificate, files.Error)
	}
	if e := st.Hosts[1].Domains[2].Error; e == nil || e.Message != "could not get certificate: DNS problem: NXDOMAIN" {
		t.Errorf("new: %+v", e)
	}
	if d := st.Hosts[1].Domains[3]; d.Certificate != nil {
		t.Errorf("path in a domain: %+v", d)
	}
}

func TestStatusNotRunning(t *testing.T) {
	r := &Reader{StorageDir: t.TempDir(), Run: func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if name == "systemctl" {
			return []byte("inactive\n"), errors.New("exit status 3")
		}
		return nil, errors.New("no journal")
	}}
	st := r.Status(context.Background(), config.Default())
	if st.Enabled || st.State != "inactive" || st.Hosts == nil || len(st.Hosts) != 0 {
		t.Errorf("%+v", st)
	}
	r.Run = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("no systemctl") }
	if st := r.Status(context.Background(), config.Default()); st.State != "unknown" {
		t.Errorf("state %q", st.State)
	}
}
