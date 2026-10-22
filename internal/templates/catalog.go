package templates

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// all is the catalog. Bump a template's Version when what it creates
// changes.
var all = []*Template{guestNetwork, iotNetwork, limitedForward, vpnNetwork, bedtime}

func first(list []string) string {
	if len(list) == 0 {
		return ""
	}
	return list[0]
}

// networkInputs are the questions of a new VLAN network.
func networkInputs(c *config.Config, name string, vlan int) []Input {
	return []Input{
		{Name: "name", Label: "Network name", Type: TypeName, Default: name, Required: true},
		{Name: "parent", Label: "On interface", Type: TypeInterface, Options: physicalInside(c), Default: first(physicalInside(c)), Required: true,
			Help: "The physical port the VLAN runs on; your switch and access point must carry this VLAN tagged."},
		{Name: "vlan_id", Label: "VLAN ID", Type: TypeVLANID, Default: fmt.Sprint(vlan), Required: true},
		{Name: "address", Label: "Box address", Type: TypeCIDR, Default: fmt.Sprintf("192.168.%d.1/24", vlan), Required: true,
			Help: "The box's address on the new network; DHCP hands out .100-.199 of a /24."},
		{Name: "dns_filter", Label: "DNS filter profile", Type: TypeProfile, Options: dnsFilterNames(c),
			Help: "Optional: block malware, phishing or ads for everyone on this network."},
	}
}

// addNetwork creates the VLAN, its DHCP server and a policy to the
// internet; it returns the network's name.
func addNetwork(c *config.Config, a *answers, label string) string {
	name := a.name("name", netifNames(c), policyNames(c))
	parent := a.raw("parent")
	vlan := a.int("vlan_id", 1, 4094)
	box := a.prefix("address")
	profile := a.raw("dns_filter")
	for _, v := range c.VLANs {
		if v.Parent == parent && v.VLANID == vlan {
			a.fail("vlan_id", "VLAN %d is already %q on %s", vlan, v.Name, parent)
		}
	}
	if len(wans(c)) == 0 {
		a.fail("", "no WAN interface is enabled")
	}
	start, end, ok := dhcpRange(box)
	if box.IsValid() && !ok {
		a.fail("address", "the box's address is inside the DHCP pool; use the network's first address, e.g. %s", box.Masked().Addr().Next())
	}
	if len(a.errs) > 0 {
		return ""
	}
	gw := box.Addr()
	c.VLANs = append(c.VLANs, config.VLAN{
		Name: name, Parent: parent, VLANID: vlan, Mode: "static", Address: &box, Enabled: true,
		Description: label, AllowAccess: []string{"ping"},
	})
	c.DHCP = append(c.DHCP, config.DHCPServer{
		Interface: name, RangeStart: start, RangeEnd: end, LeaseSeconds: 86400,
		DNS: []netip.Addr{gw}, Gateway: &gw,
	})
	addPolicy(c, config.Policy{
		Name: name + "-internet", SrcIntf: []string{name}, DstIntf: wans(c),
		SrcAddr: []string{"all"}, DstAddr: []string{"all"}, Service: []string{"all"},
		Action: "accept", NAT: true, DNSFilterProfile: profile,
	})
	return name
}

var guestNetwork = &Template{
	ID: "guest-network", Version: 1, Title: "Guest network",
	Description: "A separate network for visitors: internet only, no way into your own devices. Add it to your " +
		"access point as a guest SSID on the same VLAN.",
	inputs: func(c *config.Config) []Input { return networkInputs(c, "guest", 50) },
	apply: func(c *config.Config, a *answers) {
		addNetwork(c, a, "Guest network")
	},
}

var iotNetwork = &Template{
	ID: "iot-network", Version: 1, Title: "IoT network",
	Description: "Smart plugs, cameras and TVs on their own network: they reach the internet, your own network " +
		"can reach them, but they can't reach your computers.",
	inputs: func(c *config.Config) []Input {
		in := networkInputs(c, "iot", 60)
		from := ""
		if slices.Contains(inside(c), "lan") {
			from = "lan"
		}
		return append(in, Input{Name: "managed_from", Label: "Reachable from", Type: TypeInterface, Options: inside(c), Default: from,
			Help: "The network allowed to open connections to the IoT devices (apps, Home Assistant). Leave empty for none."})
	},
	apply: func(c *config.Config, a *answers) {
		from := a.raw("managed_from")
		name := addNetwork(c, a, "IoT network")
		if name == "" || from == "" {
			return
		}
		pol := from + "-to-" + name
		if slices.Contains(policyNames(c), pol) {
			a.fail("managed_from", "a policy named %q already exists", pol)
			return
		}
		addPolicy(c, config.Policy{
			Name: pol, SrcIntf: []string{from}, DstIntf: []string{name},
			SrcAddr: []string{"all"}, DstAddr: []string{"all"}, Service: []string{"all"}, Action: "accept",
		})
	},
}

