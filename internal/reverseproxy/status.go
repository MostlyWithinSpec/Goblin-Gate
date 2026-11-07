// Package reverseproxy reports how the reverse proxy is doing: whether Caddy
// runs, the certificate of each published domain (read from Caddy's
// storage), and the latest certificate error per domain from its log.
package reverseproxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/caddy"
)

// Status is GET /api/v1/reverse-proxy.
type Status struct {
	Enabled bool `json:"enabled"`
	// State is the service's systemd state: active, inactive, failed, ...
	State string       `json:"state"`
	Hosts []HostStatus `json:"hosts"`
}

// HostStatus is one published service.
type HostStatus struct {
	Name     string         `json:"name"`
	Enabled  bool           `json:"enabled"`
	Upstream string         `json:"upstream"`
	Domains  []DomainStatus `json:"domains"`
}

// DomainStatus is one public name: its certificate, if Caddy has one, and
// the latest error getting one that came after it.
type DomainStatus struct {
	Domain      string       `json:"domain"`
	Certificate *Certificate `json:"certificate,omitempty"`
	Error       *LogError    `json:"error,omitempty"`
}

// Certificate is what a certificate in Caddy's storage says.
type Certificate struct {
	Issuer    string    `json:"issuer"`
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`
	Obtained  time.Time `json:"obtained"`          // when Caddy stored it
	Staging   bool      `json:"staging,omitempty"` // from Let's Encrypt's staging CA: not trusted by browsers
}

// LogError is a certificate error from Caddy's log.
type LogError struct {
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
}

// Reader reads the status. The zero value reads the real box.
type Reader struct {
	StorageDir string                                                                 // "" = caddy.StorageDir
	Run        func(ctx context.Context, name string, args ...string) ([]byte, error) // nil = exec
}

func (r *Reader) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if r.Run != nil {
		return r.Run(ctx, name, args...)
	}
	return exec.CommandContext(ctx, name, args...).Output()
}

// Status reports on the proxy as cfg (the running config) describes it.
func (r *Reader) Status(ctx context.Context, cfg *config.Config) Status {
	st := Status{Enabled: cfg.ReverseProxy.Enabled, Hosts: []HostStatus{}}
	out, _ := r.run(ctx, "systemctl", "is-active", caddy.Unit) // exits non-zero unless active
	st.State = strings.TrimSpace(string(out))
	if st.State == "" {
		st.State = "unknown"
	}
	errs := r.logErrors(ctx)
	dir := r.StorageDir
	if dir == "" {
		dir = caddy.StorageDir
	}
	for _, h := range cfg.ReverseProxy.Hosts {
		hs := HostStatus{Name: h.Name, Enabled: h.Enabled, Upstream: h.Upstream, Domains: []DomainStatus{}}
		for _, d := range h.Domains {
			ds := DomainStatus{Domain: d, Certificate: newestCert(dir, d)}
			if e, ok := errs[d]; ok && (ds.Certificate == nil || e.Time.After(ds.Certificate.Obtained)) {
				ds.Error = &e
			}
			hs.Domains = append(hs.Domains, ds)
		}
		st.Hosts = append(st.Hosts, hs)
	}
	return st
}

// newestCert finds the domain's certificate with the latest expiry under
// <dir>/certificates/<issuer>/<domain>/<domain>.crt.
func newestCert(dir, domain string) *Certificate {
	if strings.ContainsAny(domain, `/\`) || strings.Contains(domain, "..") {
		return nil
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "certificates", "*", domain, domain+".crt"))
	var best *Certificate
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		block, _ := pem.Decode(data)
		if block == nil {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		if best == nil || c.NotAfter.After(best.NotAfter) {
			issuer := c.Issuer.CommonName
			if len(c.Issuer.Organization) > 0 {
				issuer = c.Issuer.Organization[0] + " " + issuer
			}
			best = &Certificate{
				Issuer:    strings.TrimSpace(issuer),
				NotBefore: c.NotBefore,
				NotAfter:  c.NotAfter,
				Obtained:  fi.ModTime().UTC(),
				Staging:   strings.Contains(filepath.Base(filepath.Dir(filepath.Dir(p))), "staging"),
			}
		}
	}
	return best
}

// logErrors returns the latest certificate error per domain from the last
// day of Caddy's (JSON) log.
func (r *Reader) logErrors(ctx context.Context) map[string]LogError {
	out, err := r.run(ctx, "journalctl", "-u", caddy.Unit, "--since", "-24h", "-o", "cat", "--no-pager", "-n", "2000")
	errs := map[string]LogError{}
	if err != nil && len(out) == 0 {
		return errs
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var l struct {
			Level      string  `json:"level"`
			TS         float64 `json:"ts"`
			Logger     string  `json:"logger"`
			Msg        string  `json:"msg"`
			Identifier string  `json:"identifier"`
			Error      string  `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.Identifier == "" || (l.Level != "error" && l.Level != "warn") {
			continue
		}
		if !strings.HasPrefix(l.Logger, "tls") {
			continue
		}
		msg := l.Msg
		if l.Error != "" {
			msg += ": " + l.Error
		}
		sec := int64(l.TS)
		t := time.Unix(sec, int64((l.TS-float64(sec))*1e9)).UTC()
		if prev, ok := errs[l.Identifier]; !ok || t.After(prev.Time) {
			errs[l.Identifier] = LogError{Time: t, Message: msg}
		}
	}
	return errs
}
