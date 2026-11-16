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
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/acme"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/caddy"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/nft"
)

// TestInternalCAE2E runs the internal CA with Debian's caddy and the
// rendered ruleset in network namespaces (inet 5.5.5.1, gw 5.5.5.10 /
// 192.168.1.1, cl 192.168.1.10 + .50):
//
//   - Caddy makes the root; an ACME client on the LAN (this test binary in
//     cl) gets a certificate for client.home.lan with an HTTP challenge, and
//     it chains to that root with the configured lifetime
//   - the ACME directory is closed to the WAN
//   - a reverse proxy host with an internal certificate (nas.home.lan) is
//     reached from the LAN through InternalProxyAddress with a certificate
//     the root vouches for, and refused from the WAN
//
// scripts/linux-proxy-test.ps1 runs it with GOBLIN_PROXY_E2E=1.
func TestInternalCAE2E(t *testing.T) {
	if os.Getenv("GOBLIN_PROXY_E2E") != "1" {
		t.Skip("set GOBLIN_PROXY_E2E=1 (root, nft, caddy, curl)")
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

	// Caddy validates the ACME client's name through /etc/hosts (the box's
	// DNS on a real gate)
	hosts, _ := os.ReadFile("/etc/hosts")
	if err := os.WriteFile("/etc/hosts", append(hosts, []byte("\n192.168.1.50 client.home.lan\n")...), 0o644); err != nil {
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
	background("upstream", "cl", []string{"GOBLIN_PROXY_UPSTREAM=1"}, os.Args[0], "-test.run", "^TestProxyE2EUpstream$")

	cfg := config.Default()
	cfg.AllowNoAdmin = true
	cfg.System.Hostname = "gate"
	cfg.DNS.LocalDomain = "home.lan"
	cfg.InternalCA = config.InternalCA{Enabled: true, ACME: true, LifetimeHours: 24}
	cfg.ReverseProxy = config.ReverseProxy{Enabled: true, Hosts: []config.ProxyHost{
		{Name: "nas", Enabled: true, Issuer: "internal", Domains: []string{"nas.home.lan"}, Upstream: "http://192.168.1.10:8096"},
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

	cfiles, err := caddy.Renderer{}.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	storage := filepath.Join(dir, "storage")
	var doc map[string]any
	json.Unmarshal(cfiles[caddy.ConfPath], &doc)
	doc["storage"].(map[string]any)["root"] = storage
	cjson, _ := json.Marshal(doc)
	os.WriteFile(filepath.Join(dir, "caddy.json"), cjson, 0o644)
	caddyBin, err := exec.LookPath("caddy")
	if err != nil {
		t.Fatal(err)
	}
	background("caddy", "gw", []string{"HOME=" + dir}, caddyBin, "run", "--config", filepath.Join(dir, "caddy.json"))

	// the root, where goblind's API reads it
	rootPath := filepath.Join(storage, strings.TrimPrefix(caddy.RootPath, caddy.StorageDir+"/"))
	var root *x509.Certificate
	for deadline := time.Now().Add(30 * time.Second); root == nil; time.Sleep(time.Second) {
		if b, err := os.ReadFile(rootPath); err == nil {
			if blk, _ := pem.Decode(b); blk != nil {
				root, _ = x509.ParseCertificate(blk.Bytes)
			}
		}
		if root == nil && time.Now().After(deadline) {
			t.Fatalf("no root at %s", rootPath)
		}
	}
	t.Logf("root: %q, valid until %s", root.Subject.CommonName, root.NotAfter.Format(time.RFC3339))
	if root.Subject.CommonName != "Goblin Gate Local CA Root" || !root.IsCA {
		t.Errorf("root %+v", root.Subject)
	}
	pool := x509.NewCertPool()
	pool.AddCert(root)

	// an ACME client on the LAN gets a certificate
	certPath := filepath.Join(dir, "client.pem")
	client := exec.Command("ip", "netns", "exec", "cl", os.Args[0], "-test.run", "^TestInternalCAE2EClient$", "-test.v")
	client.Env = append(os.Environ(), "GOBLIN_CA_DIR="+dir, "GOBLIN_CA_ROOT="+rootPath)
	out, _ := client.CombinedOutput()
	cmdOut := string(out)
	if !strings.Contains(cmdOut, "--- PASS: TestInternalCAE2EClient") {
		t.Fatalf("ACME client failed:\n%s", cmdOut)
	}
	b, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("no certificate written: %v\n%s", err, cmdOut)
	}
	var chain []*x509.Certificate
	for rest := b; ; {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			break
		}
		c, err := x509.ParseCertificate(blk.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		chain = append(chain, c)
	}
	leaf := chain[0]
	inter := x509.NewCertPool()
	for _, c := range chain[1:] {
		inter.AddCert(c)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: "client.home.lan", Roots: pool, Intermediates: inter}); err != nil {
		t.Errorf("ACME certificate does not chain to the root: %v", err)
	}
	// Caddy 2.6 cannot set the ACME lifetime: its default, which is short
	if life := leaf.NotAfter.Sub(leaf.NotBefore); life > 7*24*time.Hour || life < time.Hour {
		t.Errorf("lifetime %s", life)
	}
	t.Logf("ACME certificate for %v, valid %s", leaf.DNSNames, leaf.NotAfter.Sub(leaf.NotBefore))

	// the directory is closed to the WAN
	if out, err := exec.Command("ip", "netns", "exec", "inet", "curl", "-sk", "--max-time", "5", "https://5.5.5.10:9443/acme/local/directory").CombinedOutput(); err == nil {
		t.Errorf("ACME directory reachable from the WAN: %s", out)
	}

	// the internal proxy host from the LAN, through InternalProxyAddress
	os.WriteFile(filepath.Join(dir, "root.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: root.Raw}), 0o644)
	curl := func(ns, addr string, extra ...string) string {
		args := append([]string{"netns", "exec", ns, "curl", "-s", "--max-time", "10", "--cacert", filepath.Join(dir, "root.pem"),
			"--resolve", "nas.home.lan:443:" + addr, "-w", " status=%{http_code}"}, extra...)
		out, _ := exec.Command("ip", append(args, "https://nas.home.lan/x")...).CombinedOutput()
		return string(out)
	}
	var lan string
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(time.Second) {
		if lan = curl("cl", config.InternalProxyAddress.String()); strings.Contains(lan, "status=200") || time.Now().After(deadline) {
			break
		}
	}
	if !strings.Contains(lan, "proto=http path=/x xff=192.168.1.") || !strings.Contains(lan, "status=200") {
		t.Errorf("nas.home.lan from the LAN: %q", lan)
	}
	// from the WAN: the name does not resolve there, and the host is for the LAN
	if wan := curl("inet", "5.5.5.10", "-k"); !strings.Contains(wan, "status=403") {
		t.Errorf("nas.home.lan from the WAN: %q", wan)
	}
}

// TestInternalCAE2EClient is the ACME client TestInternalCAE2E runs on the
// LAN: it answers the HTTP challenge on 192.168.1.50:80 and writes the
// certificate chain to client.pem next to the root.
func TestInternalCAE2EClient(t *testing.T) {
	if os.Getenv("GOBLIN_CA_DIR") == "" {
		t.Skip("started by TestInternalCAE2E")
	}
	b, _ := os.ReadFile(os.Getenv("GOBLIN_CA_ROOT"))
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(b)
	httpc := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := &acme.Client{Key: key, DirectoryURL: "https://192.168.1.1:9443/acme/local/directory", HTTPClient: httpc}
	var dirErr error
	for i := 0; i < 20; i++ { // Caddy may still be getting its own certificate
		if _, dirErr = c.Discover(ctx); dirErr == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if dirErr != nil {
		t.Fatalf("directory: %v", dirErr)
	}
	if _, err := c.Register(ctx, &acme.Account{}, acme.AcceptTOS); err != nil {
		t.Fatalf("register: %v", err)
	}
	order, err := c.AuthorizeOrder(ctx, acme.DomainIDs("client.home.lan"))
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	tokens := map[string]string{}
	srv := &http.Server{Addr: "192.168.1.50:80", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v, ok := tokens[strings.TrimPrefix(r.URL.Path, "/.well-known/acme-challenge/")]; ok {
			fmt.Fprint(w, v)
			return
		}
		http.NotFound(w, r)
	})}
	go srv.ListenAndServe()
	defer srv.Close()
	for _, u := range order.AuthzURLs {
		z, err := c.GetAuthorization(ctx, u)
		if err != nil {
			t.Fatal(err)
		}
		var chal *acme.Challenge
		for _, ch := range z.Challenges {
			if ch.Type == "http-01" {
				chal = ch
			}
		}
		if chal == nil {
			t.Fatalf("no http-01 challenge: %+v", z.Challenges)
		}
		resp, err := c.HTTP01ChallengeResponse(chal.Token)
		if err != nil {
			t.Fatal(err)
		}
		tokens[chal.Token] = resp
		if _, err := c.Accept(ctx, chal); err != nil {
			t.Fatalf("accept: %v", err)
		}
		if _, err := c.WaitAuthorization(ctx, u); err != nil {
			t.Fatalf("authorization: %v", err)
		}
	}
	if order, err = c.WaitOrder(ctx, order.URI); err != nil {
		t.Fatalf("order: %v", err)
	}
	ckey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	csr, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "client.home.lan"}, DNSNames: []string{"client.home.lan"}}, ckey)
	ders, _, err := c.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	var out []byte
	for _, d := range ders {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d})...)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("GOBLIN_CA_DIR"), "client.pem"), out, 0o644); err != nil {
		t.Fatal(err)
	}
}
