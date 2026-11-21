package radius

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// State is the box's RADIUS state (not configuration: it stays out of
// backups and config history): per-device Wi-Fi passwords (PPSK), the
// client certificates issued for EAP-TLS, and the server certificate.
type State struct {
	Path string // data_dir/radius/state.json, mode 0600
	// RootCert and RootKey are the internal CA's root (PEM files).
	RootCert, RootKey string
	Now               func() time.Time

	mu     sync.Mutex
	loaded bool
	data   stateData
}

type stateData struct {
	PPSK   map[string]ppskEntry `json:"ppsk,omitempty"`
	Certs  []IssuedCert         `json:"certs,omitempty"`
	Server *serverCert          `json:"server,omitempty"`
}

type ppskEntry struct {
	PSK string    `json:"psk"`
	Set time.Time `json:"set"`
}

// IssuedCert is a client certificate the box issued to a RADIUS user.
type IssuedCert struct {
	User     string     `json:"user"`
	Serial   string     `json:"serial"` // lower-case hex
	Issued   time.Time  `json:"issued"`
	NotAfter time.Time  `json:"not_after"`
	Revoked  *time.Time `json:"revoked,omitempty"`
}

type serverCert struct {
	Cert   string `json:"cert"`
	Key    string `json:"key"`
	CA     string `json:"ca"` // the root it was issued from
	Name   string `json:"name"`
	Expiry time.Time
}

func (s *State) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *State) loadLocked() {
	if s.loaded {
		return
	}
	s.loaded = true
	b, err := os.ReadFile(s.Path)
	if err == nil {
		_ = json.Unmarshal(b, &s.data)
	}
}

func (s *State) saveLocked() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

// PPSK returns a device's Wi-Fi password ("" = none).
func (s *State) PPSK(mac string) string {
	mac = macOf(mac)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	return s.data.PPSK[mac].PSK
}

// PPSKEntry lists a device that has a Wi-Fi password of its own.
type PPSKEntry struct {
	MAC string    `json:"mac"`
	PSK string    `json:"psk"`
	Set time.Time `json:"set"`
}

// PPSKs lists the per-device Wi-Fi passwords.
func (s *State) PPSKs() []PPSKEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	out := make([]PPSKEntry, 0, len(s.data.PPSK))
	for mac, e := range s.data.PPSK {
		out = append(out, PPSKEntry{MAC: mac, PSK: e.PSK, Set: e.Set})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MAC < out[j].MAC })
	return out
}

// ErrBadPSK: a WPA2 passphrase is 8-63 printable ASCII characters.
var ErrBadPSK = errors.New("want a Wi-Fi password of 8-63 printable characters")

// MaxPPSK is how many devices may have a Wi-Fi password of their own.
const MaxPPSK = 4096

// SetPPSK sets ("" removes) a device's Wi-Fi password.
func (s *State) SetPPSK(mac, psk string) error {
	mac = macOf(mac)
	if mac == "" {
		return errors.New("invalid MAC address")
	}
	if psk != "" {
		if len(psk) < 8 || len(psk) > 63 {
			return ErrBadPSK
		}
		for _, r := range psk {
			if r < 0x20 || r > 0x7e {
				return ErrBadPSK
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	if psk == "" {
		if _, ok := s.data.PPSK[mac]; !ok {
			return nil
		}
		delete(s.data.PPSK, mac)
		return s.saveLocked()
	}
	if s.data.PPSK == nil {
		s.data.PPSK = map[string]ppskEntry{}
	}
	if _, ok := s.data.PPSK[mac]; !ok && len(s.data.PPSK) >= MaxPPSK {
		return fmt.Errorf("at most %d devices may have a Wi-Fi password of their own", MaxPPSK)
	}
	s.data.PPSK[mac] = ppskEntry{PSK: psk, Set: s.now().UTC()}
	return s.saveLocked()
}

func normSerial(s string) string {
	s = strings.ToLower(strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			return r
		}
		return -1
	}, s))
	return strings.TrimLeft(s, "0")
}

// CertValid: the certificate (by serial) was issued by this box to user and
// is not revoked.
func (s *State) CertValid(user, serial string) bool {
	serial = normSerial(serial)
	if serial == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	for _, c := range s.data.Certs {
		if normSerial(c.Serial) == serial {
			return strings.EqualFold(c.User, user) && c.Revoked == nil
		}
	}
	return false
}

// Certs lists the client certificates issued (all users when user is "").
func (s *State) Certs(user string) []IssuedCert {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	out := []IssuedCert{}
	for _, c := range s.data.Certs {
		if user == "" || strings.EqualFold(c.User, user) {
			out = append(out, c)
		}
	}
	return out
}

// RevokeCerts revokes a user's certificates (serial "" = all of them) and
// returns how many.
func (s *State) RevokeCerts(user, serial string) (int, error) {
	serial = normSerial(serial)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	now := s.now().UTC()
	n := 0
	for i := range s.data.Certs {
		c := &s.data.Certs[i]
		if strings.EqualFold(c.User, user) && c.Revoked == nil && (serial == "" || normSerial(c.Serial) == serial) {
			c.Revoked = &now
			n++
		}
	}
	if n == 0 {
		return 0, nil
	}
	return n, s.saveLocked()
}

// ErrNoCA: the internal CA has not made its root yet.
var ErrNoCA = errors.New("the internal CA has no root certificate yet: commit the internal CA first, wait a few seconds, then turn on 802.1X")

