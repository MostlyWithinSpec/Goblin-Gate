package frr

import (
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/rendertest"
)

// vpn.sites.announce puts the enabled sites' networks into BGP (network
// statements) and OSPF (kernel routes through a prefix list).
func TestSitesAnnounced(t *testing.T) {
	cfg := rendertest.Sites()
	cfg.VPN.Sites.Announce = true
	cfg.Routing.BGP = config.BGP{Enabled: true, AS: 65001}
	cfg.Routing.OSPF = config.OSPF{Enabled: true, Areas: []config.OSPFArea{{ID: "0", Interfaces: []config.OSPFInterface{{Name: "lan"}}}}}
	if errs := config.Validate(cfg); len(errs) > 0 {
		t.Fatal(errs)
	}
	conf, err := Conf(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"  network 192.168.50.0/24", "  network 192.168.60.0/24", "  network 10.254.2.2/32",
		" redistribute kernel route-map gg-sites",
		"ip prefix-list gg-sites seq 5 permit 10.254.2.2/32",
		"permit 192.168.60.0/24",
		"route-map gg-sites permit 10",
	} {
		if !strings.Contains(conf, want+"\n") && !strings.Contains(conf, want) {
			t.Errorf("missing %q in\n%s", want, conf)
		}
	}
	if strings.Contains(conf, "192.168.70.0/24") {
		t.Error("the disabled site is announced")
	}
	cfg.VPN.Sites.Announce = false
	conf, _ = Conf(cfg)
	if strings.Contains(conf, "192.168.50.0/24") || strings.Contains(conf, "gg-sites") {
		t.Errorf("announced without the flag:\n%s", conf)
	}
}
