package frr

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func routingConfig() *config.Config {
	cfg := config.Default()
	lan := netip.MustParsePrefix("192.168.1.1/24")
	cfg.Interfaces[1].Address = &lan
	cfg.Addresses = append(cfg.Addresses, config.Address{Name: "servers", Type: "subnet", Value: "10.50.0.0/16"},
		config.Address{Name: "anycast", Type: "host", Value: "fd00:53::53"})
	cfg.Routing.BGP = config.BGP{Enabled: true, AS: 65001, Announce: []string{"lan", "servers", "anycast"}, RedistributeStatic: true,
		Neighbors: []config.BGPNeighbor{
			{Address: netip.MustParseAddr("192.0.2.1"), RemoteAS: 64512, Enabled: true, Description: "isp", Password: "pw", Multihop: 2,
				Import: []config.PrefixRule{{Action: "permit", Prefix: netip.MustParsePrefix("0.0.0.0/0")}},
				Export: []config.PrefixRule{{Action: "deny", Prefix: netip.MustParsePrefix("10.0.0.0/8"), MaxLength: 32}, {Action: "permit", Prefix: netip.MustParsePrefix("0.0.0.0/0"), MaxLength: 24}}},
			{Address: netip.MustParseAddr("2001:db8::1"), RemoteAS: 64512, Enabled: true},
			{Address: netip.MustParseAddr("192.0.2.9"), RemoteAS: 64513, Enabled: false},
		}}
	cfg.Routing.OSPF = config.OSPF{Enabled: true, RedistributeConnected: true,
		Areas: []config.OSPFArea{{ID: "0", Interfaces: []config.OSPFInterface{{Name: "lan", Cost: 5, Passive: true}}}}}
	return cfg
}

func TestRender(t *testing.T) {
	cfg := routingConfig()
	if errs := config.Validate(cfg); len(errs) > 0 {
		t.Fatal(errs)
	}
	files, err := Renderer{}.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	conf := string(files[ConfPath])
	for _, want := range []string{
		"router bgp 65001\n bgp router-id 192.168.1.1\n",
		" neighbor 192.0.2.1 remote-as 64512\n neighbor 192.0.2.1 description isp\n neighbor 192.0.2.1 password pw\n neighbor 192.0.2.1 ebgp-multihop 2\n",
		" neighbor 2001:db8::1 remote-as 64512\n",
		// announce: the LAN, a subnet object; an IPv6 host in its own family
		" address-family ipv4 unicast\n  network 10.50.0.0/16\n  network 192.168.1.0/24\n  redistribute static\n  redistribute kernel\n",
		" address-family ipv6 unicast\n  network fd00:53::53/128\n",
		"  neighbor 192.0.2.1 route-map gg-n1-in in\n  neighbor 192.0.2.1 route-map gg-n1-out out\n",
		"  neighbor 2001:db8::1 activate\n",
		// filters: empty = everything; a list for one family denies the other
		"ip prefix-list gg-n1-in seq 5 permit 0.0.0.0/0\nipv6 prefix-list gg-n1-in seq 5 deny any\n",
		"ip prefix-list gg-n1-out seq 5 deny 10.0.0.0/8 le 32\nip prefix-list gg-n1-out seq 10 permit 0.0.0.0/0 le 24\n",
		"route-map gg-n2-in permit 10\nexit\n",
		"interface eth1\n ip ospf area 0.0.0.0\n ip ospf cost 5\n ip ospf passive\nexit\n",
		"router ospf\n ospf router-id 192.168.1.1\n log-adjacency-changes\n redistribute connected\n",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("frr.conf lacks:\n%s\n--- frr.conf:\n%s", want, conf)
		}
	}
	if strings.Contains(conf, "192.0.2.9") {
		t.Error("a disabled neighbour is rendered")
	}
	if d := string(files[DaemonsPath]); !strings.Contains(d, "bgpd=yes\nospfd=yes\n") || !strings.Contains(d, "ripd=no\n") {
		t.Errorf("daemons:\n%s", d)
	}

	// only OSPF: no bgpd; neither: nothing at all
	cfg.Routing.BGP.Enabled = false
	files, _ = Renderer{}.Render(cfg)
	if c := string(files[ConfPath]); strings.Contains(c, "router bgp") || !strings.Contains(string(files[DaemonsPath]), "bgpd=no") {
		t.Errorf("ospf only:\n%s", c)
	}
	cfg.Routing.OSPF.Enabled = false
	if files, _ = (Renderer{}).Render(cfg); len(files) != 0 {
		t.Errorf("rendered while off: %v", files)
	}
}

