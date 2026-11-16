package homelab

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"
)

// testPKI is a CA with a server certificate for 127.0.0.1 and a client
// certificate, as PEM.
type testPKI struct {
	CA, ServerCert, ServerKey, ClientCert, ClientKey string
	pool                                             *x509.CertPool
}

func newPKI(t *testing.T) testPKI {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	issue := func(serial int64, usage x509.ExtKeyUsage, ip bool) (string, string) {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tmpl := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "test"},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
		if ip {
			tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		kb, _ := x509.MarshalECPrivateKey(key)
		return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
			string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}))
	}
	p := testPKI{CA: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))}
	p.ServerCert, p.ServerKey = issue(2, x509.ExtKeyUsageServerAuth, true)
	p.ClientCert, p.ClientKey = issue(3, x509.ExtKeyUsageClientAuth, false)
	p.pool = x509.NewCertPool()
	p.pool.AddCert(ca)
	return p
}

// serverTLS is a server config that requires a client certificate from the CA.
func (p testPKI) serverTLS(t *testing.T, requireClient bool) *tls.Config {
	pair, err := tls.X509KeyPair([]byte(p.ServerCert), []byte(p.ServerKey))
	if err != nil {
		t.Fatal(err)
	}
	c := &tls.Config{Certificates: []tls.Certificate{pair}}
	if requireClient {
		c.ClientAuth, c.ClientCAs = tls.RequireAndVerifyClientCert, p.pool
	}
	return c
}
