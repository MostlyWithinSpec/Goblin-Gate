package render

import (
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func TestDevices(t *testing.T) {
	cfg := &config.Config{
		Interfaces: []config.Interface{{Name: "wan1", Device: "eth0"}, {Name: "lan", Device: "eth1"}},
		VLANs: []config.VLAN{
			{Name: "guest", Parent: "lan", VLANID: 20},
			{Name: "orphan", Parent: "nope", VLANID: 30},
		},
	}
	got := Devices(cfg)
	want := map[string]string{"wan1": "eth0", "lan": "eth1", "guest": "eth1.20"}
	if len(got) != len(want) {
		t.Fatalf("Devices() = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("Devices()[%q] = %q, want %q", k, got[k], v)
		}
	}
}
