package pfsense

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/")

func importSample(t *testing.T, devices map[string]string) *Result {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "sample.xml"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := Import(data, Options{Devices: devices})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// TestGolden compares the converted config and the report with
// testdata/sample.config.json and sample.report.txt.
// Regenerate with: go test ./internal/importer/pfsense -update
func TestGolden(t *testing.T) {
	res := importSample(t, map[string]string{"opt2": "eth2", "igb3": "eth3"})
	cfgJSON, _ := json.MarshalIndent(res.Config, "", "  ")
	var rep strings.Builder
	for _, e := range res.Report {
		risk := ""
		if e.Risk {
			risk = " [risk]"
		}
		fmt.Fprintf(&rep, "%s | %s | %s | %s%s\n", e.Section, e.Status, e.Item, e.Detail, risk)
	}
	for _, m := range res.Interfaces {
		fmt.Fprintf(&rep, "interface %s (%s) -> %s role=%s vlan=%v device=%q\n", m.PfSense, m.PfSenseDevice, m.Name, m.Role, m.VLAN, m.Device)
	}
	for _, e := range res.Errors {
		fmt.Fprintf(&rep, "validation: %s\n", e)
	}
	for name, got := range map[string][]byte{"sample.config.json": append(cfgJSON, '\n'), "sample.report.txt": []byte(rep.String())} {
		path := filepath.Join("testdata", name)
		if *update {
			os.WriteFile(path, got, 0o644)
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v (run with -update)", err)
		}
		if string(want) != string(got) {
			t.Errorf("%s differs from the golden file (run with -update and review the diff):\n%s", name, got)
		}
	}
}

func TestSampleIsValid(t *testing.T) {
	res := importSample(t, map[string]string{"opt2": "eth2", "igb3": "eth3"})
	if len(res.Errors) > 0 {
		t.Fatalf("validation errors: %v", res.Errors)
	}
}

func find(res *Result, item string) []Entry {
	var out []Entry
	for _, e := range res.Report {
		if e.Item == item {
			out = append(out, e)
		}
	}
	return out
}

func has(res *Result, item, status, detail string) bool {
	for _, e := range find(res, item) {
		if e.Status == status && strings.Contains(e.Detail, detail) {
			return true
		}
	}
	return false
}

func TestMappings(t *testing.T) {
	res := importSample(t, nil)
	c := res.Config
	check := func(name string, ok bool) {
		t.Helper()
		if !ok {
			t.Errorf("%s", name)
		}
	}
	// system
	check("EST becomes America/New_York", c.System.Timezone == "America/New_York" && has(res, "time zone", Approximated, "America/New_York"))
	check("NTP servers", len(c.System.NTPServers) == 2)
	check("SSH key imported, password not", len(c.System.SSHKeys) == 1 && has(res, "users", Skipped, "password hashes"))
	check("admins kept from the base config", len(c.Admins) == len(config.Default().Admins))
	// interfaces: devices of the base for wan/lan, pfSense names otherwise
	var lan, iot *InterfaceMap
	for i := range res.Interfaces {
		switch res.Interfaces[i].PfSense {
		case "lan":
			lan = &res.Interfaces[i]
		case "opt1":
			iot = &res.Interfaces[i]
		}
	}
	check("lan on the base's LAN device", lan != nil && lan.Device == "eth1" && lan.Name == "lan")
	check("opt1 is a VLAN on lan", iot != nil && iot.VLAN && iot.Name == "iot-devices")
	check("VLAN with tag 30", len(c.VLANs) >= 1 && c.VLANs[0].VLANID == 30 && c.VLANs[0].Parent == "lan")
	check("unassigned opt2 asks for a device", has(res, "opt2", Approximated, "choose which network port"))
	check("PPPoE skipped and flagged", has(res, "opt2", Skipped, "PPPoE"))
	check("WAN dhcp6 with /56", c.Interfaces[0].IPv6 != nil && c.Interfaces[0].IPv6.Mode == "dhcp6" && c.Interfaces[0].IPv6.PDSize == 56)
	check("LAN and IoT track the WAN", c.Interfaces[1].IPv6 != nil && c.Interfaces[1].IPv6.Mode == "delegated" && c.VLANs[0].IPv6.PrefixID == 3)
	check("spoofed MAC reported", has(res, "wan", Skipped, "MAC address spoofing"))
	check("VLAN on an unassigned NIC adds the NIC", has(res, "igb3", Approximated, "without assigning it"))
	// aliases
	check("host alias with several entries → group", groupHas(c, "Kids_Devices", 3))
	check("fqdn member", addrType(c, "Kids_Devices-3") == "fqdn")
	check("nested alias in a group", groupHasMember(c, "Blocked_Nets", "NAS"))
	check("url alias → feed", feedNamed(c, "bad_ips-feed") != nil)
	// rules
	check("anti-lockout (to the firewall) skipped", has(res, "Anti-Lockout Rule", Skipped, "firewall itself"))
	p := policy(c, "block-bad-nets")
	check("floating rule on wan+iot, first", p != nil && p.ID == 1 && len(p.SrcIntf) == 2 && p.Action == "deny" && p.Log == "all")
	check("floating out direction approximated", has(res, "Block bad nets", Approximated, "direction \"out\""))
	p = policy(c, "kids-bed-time")
	check("scheduled rule", p != nil && p.Schedule == "schoolnights")
	check("reject URL alias → feed blocks on WAN", feedNamed(c, "bad_ips-feed") != nil && feedNamed(c, "bad_ips-feed").BlockWAN)
	p = policy(c, "lan-to-nas-web")
	check("port alias → services for tcp and udp", p != nil && len(p.Service) == 2 && p.Service[0] == "tcp-web_ports")
	check("source port rule skipped", has(res, "mDNS from port", Skipped, "source ports"))
	check("negated address skipped", has(res, "Not to IoT", Skipped, "negated"))
	check("policy routing gateway approximated", has(res, "Via VPN", Approximated, "VPN_GW"))
	check("disabled rule stays disabled", policy(c, "iot-ping") != nil && !policy(c, "iot-ping").Enabled)
	check("OpenVPN interface rule skipped", has(res, "OpenVPN clients", Skipped, "openvpn"))
	check("NAT filter rule covered by the forward", has(res, "port forward rules", Converted, "1 rule"))
	check("hybrid NAT → nat on internal allow policies", policy(c, "default-allow-lan-to-any-rule").NAT && policy(c, "iot-out").NAT)
	check("static port not needed", has(res, "static port", Converted, "Linux keeps"))
	// port forwards
	check("forward with alias target and port change", forward(c, "nas-ssh") != nil && forward(c, "nas-ssh").ToAddr == "192.168.10.5" && forward(c, "nas-ssh").ToPort == "22")
	check("tcp/udp → two forwards", forward(c, "console-tcp") != nil && forward(c, "console-udp") != nil)
	check("all ports flagged as risk", riskOn(res, "DMZ host"))
	check("shifted range skipped", has(res, "Shifted range", Skipped, "moving the port range"))
	check("source-restricted forward flagged", riskOn(res, "Office only"))
	// services
	check("DHCP with static lease, lowercase MAC", len(c.DHCP) == 2 && c.DHCP[0].StaticLeases[0].MAC == "aa:bb:cc:00:00:05" && c.DHCP[0].LeaseSeconds == 7200)
	check("static map without IP left out", has(res, "lan", Approximated, "without an IP address"))
	check("per-interface DHCP domain approximated", has(res, "opt1", Approximated, "iot.example"))
	check("DNS forwarding to system DNS servers", len(c.DNS.Forwarders) == 2 && c.DNS.DNSSEC)
	check("host override and its alias", c.DNS.LocalRecords["nas.home.example"] == "192.168.10.5" && c.DNS.LocalRecords["files.home.example"] == "192.168.10.5")
	check("domain override with two servers, port dropped", len(c.DNS.DomainForwards) == 1 && len(c.DNS.DomainForwards[0].Servers) == 2 && has(res, "corp.example", Approximated, "port"))
	check("two WAN gateways → multi-WAN gateways", len(c.Gateways) == 2 && c.Gateways[0].Address == "dhcp" && c.Gateways[1].Address == "198.51.100.1")
	check("gateway group with tiers, unknown member left out", len(c.GatewayGroups) == 1 && len(c.GatewayGroups[0].Members) == 2 &&
		c.GatewayGroups[0].Members[1].Tier == 2 && has(res, "Failover", Approximated, "GONE_GW left out"))
	check("static route via the backup gateway", len(c.Routes) == 1 && c.Routes[0].Gateway == "backup_gw")
	check("rule gateway that is not converted is reported", has(res, "Via VPN", Approximated, "VPN_GW"))
	check("UPnP on lan and iot", c.UPnP.Enabled && len(c.UPnP.Interfaces) == 2 && len(c.UPnP.Allow) == 2 && has(res, "UPnP", Approximated, "deny entry"))
	check("inline Suricata → prevent profile on LAN policies", len(c.Profiles.IPS) == 1 && c.Profiles.IPS[0].Mode == "prevent" && policy(c, "default-allow-lan-to-any-rule").IPSProfile == "imported")
	check("pfBlocker IP list → feed on internet policies", feedNamed(c, "threats-abuse-ip") != nil && feedNamed(c, "threats-abuse-ip").BlockWAN && contains(policy(c, "iot-out").ThreatFeeds, "threats-abuse-ip"))
	check("disabled pfBlocker row not imported", feedNamed(c, "threats-old-ip") == nil)
	check("permit list skipped", has(res, "allow", Skipped, "Permit_Outbound"))
	check("DNSBL → DNS filter profile", len(c.Profiles.DNSFilter) == 1 && policy(c, "default-allow-lan-to-any-rule").DNSFilterProfile == "pfblockerng")
	check("OpenVPN server and client skipped", has(res, "OpenVPN server Road warrior", Skipped, "WireGuard") && has(res, "OpenVPN client VPN provider", Skipped, "WireGuard client"))
	check("certificates never imported", has(res, "certificates", Skipped, "never imported"))
	check("unknown package listed", has(res, "ntopng", Skipped, "no counterpart"))
}

// TestDevices: a device assignment fills the interface's device.
// TestTrackWithoutUplinkV6: LAN-side interfaces that track a WAN with no
// IPv6 of its own get no prefix in pfSense, so IPv6 stays off (not turned on
// for the WAN behind the user's back).
func TestTrackWithoutUplinkV6(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "sample.xml"))
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("<ipaddrv6>dhcp6</ipaddrv6>"), nil, 1)
	res, err := Import(data, Options{})
	if err != nil {
		t.Fatal(err)
	}
	c := res.Config
	if v6 := c.Interfaces[0].IPv6; v6 != nil && v6.Mode != "none" && v6.Mode != "" {
		t.Errorf("wan IPv6 turned on: %+v", v6)
	}
	if c.Interfaces[1].IPv6 != nil || c.VLANs[0].IPv6 != nil {
		t.Errorf("trackers kept IPv6: lan %+v, vlan %+v", c.Interfaces[1].IPv6, c.VLANs[0].IPv6)
	}
	if !has(res, "lan", Approximated, "had no IPv6 in pfSense") {
		t.Error("not reported")
	}
}

