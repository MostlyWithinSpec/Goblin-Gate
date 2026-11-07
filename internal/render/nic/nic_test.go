package nic

import (
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func TestRender(t *testing.T) {
	on, off := true, false
	cfg := config.Default() // wan1 = eth0, lan = eth1
	cfg.Interfaces[1].Offloads = &config.Offloads{GRO: &off, TSO: &on}
	cfg.Interfaces[0].Offloads = &config.Offloads{} // nothing overridden
	files, err := Renderer{}.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := string(files[ScriptPath])
	if !strings.Contains(got, "ethtool -K eth1 gro off tso on || echo") || strings.Contains(got, "eth0") || !strings.HasSuffix(got, "exit 0\n") {
		t.Fatalf("script:\n%s", got)
	}
	cfg.Interfaces[1].Enabled = false
	files, _ = Renderer{}.Render(cfg)
	if strings.Contains(string(files[ScriptPath]), "ethtool -K eth") {
		t.Fatalf("a disabled interface is left alone:\n%s", files[ScriptPath])
	}
}