var limitedForward = &Template{
	ID: "limited-forward", Version: 1, Title: "Port forward for some places only",
	Description: "Reach a server at home (Plex, a game server) from the internet, but only from the countries or " +
		"networks you list. Everyone else gets no answer.",
	inputs: func(c *config.Config) []Input {
		return []Input{
			{Name: "name", Label: "Name", Type: TypeName, Default: "plex", Required: true},
			{Name: "server", Label: "Server address", Type: TypeIP, Required: true, Help: "The server's address on your network, e.g. 192.168.1.20."},
			{Name: "port", Label: "Port", Type: TypePort, Default: "32400", Required: true},
			{Name: "protocol", Label: "Protocol", Type: TypeProtocol, Default: "tcp", Required: true},
			{Name: "sources", Label: "Allowed from", Type: TypeSources, Required: true,
				Help: "Country codes (US, CA) and networks (203.0.113.0/24), separated by commas."},
			{Name: "wan", Label: "On WAN", Type: TypeInterface, Options: wans(c), Default: first(wans(c)), Required: true},
		}
	},
	unavailable: func(c *config.Config) string {
		if len(wans(c)) == 0 {
			return "no WAN interface is enabled"
		}
		return ""
	},
	apply: func(c *config.Config, a *answers) {
		var forwards []string
		for _, f := range c.NAT.PortForwards {
			forwards = append(forwards, f.Name)
		}
		name := a.name("name", forwards)
		server := a.ips("server")
		port := a.int("port", 1, 65535)
		proto := a.raw("protocol")
		if proto != "" && proto != "tcp" && proto != "udp" {
			a.fail("protocol", "want tcp or udp")
		}
		countries, nets := a.sources("sources")
		if len(server) != 1 {
			a.fail("server", "give one address")
		}
		if len(countries)+len(nets) == 0 {
			a.fail("sources", "list at least one country or network; an open forward is not what this template is for")
		}
		taken := addressNames(c)
		var src []string
		if len(countries) > 0 {
			src = append(src, name+"-countries")
		}
		for i := range nets {
			src = append(src, fmt.Sprintf("%s-net%d", name, i+1))
		}
		for _, s := range src {
			if slices.Contains(taken, s) {
				a.fail("name", "an address named %q already exists", s)
			}
		}
		if len(a.errs) > 0 {
			return
		}
		if len(countries) > 0 {
			c.Addresses = append(c.Addresses, config.Address{Name: name + "-countries", Type: "country", Value: strings.Join(countries, ","),
				Comment: "allowed sources of port forward " + name})
		}
		for i, n := range nets {
			addr := config.Address{Name: fmt.Sprintf("%s-net%d", name, i+1), Type: "subnet", Value: n.String(),
				Comment: "allowed source of port forward " + name}
			if n.IsSingleIP() {
				addr.Type, addr.Value = "host", n.Addr().String()
			}
			c.Addresses = append(c.Addresses, addr)
		}
		c.NAT.PortForwards = append(c.NAT.PortForwards, config.PortForward{
			Name: name, InIntf: a.raw("wan"), ExtPort: fmt.Sprint(port), Protocol: proto,
			ToAddr: server[0].String(), ToPort: fmt.Sprint(port), Source: src,
		})
	},
}

var vpnNetwork = &Template{
	ID: "vpn-network", Version: 1, Title: "Network through a VPN provider",
	Description: "Send everything from one network (say a VLAN for the TV) through your VPN provider, with a " +
		"kill switch: if the tunnel is down, that traffic stops instead of leaking out your normal WAN.",
	inputs: func(c *config.Config) []Input {
		return []Input{
			{Name: "source", Label: "Network", Type: TypeInterface, Options: inside(c), Required: true},
			{Name: "tunnel", Label: "VPN provider", Type: TypeTunnel, Options: tunnelNames(c), Default: first(tunnelNames(c)), Required: true},
			{Name: "kill_switch", Label: "Kill switch", Type: TypeBool, Default: "true", Required: true},
		}
	},
	unavailable: func(c *config.Config) string {
		if len(c.VPN.WireGuardClients) == 0 {
			return "add a VPN provider first (VPN → VPN Providers)"
		}
		return ""
	},
	apply: func(c *config.Config, a *answers) {
		source, tunnel := a.raw("source"), a.raw("tunnel")
		kill := a.raw("kill_switch")
		if kill != "true" && kill != "false" {
			a.fail("kill_switch", "want true or false")
		}
		pol := source + "-via-" + tunnel
		if slices.Contains(policyNames(c), pol) {
			a.fail("source", "a policy named %q already exists", pol)
		}
		if len(a.errs) > 0 {
			return
		}
		keepDefaultOnWAN(c, a)
		if len(a.errs) > 0 {
			return
		}
		gw := ""
		for _, g := range c.Gateways {
			if g.Interface == tunnel {
				gw = g.Name
			}
		}
		if gw == "" {
			gw = "vpn-" + tunnel
			for n := 2; slices.ContainsFunc(c.Gateways, func(g config.Gateway) bool { return g.Name == gw }); n++ {
				gw = fmt.Sprintf("vpn-%s-%d", tunnel, n)
			}
			c.Gateways = append(c.Gateways, config.Gateway{Name: gw, Interface: tunnel})
		}
		for i := range c.VPN.WireGuardClients {
			if c.VPN.WireGuardClients[i].Name == tunnel && kill == "true" {
				c.VPN.WireGuardClients[i].KillSwitch = true
			}
		}
		addPolicy(c, config.Policy{
			Name: pol, SrcIntf: []string{source}, DstIntf: []string{tunnel},
			SrcAddr: []string{"all"}, DstAddr: []string{"all"}, Service: []string{"all"},
			Action: "accept", NAT: true, Gateway: gw,
		})
	},
}

