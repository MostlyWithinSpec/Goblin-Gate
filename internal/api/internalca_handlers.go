package api

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/render/caddy"
)

// InternalCAStatus is GET /api/v1/internal-ca: the internal CA as the
// running config has it, and its root certificate once Caddy made it.
type InternalCAStatus struct {
	Enabled bool `json:"enabled"`
	ACME    bool `json:"acme"`
	// Ready: the root certificate exists (Caddy creates it when it first
	// starts with the CA enabled).
	Ready bool             `json:"ready"`
	Root  *RootCertificate `json:"root,omitempty"`
	// ACMEDirectories are the directory URLs for ACME clients on the LAN.
	ACMEDirectories []string `json:"acme_directories"`
	LifetimeHours   int      `json:"lifetime_hours"`
	// InternalHosts are the reverse proxy names with internal certificates.
	InternalHosts []string `json:"internal_hosts"`
}

// RootCertificate describes the root for people checking what they trust.
type RootCertificate struct {
	Subject   string    `json:"subject"`
	SHA256    string    `json:"sha256"` // AB:CD:..., as the OS trust dialogs show it
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`
}

func (s *Server) caRootPath() string {
	if s.InternalCARoot != "" {
		return s.InternalCARoot
	}
	return caddy.RootPath
}

// readRoot returns the root certificate, or (nil, nil) when there is none
// yet.
func (s *Server) readRoot() (*x509.Certificate, error) {
	data, err := os.ReadFile(s.caRootPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	b, _ := pem.Decode(data)
	if b == nil || b.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%s: no PEM certificate", s.caRootPath())
	}
	return x509.ParseCertificate(b.Bytes)
}

func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

// GET /api/v1/internal-ca → InternalCAStatus.
func (s *Server) internalCAStatus(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	ca := cfg.InternalCA
	st := InternalCAStatus{Enabled: ca.Enabled, ACME: ca.Enabled && ca.ACME, ACMEDirectories: caddy.ACMEDirectories(cfg),
		LifetimeHours: ca.Lifetime(), InternalHosts: caddy.InternalDomains(cfg)}
	if st.ACMEDirectories == nil {
		st.ACMEDirectories = []string{}
	}
	if st.InternalHosts == nil {
		st.InternalHosts = []string{}
	}
	if ca.Enabled {
		root, err := s.readRoot()
		if err != nil {
			s.internal(w, "read the internal CA root", err)
			return
		}
		if root != nil {
			st.Ready = true
			st.Root = &RootCertificate{Subject: root.Subject.CommonName, SHA256: fingerprint(root.Raw), NotBefore: root.NotBefore, NotAfter: root.NotAfter}
		}
	}
	writeJSON(w, http.StatusOK, st)
}

// GET /api/v1/internal-ca/root.crt[?format=der] → the root certificate, PEM
// by default. Open without login: a root certificate is public, and a phone
// fetches it before it trusts anything. 404 while the CA is off or has no
// root yet.
func (s *Server) internalCARoot(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	format := r.URL.Query().Get("format")
	if format != "" && format != "pem" && format != "der" {
		writeError(w, http.StatusBadRequest, "invalid_request", "format: pem or der", nil)
		return
	}
	var root *x509.Certificate
	if cfg.InternalCA.Enabled {
		if root, err = s.readRoot(); err != nil {
			s.internal(w, "read the internal CA root", err)
			return
		}
	}
	if root == nil {
		writeError(w, http.StatusNotFound, "not_found", "the internal CA is off or has not made its root certificate yet", nil)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if format == "der" {
		// the type phones offer to install
		w.Header().Set("Content-Type", "application/x-x509-ca-cert")
		w.Header().Set("Content-Disposition", `attachment; filename="goblin-gate-root-ca.crt"`)
		w.Write(root.Raw)
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", `attachment; filename="goblin-gate-root-ca.pem"`)
	w.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: root.Raw}))
}
