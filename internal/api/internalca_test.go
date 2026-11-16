package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInternalCA(t *testing.T) {
	a := newAuthAPI(t)
	a.server.InternalCARoot = filepath.Join(a.dir, "root.crt")
	get := func(path string) (int, string, http.Header) {
		resp, err := http.Get(a.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Header
	}
	// off: no root
	if code, _, _ := get("/api/v1/internal-ca/root.crt"); code != 404 {
		t.Errorf("root while off: %d", code)
	}
	cfg, _ := a.store.Running()
	cfg.System.Hostname = "gate"
	cfg.InternalCA.Enabled, cfg.InternalCA.ACME = true, true
	if _, err := a.store.SaveRevision(cfg, "test", "ca"); err != nil {
		t.Fatal(err)
	}
	b := a.browser()
	b.login("viewer")
	st := b.expect("GET", "/api/v1/internal-ca", nil, 200)
	if st["enabled"] != true || st["ready"] != false || st["lifetime_hours"] != float64(48) {
		t.Errorf("before the root exists: %v", st)
	}
	dirs, _ := st["acme_directories"].([]any)
	if len(dirs) == 0 || !strings.HasPrefix(dirs[0].(string), "https://gate.") || !strings.HasSuffix(dirs[0].(string), ":9443/acme/local/directory") {
		t.Errorf("directories %v", dirs)
	}
	if code, _, _ := get("/api/v1/internal-ca/root.crt"); code != 404 {
		t.Errorf("root before Caddy made it: %d", code)
	}

	// Caddy made the root
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Goblin Gate Local CA Root"},
		NotBefore: time.Now(), NotAfter: time.Now().Add(10 * 365 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	os.WriteFile(a.server.InternalCARoot, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)

	st = b.expect("GET", "/api/v1/internal-ca", nil, 200)
	root, _ := st["root"].(map[string]any)
	if st["ready"] != true || root["subject"] != "Goblin Gate Local CA Root" || len(root["sha256"].(string)) != 95 {
		t.Errorf("with the root: %v", st)
	}
	// the download needs no login
	code, body, h := get("/api/v1/internal-ca/root.crt")
	if code != 200 || !strings.HasPrefix(body, "-----BEGIN CERTIFICATE-----") || h.Get("Content-Type") != "application/x-pem-file" {
		t.Errorf("pem: %d %q %v", code, body, h)
	}
	code, body, h = get("/api/v1/internal-ca/root.crt?format=der")
	if code != 200 || body != string(der) || h.Get("Content-Type") != "application/x-x509-ca-cert" {
		t.Errorf("der: %d %v", code, h)
	}
	if code, _, _ := get("/api/v1/internal-ca/root.crt?format=p12"); code != 400 {
		t.Errorf("bad format: %d", code)
	}
	// the status does need a login
	if code, _, _ := get("/api/v1/internal-ca"); code != 401 {
		t.Errorf("status without login: %d", code)
	}
}