var bedtime = &Template{
	ID: "bedtime", Version: 1, Title: "Bedtime for some devices",
	Description: "Turn off the internet for the kids' tablets and consoles at night. Everything else on the " +
		"network keeps working.",
	inputs: func(c *config.Config) []Input {
		return []Input{
			{Name: "name", Label: "Name", Type: TypeName, Default: "bedtime", Required: true},
			{Name: "interface", Label: "Network", Type: TypeInterface, Options: inside(c), Default: first(inside(c)), Required: true},
			{Name: "devices", Label: "Device addresses", Type: TypeIPs, Required: true,
				Help: "Their IP addresses; give them static DHCP leases first so the addresses don't change."},
			{Name: "days", Label: "Days", Type: TypeDays, Default: "mon,tue,wed,thu,fri,sat,sun", Required: true},
			{Name: "start", Label: "From", Type: TypeTime, Default: "21:00", Required: true},
			{Name: "end", Label: "Until", Type: TypeTime, Default: "07:00", Required: true},
		}
	},
	apply: func(c *config.Config, a *answers) {
		var schedules []string
		for _, s := range c.Schedules {
			schedules = append(schedules, s.Name)
		}
		name := a.name("name", schedules, policyNames(c))
		iface := a.raw("interface")
		devices := a.ips("devices")
		days := a.days("days")
		start, end := a.time("start"), a.time("end")
		if start != "" && start == end {
			a.fail("end", "the end must differ from the start")
		}
		if len(devices) == 0 {
			a.fail("devices", "list at least one address")
		}
		if len(devices) > 32 {
			a.fail("devices", "at most 32 addresses")
		}
		if len(wans(c)) == 0 {
			a.fail("", "no WAN interface is enabled")
		}
		group := name + "-devices"
		var members []string
		for i := range devices {
			members = append(members, fmt.Sprintf("%s-%d", name, i+1))
		}
		taken := addressNames(c)
		for _, n := range append([]string{group}, members...) {
			if slices.Contains(taken, n) {
				a.fail("name", "an address named %q already exists", n)
			}
		}
		if len(a.errs) > 0 {
			return
		}
		for i, ip := range devices {
			c.Addresses = append(c.Addresses, config.Address{Name: members[i], Type: "host", Value: ip.String(), Comment: "bedtime " + name})
		}
		c.AddressGroups = append(c.AddressGroups, config.AddressGroup{Name: group, Members: members})
		c.Schedules = append(c.Schedules, config.Schedule{Name: name, Comment: "bedtime",
			Ranges: []config.ScheduleRange{{Days: days, Start: start, End: end}}})
		addPolicy(c, config.Policy{
			Name: name, SrcIntf: []string{iface}, DstIntf: wans(c),
			SrcAddr: []string{group}, DstAddr: []string{"all"}, Service: []string{"all"},
			Action: "deny", Schedule: name,
		})
	},
}

// keepDefaultOnWAN prepares c for a tunnel gateway. Once gateways exist,
// every WAN needs one (goblind then owns the default route), and an unset
// default gateway means "every gateway in list order", which would let
// ordinary traffic fail over into the tunnel. So the WANs get gateways if
// they have none, and the default is pinned to them.
func keepDefaultOnWAN(c *config.Config, a *answers) {
	if len(c.Gateways) == 0 {
		for _, i := range c.Interfaces {
			if i.Role != "wan" || !i.Enabled || i.Mode == "none" {
				continue
			}
			addr := "dhcp"
			if i.Mode != "dhcp" {
				if i.Gateway == nil {
					a.fail("", "WAN %s has a static address but no gateway; set its gateway first", i.Name)
					return
				}
				addr = i.Gateway.String()
			}
			c.Gateways = append(c.Gateways, config.Gateway{Name: i.Name, Interface: i.Name, Address: addr})
		}
	}
	if c.DefaultGateway != "" {
		return
	}
	var wanGWs []string
	for _, g := range c.Gateways {
		if roleOf(c, g.Interface) == "wan" {
			wanGWs = append(wanGWs, g.Name)
		}
	}
	switch len(wanGWs) {
	case 0:
	case 1:
		c.DefaultGateway = wanGWs[0]
	default:
		name := "wan-failover"
		for n := 2; slices.ContainsFunc(c.GatewayGroups, func(g config.GatewayGroup) bool { return g.Name == name }); n++ {
			name = fmt.Sprintf("wan-failover-%d", n)
		}
		group := config.GatewayGroup{Name: name}
		for i, g := range wanGWs {
			group.Members = append(group.Members, config.GroupMember{Gateway: g, Tier: i + 1})
		}
		c.GatewayGroups = append(c.GatewayGroups, group)
		c.DefaultGateway = name
	}
}
