package nft

import (
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/rendertest"
)

func TestRadiusInput(t *testing.T) {
	cfg := rendertest.Radius()
	cfg.RADIUS.Clients = append(cfg.RADIUS.Clients, config.RADIUSClient{Name: "v6", Address: "fd00::2", Secret: "v6-secret-1"})
	files, err := Renderer{}.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rs := all(files)
	for _, want := range []string{
		`ip saddr { 192.168.1.2/32, 192.168.1.64/28 } udp dport { 1812, 1813 } accept comment "RADIUS clients"`,
		`ip6 saddr { fd00::2/128 } udp dport { 1812, 1813 } accept comment "RADIUS clients"`,
	} {
		if !strings.Contains(rs, want) {
			t.Errorf("missing %q", want)
		}
	}
	if !strings.Contains(rs, `iifname != `) {
		t.Error("RADIUS open on the WAN")
	}
	cfg.RADIUS.Enabled = false
	files, _ = Renderer{}.Render(cfg)
	if strings.Contains(all(files), "1812") {
		t.Error("RADIUS open while off")
	}
}

func all(files map[string][]byte) string {
	var s string
	for _, b := range files {
		s += string(b)
	}
	return s
}
