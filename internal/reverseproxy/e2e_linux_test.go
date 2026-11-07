//go:build linux

package reverseproxy_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/caddy"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/nft"
	"github.com/networkgoblinlabs/goblin-gate/internal/reverseproxy"
)

// TestProxyE2E runs the rendered ruleset and Caddy config (Debian's caddy)
// against Pebble, Let's Encrypt's test CA, in three network namespaces:
//
//	inet  5.5.5.1     Pebble and an outside client
//	gw    eth0 5.5.5.10 (WAN), eth1 192.168.1.1 (LAN): nft + caddy
//	cl    192.168.1.10 the upstream services, 192.168.1.50 a LAN client
//
// Pebble validates through the WAN redirects (port 80/443 to Caddy's high
// ports), so issuing proves the alternate-port challenges. Then: HTTPS to the
// upstream (plain and TLS with a self-signed certificate), the redirect to
// HTTPS, the allow-list (403 from the internet, 200 from the LAN through the
// hairpin redirect), the box's own LAN address not redirected, and the status
// reader finding the certificates. Needs root, nft, caddy, curl and pebble:
// scripts/linux-proxy-test.ps1 runs it with GOBLIN_PROXY_E2E=1.
func TestProxyE2E(t *testing.T) {
	if os.Getenv("GOBLIN_PROXY_E2E") != "1" {
		t.Skip("set GOBLIN_PROXY_E2E=1 (root, nft, caddy, curl, pebble)")
	}
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	in := func(ns string, args ...string) string {
		t.Helper()
		return run(append([]string{"ip", "netns", "exec", ns}, args...)...)
	}
	for _, ns := range []string{"inet", "gw", "cl"} {
		exec.Command("ip", "netns", "del", ns).Run()
		run("ip", "netns", "add", ns)
		in(ns, "ip", "link", "set", "lo", "up")
		t.Cleanup(func() { exec.Command("ip", "netns", "del", ns).Run() })
	}
	in("gw", "ip", "link", "add", "eth0", "type", "veth", "peer", "name", "up0", "netns", "inet")
	in("gw", "ip", "link", "add", "eth1", "type", "veth", "peer", "name", "c0", "netns", "cl")
	in("gw", "ip", "addr", "add", "5.5.5.10/24", "dev", "eth0")
	in("gw", "ip", "addr", "add", "192.168.1.1/24", "dev", "eth1")
	in("gw", "ip", "link", "set", "eth0", "up")
	in("gw", "ip", "link", "set", "eth1", "up")
	in("gw", "ip", "route", "add", "default", "via", "5.5.5.1")
	in("gw", "sysctl", "-qw", "net.ipv4.ip_forward=1")
	in("inet", "ip", "addr", "add", "5.5.5.1/24", "dev", "up0")
	in("inet", "ip", "link", "set", "up0", "up")
	in("cl", "ip", "addr", "add", "192.168.1.10/24", "dev", "c0")
	in("cl", "ip", "addr", "add", "192.168.1.50/24", "dev", "c0")
	in("cl", "ip", "link", "set", "c0", "up")
	in("cl", "ip", "route", "add", "default", "via", "192.168.1.1")

	// names: Pebble and Caddy resolve through /etc/hosts
	hosts, _ := os.ReadFile("/etc/hosts")
	if err := os.WriteFile("/etc/hosts", append(hosts, []byte("\n5.5.5.1 pebble\n5.5.5.10 media.example.com nas.example.com\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.WriteFile("/etc/hosts", hosts, 0o644) })

	dir := t.TempDir()
	logs := map[string]string{}
	background := func(name, ns string, env []string, args ...string) {
		t.Helper()
		p := filepath.Join(dir, name+".log")
		logs[name] = p
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("ip", append([]string{"netns", "exec", ns}, args...)...)
		cmd.Env = append(os.Environ(), env...)
		cmd.Stdout, cmd.Stderr = f, f
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait(); f.Close() })
	}
	t.Cleanup(func() {
		if t.Failed() {
			for name, p := range logs {
				b, _ := os.ReadFile(p)
				if len(b) > 6000 {
					b = b[len(b)-6000:]
				}
				t.Logf("--- %s log:\n%s", name, b)
			}
		}
	})

	// Pebble with its own server certificate (for "pebble") from a test CA,
	// validating on the real ports 80 and 443
	caPEM := writePebbleCerts(t, dir)
	pcfg := fmt.Sprintf(`{"pebble":{"listenAddress":"0.0.0.0:14000","managementListenAddress":"0.0.0.0:15000",
	  "certificate":%q,"privateKey":%q,"httpPort":80,"tlsPort":443,"ocspResponderURL":""}}`,
		filepath.Join(dir, "pebble.crt"), filepath.Join(dir, "pebble.key"))
	os.WriteFile(filepath.Join(dir, "pebble.json"), []byte(pcfg), 0o644)
	pebble, err := exec.LookPath("pebble")
	if err != nil {
		t.Fatal(err)
	}
	background("pebble", "inet", []string{"PEBBLE_VA_NOSLEEP=1", "PEBBLE_WFE_NONCEREJECT=0"}, pebble, "-config", filepath.Join(dir, "pebble.json"))

	// the upstreams: this test binary as an HTTP (8096) and HTTPS (8443) server
	background("upstream", "cl", []string{"GOBLIN_PROXY_UPSTREAM=1"}, os.Args[0], "-test.run", "^TestProxyE2EUpstream$")

	cfg := config.Default()
	cfg.AllowNoAdmin = true
	cfg.ReverseProxy = config.ReverseProxy{Enabled: true, Email: "admin@example.com", Hosts: []config.ProxyHost{
		{Name: "jellyfin", Enabled: true, Domains: []string{"media.example.com"}, Upstream: "http://192.168.1.10:8096"},
		{Name: "nas", Enabled: true, Domains: []string{"nas.example.com"}, Upstream: "https://192.168.1.10:8443", InsecureSkipVerify: true, Allow: []string{"192.168.1.0/24"}},
	}}
	if errs := config.Validate(cfg); len(errs) > 0 {
		t.Fatalf("fixture invalid: %v", errs)
	}
	files, err := nft.Renderer{}.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "goblin.nft"), files[nft.Path], 0o644)
	in("gw", "nft", "-f", filepath.Join(dir, "goblin.nft"))

	// the rendered Caddy config, pointed at Pebble instead of Let's Encrypt
	cfiles, err := caddy.Renderer{}.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	storage := filepath.Join(dir, "storage")
	os.WriteFile(filepath.Join(dir, "ca.pem"), caPEM, 0o644)
	cjson := pointAtPebble(t, cfiles[caddy.ConfPath], storage, filepath.Join(dir, "ca.pem"))
	os.WriteFile(filepath.Join(dir, "caddy.json"), cjson, 0o644)
	caddyBin, err := exec.LookPath("caddy")
	if err != nil {
		t.Fatal(err)
	}
	background("caddy", "gw", []string{"HOME=" + dir}, caddyBin, "run", "--config", filepath.Join(dir, "caddy.json"))

	// the certificates, as the status reader sees them
	reader := &reverseproxy.Reader{StorageDir: storage, Run: func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if name == "journalctl" {
			return os.ReadFile(logs["caddy"]) // Caddy's JSON log, as journalctl -o cat prints it
		}
		return []byte("active\n"), nil
	}}
	var st reverseproxy.Status
	for deadline := time.Now().Add(120 * time.Second); ; time.Sleep(2 * time.Second) {
		st = reader.Status(context.Background(), cfg)
		if st.Hosts[0].Domains[0].Certificate != nil && st.Hosts[1].Domains[0].Certificate != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no certificates after 120s: %+v", st)
		}
	}
	for _, h := range st.Hosts {
		c := h.Domains[0].Certificate
		t.Logf("%s: issued by %q, valid until %s", h.Domains[0].Domain, c.Issuer, c.NotAfter.Format(time.RFC3339))
		if !strings.Contains(c.Issuer, "Pebble") || h.Domains[0].Error != nil {
			t.Errorf("%s: %+v %+v", h.Name, c, h.Domains[0].Error)
		}
	}
	// Pebble's issuing root, to check the served chain like a browser would
	root := in("inet", "curl", "-sf", "--cacert", filepath.Join(dir, "ca.pem"), "https://pebble:15000/roots/0")
	os.WriteFile(filepath.Join(dir, "root.pem"), []byte(root), 0o644)
	curl := func(ns string, args ...string) (string, error) {
		a := append([]string{"netns", "exec", ns, "curl", "-s", "--max-time", "10", "--cacert", filepath.Join(dir, "root.pem"),
			"--resolve", "media.example.com:443:5.5.5.10", "--resolve", "nas.example.com:443:5.5.5.10",
			"--resolve", "media.example.com:80:5.5.5.10", "-w", " status=%{http_code} location=%{redirect_url}"}, args...)
		out, err := exec.Command("ip", a...).CombinedOutput()
		return string(out), err
	}
	expect := func(ns, url string, want ...string) {
		t.Helper()
		out, err := curl(ns, url)
		for _, w := range want {
			if !strings.Contains(out, w) {
				t.Errorf("%s from %s: %q (%v), want %q", url, ns, out, err, w)
			}
		}
	}
	// from the internet
	expect("inet", "https://media.example.com/x", "proto=http path=/x xff=5.5.5.1", "status=200")
	expect("inet", "http://media.example.com/x?y=1", "status=308", "location=https://media.example.com/x?y=1")
	expect("inet", "https://nas.example.com/", "status=403")
	// from the LAN, opening the public address (hairpin): allowed onto the NAS
	expect("cl", "https://nas.example.com/", "proto=https path=/ xff=192.168.1.", "status=200")
	expect("cl", "https://media.example.com/", "status=200")
	// the box's own LAN address is not sent to the proxy
	if out, err := curl("cl", "-k", "https://192.168.1.1/"); err == nil || strings.Contains(out, "status=200") {
		t.Errorf("https://192.168.1.1 reached something: %q", out)
	}
	// Caddy's high ports are not reachable directly, only through the redirect
	if out, err := curl("inet", "-k", "https://5.5.5.10:10443/"); err == nil {
		t.Errorf("port 10443 open from the WAN: %q", out)
	}
}