func TestValidate(t *testing.T) {
	cfg := routingConfig()
	cfg.Routing.RouterID = netip.MustParseAddr("10.255.0.1")
	if errs := config.Validate(cfg); len(errs) > 0 {
		t.Fatal(errs)
	}
	if c, _ := Conf(cfg); !strings.Contains(c, "bgp router-id 10.255.0.1") {
		t.Error("router_id not used")
	}
	for name, change := range map[string]func(c *config.Config){
		"router id v6":     func(c *config.Config) { c.Routing.RouterID = netip.MustParseAddr("fd00::1") },
		"AS 0":             func(c *config.Config) { c.Routing.BGP.AS = 0 },
		"AS_TRANS":         func(c *config.Config) { c.Routing.BGP.Neighbors[0].RemoteAS = 23456 },
		"announce unknown": func(c *config.Config) { c.Routing.BGP.Announce = []string{"nope"} },
		"announce fqdn": func(c *config.Config) {
			c.Addresses = append(c.Addresses, config.Address{Name: "web", Type: "fqdn", Value: "example.com"})
			c.Routing.BGP.Announce = []string{"web"}
		},
		"announce dhcp wan":  func(c *config.Config) { c.Routing.BGP.Announce = []string{"wan1"} },
		"duplicate neighbor": func(c *config.Config) { c.Routing.BGP.Neighbors[1].Address = c.Routing.BGP.Neighbors[0].Address },
		"no address":         func(c *config.Config) { c.Routing.BGP.Neighbors[0].Address = netip.Addr{} },
		"password space":     func(c *config.Config) { c.Routing.BGP.Neighbors[0].Password = "a b" },
		"description quote":  func(c *config.Config) { c.Routing.BGP.Neighbors[0].Description = "x\" password y" },
		"description nl":     func(c *config.Config) { c.Routing.BGP.Neighbors[0].Description = "x\n password y" },
		"multihop":           func(c *config.Config) { c.Routing.BGP.Neighbors[0].Multihop = 256 },
		"rule action":        func(c *config.Config) { c.Routing.BGP.Neighbors[0].Import[0].Action = "allow" },
		"rule host bits": func(c *config.Config) {
			c.Routing.BGP.Neighbors[0].Import[0].Prefix = netip.MustParsePrefix("10.0.0.1/8")
		},
		"rule max length": func(c *config.Config) { c.Routing.BGP.Neighbors[0].Export[0].MaxLength = 4 },
		"no areas":        func(c *config.Config) { c.Routing.OSPF.Areas = nil },
		"area id":         func(c *config.Config) { c.Routing.OSPF.Areas[0].ID = "backbone" },
		"ospf iface":      func(c *config.Config) { c.Routing.OSPF.Areas[0].Interfaces[0].Name = "nope" },
		"ospf dhcp iface": func(c *config.Config) { c.Routing.OSPF.Areas[0].Interfaces[0].Name = "wan1" },
		"ospf twice": func(c *config.Config) {
			c.Routing.OSPF.Areas = append(c.Routing.OSPF.Areas, config.OSPFArea{ID: "1", Interfaces: c.Routing.OSPF.Areas[0].Interfaces})
		},
		"cost": func(c *config.Config) { c.Routing.OSPF.Areas[0].Interfaces[0].Cost = 70000 },
	} {
		c := routingConfig()
		change(c)
		if errs := config.Validate(c); len(errs) == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
	// off: nothing is checked
	c := routingConfig()
	c.Routing.BGP.Enabled, c.Routing.OSPF.Enabled = false, false
	c.Routing.BGP.AS = 0
	if errs := config.Validate(c); len(errs) > 0 {
		t.Errorf("off: %v", errs)
	}
	if id, ok := config.OSPFAreaID("1"); !ok || id != "0.0.0.1" {
		t.Errorf("area 1 = %q", id)
	}
}

func TestSecrets(t *testing.T) {
	prev := routingConfig()
	red := prev.Clone()
	config.RedactSecrets(red)
	if red.Routing.BGP.Neighbors[0].Password != "" {
		t.Fatal("password not redacted")
	}
	// written back unchanged: the password stays
	next := red.Clone()
	config.KeepRedactedSecrets(prev, next)
	if next.Routing.BGP.Neighbors[0].Password != "pw" {
		t.Error("password lost")
	}
	// pointed elsewhere: it must be typed again
	next = red.Clone()
	next.Routing.BGP.Neighbors[0].RemoteAS = 65555
	config.KeepRedactedSecrets(prev, next)
	if next.Routing.BGP.Neighbors[0].Password != "" {
		t.Error("password kept for a changed neighbour")
	}
}