func (s *State) root() (*x509.Certificate, crypto.Signer, []byte, error) {
	cb, err := os.ReadFile(s.RootCert)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil, ErrNoCA
	}
	if err != nil {
		return nil, nil, nil, err
	}
	kb, err := os.ReadFile(s.RootKey)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil, ErrNoCA
	}
	if err != nil {
		return nil, nil, nil, err
	}
	blk, _ := pem.Decode(cb)
	if blk == nil {
		return nil, nil, nil, errors.New("internal CA: bad root certificate")
	}
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("internal CA: %w", err)
	}
	key, err := parseKey(kb)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("internal CA: %w", err)
	}
	return cert, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), nil
}

func parseKey(b []byte) (crypto.Signer, error) {
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, errors.New("bad root key")
	}
	if k, err := x509.ParsePKCS8PrivateKey(blk.Bytes); err == nil {
		if sg, ok := k.(crypto.Signer); ok {
			return sg, nil
		}
	}
	if k, err := x509.ParseECPrivateKey(blk.Bytes); err == nil {
		return k, nil
	}
	if k, err := x509.ParsePKCS1PrivateKey(blk.Bytes); err == nil {
		return k, nil
	}
	return nil, errors.New("unsupported root key")
}

func (s *State) issue(ca *x509.Certificate, caKey crypto.Signer, tmpl *x509.Certificate) (certPEM, keyPEM []byte, serial string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, "", err
	}
	sn, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, nil, "", err
	}
	sn.Add(sn, big.NewInt(1))
	tmpl.SerialNumber = sn
	now := s.now()
	tmpl.NotBefore = now.Add(-5 * time.Minute)
	if tmpl.NotAfter.After(ca.NotAfter) {
		tmpl.NotAfter = ca.NotAfter
	}
	tmpl.KeyUsage = x509.KeyUsageDigitalSignature
	tmpl.BasicConstraintsValid = true
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, "", err
	}
	kd, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, "", err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd}),
		hex.EncodeToString(sn.Bytes()), nil
}

// ServerCert returns the 802.1X server certificate for name (the box's
// host name), issuing a new one when there is none, it was issued for
// another name or from another root, or it expires within 30 days. It is
// valid for two years.
func (s *State) ServerCert(name string) (cert, key, ca []byte, err error) {
	root, rootKey, rootPEM, err := s.root()
	if err != nil {
		return nil, nil, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	now := s.now()
	if sc := s.data.Server; sc != nil && sc.Name == name && bytes.Equal([]byte(sc.CA), rootPEM) && sc.Expiry.After(now.Add(30*24*time.Hour)) {
		return []byte(sc.Cert), []byte(sc.Key), rootPEM, nil
	}
	tmpl := &x509.Certificate{
		Subject:     pkix.Name{CommonName: name},
		DNSNames:    []string{name},
		NotAfter:    now.Add(2 * 365 * 24 * time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	c, k, _, err := s.issue(root, rootKey, tmpl)
	if err != nil {
		return nil, nil, nil, err
	}
	s.data.Server = &serverCert{Cert: string(c), Key: string(k), CA: string(rootPEM), Name: name, Expiry: tmpl.NotAfter}
	if err := s.saveLocked(); err != nil {
		return nil, nil, nil, err
	}
	return c, k, rootPEM, nil
}

// ClientCert is a certificate issued to a user for EAP-TLS (the key is not
// kept by the box).
type ClientCert struct {
	User        string    `json:"user"`
	Serial      string    `json:"serial"`
	NotAfter    time.Time `json:"not_after"`
	Certificate string    `json:"certificate"` // PEM
	Key         string    `json:"key"`         // PEM (PKCS#8)
	CA          string    `json:"ca"`          // PEM: the root to trust
	Fingerprint string    `json:"ca_sha256"`
}

// MaxCertDays is the longest a client certificate may be valid.
const MaxCertDays = 3650

// IssueClient issues a client certificate (common name = user).
func (s *State) IssueClient(user string, days int) (ClientCert, error) {
	if days <= 0 {
		days = 365
	}
	if days > MaxCertDays {
		return ClientCert{}, fmt.Errorf("want at most %d days", MaxCertDays)
	}
	root, rootKey, rootPEM, err := s.root()
	if err != nil {
		return ClientCert{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	now := s.now()
	tmpl := &x509.Certificate{
		Subject:     pkix.Name{CommonName: user},
		NotAfter:    now.Add(time.Duration(days) * 24 * time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	c, k, serial, err := s.issue(root, rootKey, tmpl)
	if err != nil {
		return ClientCert{}, err
	}
	s.data.Certs = append(s.data.Certs, IssuedCert{User: user, Serial: serial, Issued: now.UTC(), NotAfter: tmpl.NotAfter.UTC()})
	// forget certificates long expired
	keep := s.data.Certs[:0]
	for _, ic := range s.data.Certs {
		if ic.NotAfter.After(now.Add(-90 * 24 * time.Hour)) {
			keep = append(keep, ic)
		}
	}
	s.data.Certs = keep
	if err := s.saveLocked(); err != nil {
		return ClientCert{}, err
	}
	sum := sha256.Sum256(root.Raw)
	return ClientCert{User: user, Serial: serial, NotAfter: tmpl.NotAfter.UTC(), Certificate: string(c), Key: string(k), CA: string(rootPEM), Fingerprint: hex.EncodeToString(sum[:])}, nil
}
