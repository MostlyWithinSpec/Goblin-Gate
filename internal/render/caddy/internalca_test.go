package caddy

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func caConfig() *config.Config {
	c := sample()
	c.System.Hostname = "gate"
	c.DNS.LocalDomain = "home.lan"
	c.InternalCA = config.InternalCA{Enabled: true, ACME: true}
	c.ReverseProxy.Hosts = append(c.ReverseProxy.Hosts,
		config.ProxyHost{Name: "nas-lan", Enabled: true, Issuer: "internal", Domains: []string{"nas.home.lan"}, Upstream: "https://192.168.1.20:5001", InsecureSkipVerify: true})
	return c
}

func render(t *testing.T, c *config.Config) (string, map[string]any) {
	t.Helper()
	if errs := config.Validate(c); len(errs) > 0 {
		t.Fatal(errs)
	}
	files, err := Renderer{}.Render(c)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(files[ConfPath], &doc); err != nil {
		t.Fatal(err)
	}
	return string(files[ConfPath]), doc
}

func TestInternalCA(t *testing.T) {
	s, doc := render(t, caConfig())
	apps := doc["apps"].(map[string]any)
	cas := apps["pki"].(map[string]any)["certificate_authorities"].(map[string]any)
	local := cas["local"].(map[string]any)
	if local["name"] != "Goblin Gate Local CA" || local["install_trust"] != false || local["root_common_name"] != "Goblin Gate Local CA Root" {
		t.Errorf("pki: %v", local)
	}
	// the ACME directory: on 9443 for the box's LAN names, LAN ranges only
	acme := apps["http"].(map[string]any)["servers"].(map[string]any)["acme"].(map[string]any)
	if !slices.Equal(anyStrings(acme["listen"]), []string{":9443"}) {
		t.Errorf("acme listen %v", acme["listen"])
	}
	route := acme["routes"].([]any)[0].(map[string]any)
	if got := anyStrings(route["match"].([]any)[0].(map[string]any)["host"]); !slices.Equal(got, []string{"gate.home.lan", "192.168.1.1"}) {
		t.Errorf("acme hosts %v", got)
	}
	for _, want := range []string{
		`"handler": "acme_server"`,
		`"lifetime": "48h"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("caddy.json lacks %s", want)
		}
	}
	// policies: Let's Encrypt for public names, the internal CA for the rest
	pols := apps["tls"].(map[string]any)["automation"].(map[string]any)["policies"].([]any)
	if len(pols) != 2 {
		t.Fatalf("policies %v", pols)
	}
	le, in := pols[0].(map[string]any), pols[1].(map[string]any)
	if slices.Contains(anyStrings(le["subjects"]), "nas.home.lan") {
		t.Error("an internal name went to Let's Encrypt")
	}
	if got := anyStrings(in["subjects"]); !slices.Equal(got, []string{"nas.home.lan", "gate.home.lan", "192.168.1.1"}) {
		t.Errorf("internal subjects %v", got)
	}
	if is := in["issuers"].([]any)[0].(map[string]any); is["module"] != "internal" || is["ca"] != "local" {
		t.Errorf("internal issuer %v", is)
	}
	// an internal host with no allow list is for the LAN side only
	if !strings.Contains(s, `"192.168.1.0/24"`) {
		t.Error("internal host not limited to the LAN")
	}
	if got := ACMEDirectories(caConfig()); !slices.Equal(got, []string{"https://gate.home.lan:9443/acme/local/directory", "https://192.168.1.1:9443/acme/local/directory"}) {
		t.Errorf("directories %v", got)
	}
	if got := InternalDomains(caConfig()); !slices.Equal(got, []string{"nas.home.lan"}) {
		t.Errorf("internal domains %v", got)
	}
}

func TestInternalCAWithoutProxy(t *testing.T) {
	c := caConfig()
	c.ReverseProxy = config.ReverseProxy{}
	c.InternalCA.ACME = false
	if !Active(c) || ProxyActive(c) {
		t.Fatal("the CA alone runs Caddy, without the proxy")
	}
	s, doc := render(t, c)
	apps := doc["apps"].(map[string]any)
	if _, ok := apps["pki"]; !ok {
		t.Error("no pki app")
	}
	if _, ok := apps["http"]; ok {
		t.Errorf("servers without a proxy or ACME:\n%s", s)
	}
	if _, ok := apps["tls"]; ok {
		t.Errorf("tls policies with nothing to certify:\n%s", s)
	}
	// with ACME and an allow list
	c.InternalCA.ACME = true
	c.InternalCA.ACMEAllow = []string{"192.168.1.0/28"}
	s, _ = render(t, c)
	if !strings.Contains(s, `"192.168.1.0/28"`) || strings.Contains(s, `"192.168.1.0/24"`) {
		t.Errorf("acme_allow not used:\n%s", s)
	}
}

func anyStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}
