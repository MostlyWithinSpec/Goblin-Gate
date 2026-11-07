package caddy

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

var update = flag.Bool("update", false, "rewrite testdata/caddy.json")

func sample() *config.Config {
	c := config.Default()
	c.AllowNoAdmin = true
	c.Addresses = append(c.Addresses, config.Address{Name: "office", Type: "subnet", Value: "203.0.113.0/24"})
	c.ReverseProxy = config.ReverseProxy{
		Enabled: true,
		Email:   "me@example.com",
		Hosts: []config.ProxyHost{
			{Name: "jellyfin", Enabled: true, Domains: []string{"media.example.com"}, Upstream: "http://192.168.1.10:8096"},
			{Name: "nas", Enabled: true, Domains: []string{"nas.example.com", "files.example.com"}, Upstream: "https://192.168.1.20:5001", InsecureSkipVerify: true, Allow: []string{"office", "198.51.100.7"}},
			{Name: "ha", Enabled: true, Domains: []string{"ha.example.com"}, Upstream: "https://homeassistant.home.lan:8123"},
			{Name: "off", Enabled: false, Domains: []string{"off.example.com"}, Upstream: "http://192.168.1.30"},
		},
	}
	return c
}

func TestRenderGolden(t *testing.T) {
	c := sample()
	if errs := config.Validate(c); len(errs) > 0 {
		t.Fatalf("sample config invalid: %v", errs)
	}
	files, err := Renderer{}.Render(c)
	if err != nil {
		t.Fatal(err)
	}
	got := files[ConfPath]
	golden := filepath.Join("testdata", "caddy.json")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
		t.Errorf("caddy.json differs from %s (run with -update):\n%s", golden, got)
	}
}

func TestRenderContents(t *testing.T) {
	files, err := Renderer{}.Render(sample())
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(files[ConfPath], &doc); err != nil {
		t.Fatal(err)
	}
	s := string(files[ConfPath])
	for _, want := range []string{
		`"disabled": true`, // no admin API
		`"192.168.1.10:8096"`,
		`"insecure_skip_verify": true`,
		`"server_name": "homeassistant.home.lan"`,
		`"203.0.113.0/24"`, `"198.51.100.7/32"`,
		`"alternate_port": 10080`, `"alternate_port": 10443`,
		`"email": "me@example.com"`,
		`"https://acme-v02.api.letsencrypt.org/directory"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(s, "off.example.com") {
		t.Error("a disabled host is published")
	}
}

func TestRenderOffAndStaging(t *testing.T) {
	c := sample()
	c.ReverseProxy.Enabled = false
	if files, _ := (Renderer{}).Render(c); len(files) != 0 {
		t.Error("disabled proxy rendered a config")
	}
	c = sample()
	for i := range c.ReverseProxy.Hosts {
		c.ReverseProxy.Hosts[i].Enabled = false
	}
	if files, _ := (Renderer{}).Render(c); len(files) != 0 || Active(c) {
		t.Error("no enabled host: still rendered or active")
	}
	c = sample()
	c.ReverseProxy.Staging = true
	files, _ := Renderer{}.Render(c)
	if !strings.Contains(string(files[ConfPath]), "acme-staging-v02") {
		t.Error("staging CA not used")
	}
}
