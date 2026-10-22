package templates

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// twoVLANs is a bigger home setup: a trusted LAN and two VLANs on it, a DNS
// filter profile and a VPN provider tunnel.
func twoVLANs() *config.Config {
	c := config.Default()
	for _, v := range []struct {
		name string
		id   int
	}{{"kids", 20}, {"cams", 30}} {
		p := netip.MustParsePrefix(fmt.Sprintf("10.0.%d.1/24", v.id))
		c.VLANs = append(c.VLANs, config.VLAN{Name: v.name, Parent: "lan", VLANID: v.id, Mode: "static", Address: &p, Enabled: true})
	}
	c.Profiles.DNSFilter = []config.DNSFilterProfile{{Name: "family"}}
	c.VPN.WireGuardClients = []config.WireGuardClient{{
		Name: "mullvad", Enabled: true, Address: netip.MustParsePrefix("10.64.0.2/32"),
		PeerPublicKey: "uMB7CjMbXQ3e3l3QWtmN1Sow8WqzMx6zZQTjT3kMrQU=", Endpoint: "198.51.100.7:51820",
	}}
	return c
}

// answersFor fills every template's required inputs that have no default.
var answersFor = map[string]map[string]string{
	"guest-network":   {},
	"iot-network":     {},
	"limited-forward": {"server": "192.168.1.20", "sources": "US, CA, 203.0.113.0/24, 198.51.100.9"},
	"vpn-network":     {"source": "lan"},
	"bedtime":         {"devices": "192.168.1.50 192.168.1.51"},
}

func validate(t *testing.T, c *config.Config) {
	t.Helper()
	if errs := config.Validate(c); len(errs) > 0 {
		t.Fatalf("invalid config: %+v", errs)
	}
}

func TestEveryTemplateRendersValidConfig(t *testing.T) {
	bases := map[string]func() *config.Config{"default": config.Default, "two VLANs": twoVLANs}
	for bname, base := range bases {
		for _, tpl := range all {
			t.Run(bname+"/"+tpl.ID, func(t *testing.T) {
				c := base()
				if c.Admins[0].PasswordHash == "" {
					c.AllowNoAdmin = true
				}
				validate(t, c)
				info := tpl.Info(c)
				if info.Unavailable != "" {
					if tpl.ID == "vpn-network" && bname == "default" {
						if _, err := tpl.Apply(c, answersFor[tpl.ID]); err == nil {
							t.Fatal("applied while unavailable")
						}
						return
					}
					t.Fatalf("unavailable: %s", info.Unavailable)
				}
				before := config.Diff(config.Default(), c)
				out, err := tpl.Apply(c, answersFor[tpl.ID])
				if err != nil {
					t.Fatalf("apply: %v", err)
				}
				out.AllowNoAdmin = c.AllowNoAdmin
				validate(t, out)
				if len(config.Diff(c, out)) == 0 {
					t.Fatal("template changed nothing")
				}
				// the input config is untouched
				if len(config.Diff(config.Default(), c)) != len(before) {
					t.Fatal("template changed its input config")
				}
				// running it again with the same names is refused, not merged
				if _, err := tpl.Apply(out, answersFor[tpl.ID]); err == nil {
					t.Fatal("second run accepted")
				}
			})
		}
	}
}

func TestGuestNetworkShape(t *testing.T) {
	c := twoVLANs()
	out, err := guestNetwork.Apply(c, map[string]string{"dns_filter": "family"})
	if err != nil {
		t.Fatal(err)
	}
	v := out.VLANs[len(out.VLANs)-1]
	if v.Name != "guest" || v.VLANID != 50 || v.Address.String() != "192.168.50.1/24" || slices.Contains(v.AllowAccess, "https") {
		t.Fatalf("vlan %+v", v)
	}
	d := out.DHCP[len(out.DHCP)-1]
	if d.RangeStart.String() != "192.168.50.100" || d.RangeEnd.String() != "192.168.50.199" {
		t.Fatalf("dhcp %+v", d)
	}
	p := out.Policies[0]
	if p.Name != "guest-internet" || p.DNSFilterProfile != "family" || !p.NAT || p.ID != 2 {
		t.Fatalf("policy %+v", p)
	}
}

