package radius

import (
	"errors"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/rendertest"
)

func renderer() Renderer {
	return Renderer{
		Token: func() (string, error) { return "hook-token", nil },
		Certs: func(*config.Config) (Certs, error) {
			return Certs{Cert: []byte("CERT"), Key: []byte("KEY"), CA: []byte("CA")}, nil
		},
	}
}

func TestRender(t *testing.T) {
	files, err := renderer().Render(rendertest.Radius())
	if err != nil {
		t.Fatal(err)
	}
	conf := string(files[ConfPath])
	for _, want := range []string{
		"raddbdir = /run/goblin-radius",
		"client aps {\n\tipaddr = 192.168.1.64/28\n\tsecret = \"ap-secret-123\"",
		"client switch {\n\tipaddr = 192.168.1.2/32\n\tsecret = \"switch-secret-1\"",
		`uri = "${..connect_uri}/mab"`,
		`uri = "${..connect_uri}/inner"`,
		`password = "hook-token"`,
		`connect_uri = "http://127.0.0.1:18122/radius"`,
		`virtual_server = "goblin-inner"`,
		"server goblin-inner {",
		"&Tmp-String-2 := \"%{%{Packet-Src-IP-Address}:-%{Packet-Src-IPv6-Address}}\"",
		"&TLS-Client-Cert-Serial := &session-state:TLS-Client-Cert-Serial",
		"port = 1812", "port = 1813",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q", want)
		}
	}
	// the aps sort before the switch: the order is stable
	if strings.Index(conf, "client aps") > strings.Index(conf, "client switch") {
		t.Error("clients not sorted")
	}
	if string(files[CertDir+"server.pem"]) != "CERT" || string(files[CertDir+"server.key"]) != "KEY" || string(files[CertDir+"ca.pem"]) != "CA" {
		t.Error("certificates missing")
	}
	if strings.Contains(string(files[DictPath]), "$INCLUDE") {
		t.Error("dictionary")
	}
	for p := range files {
		if !strings.HasPrefix(p, Dir) {
			t.Errorf("%s outside %s", p, Dir)
		}
	}
}

func TestRenderMABOnly(t *testing.T) {
	cfg := rendertest.Radius()
	cfg.RADIUS.Dot1X = false
	r := renderer()
	r.Certs = nil // not needed without 802.1X
	files, err := r.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	conf := string(files[ConfPath])
	if strings.Contains(conf, "eap {") || strings.Contains(conf, "goblin-inner") {
		t.Error("EAP without 802.1X")
	}
	if !strings.Contains(conf, "if (&EAP-Message) {\n\t\t\treject") {
		t.Error("EAP not refused")
	}
	if _, ok := files[CertDir+"server.pem"]; ok {
		t.Error("certificate without 802.1X")
	}
}

func TestRenderOffAndErrors(t *testing.T) {
	cfg := rendertest.Radius()
	cfg.RADIUS.Enabled = false
	if files, err := renderer().Render(cfg); err != nil || len(files) != 0 {
		t.Errorf("off: %v %v", files, err)
	}
	cfg.RADIUS.Enabled = true
	r := renderer()
	r.Token = func() (string, error) { return "", errors.New("disk full") }
	if _, err := r.Render(cfg); err == nil {
		t.Error("no token accepted")
	}
	r = renderer()
	r.Certs = func(*config.Config) (Certs, error) { return Certs{}, errors.New("no CA yet") }
	if _, err := r.Render(cfg); err == nil || !strings.Contains(err.Error(), "no CA yet") {
		t.Errorf("certificate error: %v", err)
	}
	// a secret the config check would refuse never reaches the file
	cfg.RADIUS.Clients[0].Secret = `bad"secret`
	if _, err := renderer().Render(cfg); err == nil {
		t.Error("quote in a secret rendered")
	}
}
