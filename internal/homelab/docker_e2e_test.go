package homelab

import (
	"bytes"
	"context"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestDockerE2E reads a real dockerd over TLS with a client certificate.
// scripts/homelab-docker-test.ps1 starts one (docker:dind) with a bridge
// container "web" (label gg-web) and a macvlan container "iot" at
// 10.99.0.70 (label gg-iot), and sets:
//
//	HOMELAB_DOCKER_URL    tcp://127.0.0.1:<port>
//	HOMELAB_DOCKER_CERTS  a directory with ca.pem, cert.pem, key.pem
func TestDockerE2E(t *testing.T) {
	u, dir := os.Getenv("HOMELAB_DOCKER_URL"), os.Getenv("HOMELAB_DOCKER_CERTS")
	if u == "" || dir == "" {
		t.Skip("HOMELAB_DOCKER_URL and HOMELAB_DOCKER_CERTS not set (scripts/homelab-docker-test.ps1)")
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	s, err := SettingsFrom(map[string]any{"docker_url": u, "docker_ca": read("ca.pem"), "docker_cert": read("cert.pem"),
		"docker_key": read("key.pem"), "docker_host_address": "192.168.20.5"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	gs, err := NewDocker(s.Docker[0]).Guests(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range gs {
		t.Logf("%s %v %v", g.Name, g.Tags, g.Addrs)
	}

	api := &fakeAPI{names: []string{"iot", "web"}, objs: map[string][]netip.Prefix{}}
	var log bytes.Buffer
	sy := &Syncer{API: api, Sources: []Source{NewDocker(s.Docker[0])}, Settings: s, Logger: slog.New(slog.NewTextHandler(&log, nil))}
	if err := sy.Once(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{"iot 10.99.0.70/32", "web 192.168.20.5/32"}
	if !slices.Equal(api.puts, want) {
		t.Fatalf("puts %q, want %q\n%s", api.puts, want, log.String())
	}

	// a client certificate the daemon's CA did not sign is refused
	other := newPKI(t)
	bad, err := SettingsFrom(map[string]any{"docker_url": u, "docker_ca": read("ca.pem"), "docker_cert": other.ClientCert, "docker_key": other.ClientKey})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewDocker(bad.Docker[0]).Guests(ctx); err == nil {
		t.Error("dockerd accepted a foreign client certificate")
	}
}