func TestBedtimeWinsOverTheLANPolicy(t *testing.T) {
	out, err := bedtime.Apply(config.Default(), map[string]string{"devices": "192.168.1.50", "days": "mon, FRI", "start": "20:30", "end": "06:45"})
	if err != nil {
		t.Fatal(err)
	}
	p := out.Policies[0]
	if p.Action != "deny" || p.Schedule != "bedtime" || p.SrcAddr[0] != "bedtime-devices" {
		t.Fatalf("first policy %+v", p)
	}
	r := out.Schedules[0].Ranges[0]
	if !slices.Equal(r.Days, []string{"mon", "fri"}) || r.Start != "20:30" || r.End != "06:45" {
		t.Fatalf("schedule %+v", r)
	}
}

func TestVPNNetworkKillSwitch(t *testing.T) {
	out, err := vpnNetwork.Apply(twoVLANs(), map[string]string{"source": "kids"})
	if err != nil {
		t.Fatal(err)
	}
	// the WAN gets a gateway of its own and stays the default route
	if !out.VPN.WireGuardClients[0].KillSwitch || len(out.Gateways) != 2 || out.Gateways[0].Interface != "wan1" ||
		out.DefaultGateway != "wan1" || out.Policies[0].Gateway != out.Gateways[1].Name {
		t.Fatalf("gateways %+v policy %+v", out.Gateways, out.Policies[0])
	}
	// a second network through the same tunnel reuses its gateway
	out2, err := vpnNetwork.Apply(out, map[string]string{"source": "cams", "kill_switch": "false"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out2.Gateways) != 2 {
		t.Fatalf("gateway duplicated: %+v", out2.Gateways)
	}
}

func TestInputErrors(t *testing.T) {
	cases := []struct {
		tpl    *Template
		values map[string]string
		input  string
	}{
		{guestNetwork, map[string]string{"name": "Guest!"}, "name"},
		{guestNetwork, map[string]string{"name": "lan"}, "name"},
		{guestNetwork, map[string]string{"vlan_id": "5000"}, "vlan_id"},
		{guestNetwork, map[string]string{"address": "192.168.50.0/24"}, "address"},
		{guestNetwork, map[string]string{"address": "192.168.50.150/24"}, "address"},
		{guestNetwork, map[string]string{"address": "10.0.0.1/8"}, "address"},
		{guestNetwork, map[string]string{"parent": "wan1"}, "parent"},
		{guestNetwork, map[string]string{"bogus": "x"}, "bogus"},
		{limitedForward, map[string]string{"server": "192.168.1.20", "sources": "usa"}, "sources"},
		{limitedForward, map[string]string{"server": "192.168.1.20"}, "sources"},
		{limitedForward, map[string]string{"server": "nas", "sources": "US"}, "server"},
		{limitedForward, map[string]string{"server": "192.168.1.20", "sources": "US", "protocol": "icmp"}, "protocol"},
		{bedtime, map[string]string{"devices": "192.168.1.50", "start": "25:00"}, "start"},
		{bedtime, map[string]string{"devices": "192.168.1.50", "start": "07:00", "end": "07:00"}, "end"},
		{bedtime, map[string]string{"devices": "192.168.1.50", "days": "monday"}, "days"},
	}
	for _, tc := range cases {
		_, err := tc.tpl.Apply(config.Default(), tc.values)
		var ie InputErrors
		if !errors.As(err, &ie) || !slices.ContainsFunc(ie, func(e InputError) bool { return e.Input == tc.input }) {
			t.Errorf("%s %v: got %v, want an error on %q", tc.tpl.ID, tc.values, err, tc.input)
		}
	}
}

func TestListOptions(t *testing.T) {
	infos := List(twoVLANs())
	if len(infos) != len(all) {
		t.Fatal("list length")
	}
	for _, i := range infos {
		if i.ID == "vpn-network" {
			for _, in := range i.Inputs {
				if in.Name == "source" && !slices.Equal(in.Options, []string{"lan", "kids", "cams"}) {
					t.Fatalf("source options %v", in.Options)
				}
			}
		}
	}
}

func TestVPNNetworkTwoWANs(t *testing.T) {
	c := twoVLANs()
	c.Interfaces = append(c.Interfaces, config.Interface{Name: "wan2", Device: "eth3", Role: "wan", Mode: "dhcp", Enabled: true})
	out, err := vpnNetwork.Apply(c, map[string]string{"source": "kids"})
	if err != nil {
		t.Fatal(err)
	}
	validate(t, out)
	if out.DefaultGateway != "wan-failover" || len(out.GatewayGroups) != 1 || len(out.GatewayGroups[0].Members) != 2 {
		t.Fatalf("default %q groups %+v", out.DefaultGateway, out.GatewayGroups)
	}
}
