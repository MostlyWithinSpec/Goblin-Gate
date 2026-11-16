package homelab

import (
	"context"
	"crypto/sha256"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// fakeServer answers GET paths (with query) from testdata files; a value
// starting with "!" is a status code and message instead.
type fakeServer struct {
	mu    sync.Mutex
	files map[string]string
	seen  []string
	check func(r *http.Request) bool
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.seen = append(f.seen, r.URL.RequestURI())
	f.mu.Unlock()
	if f.check != nil && !f.check(r) {
		http.Error(w, "authentication failure", http.StatusUnauthorized)
		return
	}
	v, ok := f.files[r.URL.RequestURI()]
	if !ok {
		http.Error(w, `{"message":"no such route"}`, http.StatusNotImplemented)
		return
	}
	if strings.HasPrefix(v, "!") {
		code, msg, _ := strings.Cut(v[1:], " ")
		w.Header().Set("Content-Type", "application/json")
		switch code {
		case "500":
			w.WriteHeader(500)
		case "403":
			w.WriteHeader(403)
		}
		w.Write([]byte(msg))
		return
	}
	data, err := os.ReadFile(filepath.Join("testdata", v))
	if err != nil {
		panic(err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func names(gs []Guest) []string {
	var out []string
	for _, g := range gs {
		out = append(out, g.Name)
	}
	return out
}

func addrStrings(as []netip.Addr) string {
	var out []string
	for _, a := range as {
		out = append(out, a.String())
	}
	return strings.Join(out, " ")
}

func proxmoxFake(t *testing.T) (*fakeServer, *httptest.Server, testPKI) {
	pki := newPKI(t)
	f := &fakeServer{files: map[string]string{
		"/api2/json/cluster/resources?type=vm":                        "proxmox/cluster-resources.json",
		"/api2/json/nodes/pve1/qemu/100/agent/network-get-interfaces": "proxmox/qemu-100-agent.json",
		"/api2/json/nodes/pve1/qemu/101/agent/network-get-interfaces": `!500 {"data":null,"message":"QEMU guest agent is not running\n"}`,
		"/api2/json/nodes/pve1/qemu/101/config":                       "proxmox/qemu-101-config.json",
		"/api2/json/nodes/pve2/lxc/200/interfaces":                    "proxmox/lxc-200-interfaces.json",
		"/api2/json/nodes/pve2/lxc/201/interfaces":                    "proxmox/lxc-201-interfaces.json",
	}, check: func(r *http.Request) bool {
		return r.Header.Get("Authorization") == "PVEAPIToken=goblin@pve!gate=7b0c-secret"
	}}
	srv := httptest.NewUnstartedServer(f)
	srv.TLS = pki.serverTLS(t, false)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return f, srv, pki
}

func TestProxmoxGuests(t *testing.T) {
	f, srv, pki := proxmoxFake(t)
	s, err := SettingsFrom(map[string]any{"proxmox_url": srv.URL, "proxmox_token_id": "goblin@pve!gate",
		"proxmox_token_secret": "7b0c-secret", "proxmox_ca": pki.CA})
	if err != nil {
		t.Fatal(err)
	}
	gs, err := NewProxmox(*s.Proxmox).Guests(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// running guests only, no templates
	if got := names(gs); !slices.Equal(got, []string{"web-1", "db", "pihole", "web-cache"}) {
		t.Fatalf("guests %v", got)
	}
	web, db, pihole := gs[0], gs[1], gs[2]
	if !slices.Equal(web.Tags, []string{"gg-web", "prod"}) || web.Kind != "qemu" {
		t.Errorf("web-1: %+v", web)
	}
	// the agent's addresses, lo left out (link-local is filtered by the syncer)
	if got := addrStrings(web.Addrs); got != "192.168.20.10 fd00:20::10 fe80::be24:11ff:feaa:bb00 172.17.0.1" {
		t.Errorf("web-1 addresses %s", got)
	}
	if web.MACs != nil {
		t.Error("asked for a config although the agent answered")
	}
	// no agent: the MAC from the config, for the DHCP leases
	if len(db.Addrs) != 0 || !slices.Equal(db.MACs, []string{"bc:24:11:aa:bb:01"}) {
		t.Errorf("db: %+v", db)
	}
	if got := addrStrings(pihole.Addrs); got != "192.168.20.53 fd00:20::53" || pihole.Kind != "lxc" {
		t.Errorf("pihole: %+v", pihole)
	}
	for _, p := range f.seen {
		if strings.Contains(p, "/102/") || strings.Contains(p, "/9000/") {
			t.Errorf("asked about a stopped guest or template: %s", p)
		}
	}
}

func TestProxmoxErrors(t *testing.T) {
	_, srv, pki := proxmoxFake(t)
	ctx := context.Background()
	settings := func(extra map[string]any) ProxmoxSettings {
		m := map[string]any{"proxmox_url": srv.URL, "proxmox_token_id": "goblin@pve!gate", "proxmox_token_secret": "7b0c-secret"}
		for k, v := range extra {
			m[k] = v
		}
		s, err := SettingsFrom(m)
		if err != nil {
			t.Fatal(err)
		}
		return *s.Proxmox
	}
	// a wrong token
	s := settings(map[string]any{"proxmox_ca": pki.CA})
	s.TokenSecret = "wrong"
	if _, err := NewProxmox(s).Guests(ctx); err == nil || !strings.Contains(err.Error(), "proxmox_token_secret") {
		t.Errorf("wrong token: %v", err)
	}
	// the self-signed certificate, pinned by fingerprint
	block, _ := pem.Decode([]byte(pki.ServerCert))
	sum := sha256.Sum256(block.Bytes)
	if _, err := NewProxmox(settings(map[string]any{"proxmox_fingerprint": colonHex(sum[:])})).Guests(ctx); err != nil {
		t.Errorf("pinned: %v", err)
	}
	sum[0] ^= 1
	if _, err := NewProxmox(settings(map[string]any{"proxmox_fingerprint": colonHex(sum[:])})).Guests(ctx); err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Errorf("wrong pin: %v", err)
	}
	// neither: the system's CAs do not know it
	if _, err := NewProxmox(settings(nil)).Guests(ctx); err == nil {
		t.Error("trusted an unknown certificate")
	}
}

func dockerFake(t *testing.T) (*httptest.Server, testPKI) {
	pki := newPKI(t)
	f := &fakeServer{files: map[string]string{
		"/networks":        "docker/networks.json",
		"/containers/json": "docker/containers.json",
	}}
	srv := httptest.NewUnstartedServer(f)
	srv.TLS = pki.serverTLS(t, true)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, pki
}

func TestDockerGuests(t *testing.T) {
	srv, pki := dockerFake(t)
	url := strings.Replace(srv.URL, "https://", "tcp://", 1)
	s, err := SettingsFrom(map[string]any{"docker_url": url, "docker_ca": pki.CA, "docker_cert": pki.ClientCert,
		"docker_key": pki.ClientKey, "docker_host_address": "192.168.20.5"})
	if err != nil {
		t.Fatal(err)
	}
	gs, err := NewDocker(s.Docker[0]).Guests(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, g := range gs {
		got[g.Name] = addrStrings(g.Addrs)
	}
	want := map[string]string{
		"nginx":         "192.168.20.5",              // bridge: behind the host's NAT
		"homeassistant": "192.168.20.70 fd00:20::70", // macvlan: its own address
		"web-batch":     "",                          // no network
		"plex":          "192.168.20.5",              // host network
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s: %q, want %q", name, got[name], w)
		}
	}
	if !slices.Contains(gs[0].Tags, "gg-web") {
		t.Errorf("labels: %v", gs[0].Tags)
	}

	// the host address from the URL when not set
	s.Docker[0].Host = netip.Addr{}
	gs, err = NewDocker(s.Docker[0]).Guests(context.Background())
	if err != nil || addrStrings(gs[0].Addrs) != "127.0.0.1" {
		t.Errorf("host from URL: %v %v", gs, err)
	}

	// without the client certificate dockerd refuses the connection
	s.Docker[0].TLS.Certificates = nil
	if _, err := NewDocker(s.Docker[0]).Guests(context.Background()); err == nil {
		t.Error("connected without a client certificate")
	}
}
