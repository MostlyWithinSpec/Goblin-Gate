package pfsense

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func importOPN(t *testing.T) *Result {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "opnsense.xml"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := Import(data, Options{Devices: map[string]string{"opt2": "eth2", "opt3": "eth3"}})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// TestOPNsenseGolden compares the OPNsense sample's conversion with
// testdata/opnsense.config.json and opnsense.report.txt.
// Regenerate with: go test ./internal/importer/pfsense -update
func TestOPNsenseGolden(t *testing.T) {
	res := importOPN(t)
	cfgJSON, _ := json.MarshalIndent(res.Config, "", "  ")
	var rep strings.Builder
	for _, e := range res.Report {
		fmt.Fprintf(&rep, "%s | %s | %s | %s\n", e.Section, e.Status, e.Item, e.Detail)
	}
	for _, m := range res.Interfaces {
		fmt.Fprintf(&rep, "interface %s (%s) -> %s role=%s vlan=%v device=%q\n", m.PfSense, m.PfSenseDevice, m.Name, m.Role, m.VLAN, m.Device)
	}
	for _, e := range res.Errors {
		fmt.Fprintf(&rep, "validation: %s\n", e)
	}
	for name, got := range map[string][]byte{"opnsense.config.json": append(cfgJSON, '\n'), "opnsense.report.txt": []byte(rep.String())} {
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

func TestOPNsenseNoSecrets(t *testing.T) {
	res := importOPN(t)
	out, _ := json.Marshal(res.Config)
	rep, _ := json.Marshal(res.Report)
	for _, blob := range [][]byte{out, rep} {
		if strings.Contains(string(blob), "SHOULD-NOT-APPEAR") || strings.Contains(string(blob), "feeduser") {
			t.Fatalf("a secret was imported:\n%s", blob)
		}
	}
	if len(res.Errors) > 0 {
		t.Fatalf("validation errors: %v", res.Errors)
	}
}

func TestOPNsenseConversion(t *testing.T) {
	res := importOPN(t)
	c := res.Config
	byName := map[string]config.Policy{}
	for _, p := range c.Policies {
		byName[p.Name] = p
	}

	// newer-format rules keep OPNsense's sequence order, after the legacy rule
	var names []string
	for _, p := range c.Policies {
		names = append(names, p.Name)
	}
	want := []string{"legacy-lan-to-any", "iot-may-not-reach-lan", "iot-to-internet", "nas-from-home-countries", "disabled-test-rule"}
	if !slices.Equal(names, want) {
		t.Fatalf("policies %v, want %v", names, want)
	}
	if p := byName["iot-may-not-reach-lan"]; p.Action != "deny" || !slices.Equal(p.DstAddr, []string{"lan-net"}) {
		t.Errorf("block rule %+v", p)
	}
	if p := byName["nas-from-home-countries"]; !slices.Equal(p.SrcAddr, []string{"Home_Countries"}) || p.Log != "all" {
		t.Errorf("geoip rule %+v", p)
	}
	if p := byName["iot-to-internet"]; p.Gateway == "" || !p.NAT {
		t.Errorf("policy routing / NAT lost: %+v", p)
	}
	if byName["disabled-test-rule"].Enabled {
		t.Error("a disabled rule came back enabled")
	}

	// "0" flags are off, not on
	for _, i := range c.Interfaces {
		if i.Name == "spare" && i.Enabled {
			t.Error("<enable>0</enable> interface enabled")
		}
	}
	if len(c.NAT.PortForwards) != 1 || c.NAT.PortForwards[0].Name != "plex" {
		t.Errorf("port forwards %+v (the disabled one must not come back)", c.NAT.PortForwards)
	}
	if len(c.Gateways) != 2 || c.DefaultGateway != "fiber_gw" || len(c.GatewayGroups) != 1 {
		t.Errorf("gateways %+v default %q groups %+v", c.Gateways, c.DefaultGateway, c.GatewayGroups)
	}
	if len(c.Routes) != 1 || c.Routes[0].Destination.String() != "10.50.0.0/16" {
		t.Errorf("routes %+v (the disabled one must not come back)", c.Routes)
	}

	// Kea subnets land on the interfaces whose network they are
	if len(c.DHCP) != 2 {
		t.Fatalf("dhcp %+v", c.DHCP)
	}
	iot := c.DHCP[1]
	if iot.Interface != "iot" || iot.RangeStart.String() != "192.168.20.129" || iot.RangeEnd.String() != "192.168.20.254" ||
		iot.LeaseSeconds != 7200 || len(iot.StaticLeases) != 1 || iot.StaticLeases[0].Hostname != "thermostat" {
		t.Errorf("iot dhcp %+v", iot)
	}

	// Unbound: forwarders from its own entries, host and domain overrides
	if len(c.DNS.Forwarders) != 1 || c.DNS.Forwarders[0].String() != "1.1.1.1" || !c.DNS.DNSSEC {
		t.Errorf("dns %+v", c.DNS)
	}
	if c.DNS.LocalRecords["nas.home.arpa"] != "192.168.10.20" || len(c.DNS.DomainForwards) != 1 {
		t.Errorf("dns overrides %+v", c.DNS)
	}

	// what can't be converted is in the report, worded for OPNsense
	for _, item := range []string{"Kid_Tablets", "tag for shaper", "mail.home.arpa", "IDS", "wireguard", "Kea subnet 172.16.0.0/24", "IoT out the cable line"} {
		if len(find(res, item)) == 0 {
			t.Errorf("no report line for %q", item)
		}
	}
	for _, e := range res.Report {
		if strings.Contains(e.Detail, "pfSense") {
			t.Errorf("report names pfSense: %+v", e)
		}
	}
}

func TestNotAFirewallConfig(t *testing.T) {
	for _, doc := range []string{`<foo><interfaces><lan/></interfaces></foo>`, `<opnsense></opnsense>`, `not xml`} {
		if _, err := Import([]byte(doc), Options{}); err == nil {
			t.Errorf("%q accepted", doc)
		}
	}
}