// TestProxyE2EUpstream is the upstream TestProxyE2E starts in the client
// namespace: it answers with what it received and runs until killed.
func TestProxyE2EUpstream(t *testing.T) {
	if os.Getenv("GOBLIN_PROXY_UPSTREAM") != "1" {
		t.Skip("started by TestProxyE2E")
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proto := "http"
		if r.TLS != nil {
			proto = "https"
		}
		fmt.Fprintf(w, "proto=%s path=%s xff=%s", proto, r.URL.Path, r.Header.Get("X-Forwarded-For"))
	})
	cert := selfSigned(t, "nas.home.lan")
	go func() {
		srv := &http.Server{Addr: "192.168.1.10:8443", Handler: h, TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}}}
		t.Error(srv.ListenAndServeTLS("", ""))
	}()
	t.Error(http.ListenAndServe("192.168.1.10:8096", h))
}

// pointAtPebble swaps the ACME directory for Pebble's, trusts the test CA
// for it and moves the storage into the test's directory.
func pointAtPebble(t *testing.T, in []byte, storage, ca string) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(in, &doc); err != nil {
		t.Fatal(err)
	}
	doc["storage"].(map[string]any)["root"] = storage
	policies := doc["apps"].(map[string]any)["tls"].(map[string]any)["automation"].(map[string]any)["policies"].([]any)
	for _, p := range policies {
		for _, is := range p.(map[string]any)["issuers"].([]any) {
			is := is.(map[string]any)
			is["ca"] = "https://pebble:14000/dir"
			is["trusted_roots_pem_files"] = []string{ca}
		}
	}
	out, _ := json.Marshal(doc)
	return out
}

func writePebbleCerts(t *testing.T, dir string) []byte {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "goblin test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "pebble"},
		DNSNames: []string{"pebble"}, IPAddresses: []net.IP{net.ParseIP("5.5.5.1")},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	os.WriteFile(filepath.Join(dir, "pebble.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	os.WriteFile(filepath.Join(dir, "pebble.key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
}

func selfSigned(t *testing.T, name string) tls.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