func TestDevices(t *testing.T) {
	res := importSample(t, map[string]string{"wan": "enp1s0", "opt2": "enp3s0"})
	if res.Config.Interfaces[0].Device != "enp1s0" {
		t.Errorf("wan device %q", res.Config.Interfaces[0].Device)
	}
	for _, m := range res.Interfaces {
		if m.PfSense == "opt2" && m.Device != "enp3s0" {
			t.Errorf("opt2 mapping %+v", m)
		}
	}
	if has(res, "opt2", Approximated, "choose which network port") {
		t.Error("an assigned interface still asks for a device")
	}
}

func TestNotPfSense(t *testing.T) {
	for _, data := range []string{"", "<html></html>", "<pfsense><version>1</version></pfsense>"} {
		if _, err := Import([]byte(data), Options{}); err == nil {
			t.Errorf("%q: want an error", data)
		}
	}
}

func policy(c *config.Config, name string) *config.Policy {
	for i := range c.Policies {
		if c.Policies[i].Name == name {
			return &c.Policies[i]
		}
	}
	return nil
}

func forward(c *config.Config, name string) *config.PortForward {
	for i := range c.NAT.PortForwards {
		if c.NAT.PortForwards[i].Name == name {
			return &c.NAT.PortForwards[i]
		}
	}
	return nil
}

func feedNamed(c *config.Config, name string) *config.Feed {
	for i := range c.Feeds {
		if c.Feeds[i].Name == name {
			return &c.Feeds[i]
		}
	}
	return nil
}

func groupHas(c *config.Config, name string, n int) bool {
	for _, g := range c.AddressGroups {
		if g.Name == name {
			return len(g.Members) == n
		}
	}
	return false
}

func groupHasMember(c *config.Config, name, member string) bool {
	for _, g := range c.AddressGroups {
		if g.Name == name {
			return contains(g.Members, member)
		}
	}
	return false
}

func addrType(c *config.Config, name string) string {
	for _, a := range c.Addresses {
		if a.Name == name {
			return a.Type
		}
	}
	return ""
}

func riskOn(res *Result, item string) bool {
	for _, e := range find(res, item) {
		if e.Risk {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
