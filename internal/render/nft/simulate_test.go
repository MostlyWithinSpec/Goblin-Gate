package nft

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/rendertest"
)

// simConfig: LAN 192.168.1.0/24 (https, ssh, ping), IoT VLAN 192.168.20.0/24
// (ping only) isolated from the LAN except Home Assistant, a kids' night
// schedule, a port forward and a threat feed.
func simConfig() *config.Config {
	cfg := config.Default()
	cfg.System.Timezone = "UTC"
	cfg.Interfaces[1].AllowAccess = []string{"https", "ssh", "ping"}
	iot := netip.MustParsePrefix("192.168.20.1/24")
	cfg.VLANs = []config.VLAN{{Name: "iot", Parent: "lan", VLANID: 20, Mode: "static", Address: &iot, Enabled: true, AllowAccess: []string{"ping"}}}
	cfg.DHCP = append(cfg.DHCP, config.DHCPServer{Interface: "iot", RangeStart: netip.MustParseAddr("192.168.20.100"), RangeEnd: netip.MustParseAddr("192.168.20.199")})
	cfg.Addresses = []config.Address{
		{Name: "lan-net", Type: "subnet", Value: "192.168.1.0/24"},
		{Name: "iot-net", Type: "subnet", Value: "192.168.20.0/24"},
		{Name: "ha", Type: "host", Value: "192.168.1.23"},
		{Name: "kid-pc", Type: "host", Value: "192.168.1.50"},
	}
	cfg.Services = []config.Service{{Name: "ha-web", Protocol: "tcp", Ports: "8123"}}
	cfg.Schedules = []config.Schedule{{Name: "night", Ranges: []config.ScheduleRange{{Days: []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}, Start: "22:00", End: "24:00"}}}}
	cfg.Feeds = []config.Feed{{Name: "drop", Type: "ip", URL: "https://example.org/drop.txt", Enabled: true, BlockWAN: true}}
	cfg.NAT.PortForwards = []config.PortForward{{Name: "plex", InIntf: "wan1", ExtPort: "32400", Protocol: "tcp", ToAddr: "192.168.1.50"}}
	cfg.Policies = []config.Policy{
		{ID: 1, Name: "iot-to-ha", Enabled: true, SrcIntf: []string{"iot"}, DstIntf: []string{"all"}, SrcAddr: []string{"iot-net"}, DstAddr: []string{"ha"}, Service: []string{"ha-web"}, Action: "accept"},
		{ID: 2, Name: "iot-no-lan", Enabled: true, SrcIntf: []string{"iot"}, DstIntf: []string{"all"}, SrcAddr: []string{"iot-net"}, DstAddr: []string{"lan-net"}, Service: []string{"any"}, Action: "deny", Log: "deny"},
		{ID: 3, Name: "iot-out", Enabled: true, SrcIntf: []string{"iot"}, DstIntf: []string{"wan1"}, SrcAddr: []string{"iot-net"}, DstAddr: []string{"all"}, Service: []string{"any"}, Action: "accept", NAT: true},
		{ID: 4, Name: "old", Enabled: false, SrcIntf: []string{"lan"}, DstIntf: []string{"all"}, SrcAddr: []string{"all"}, DstAddr: []string{"all"}, Service: []string{"any"}, Action: "deny"},
		{ID: 5, Name: "kid-bedtime", Enabled: true, SrcIntf: []string{"lan"}, DstIntf: []string{"wan1"}, SrcAddr: []string{"kid-pc"}, DstAddr: []string{"all"}, Service: []string{"any"}, Action: "deny", Schedule: "night"},
		{ID: 6, Name: "lan-out", Enabled: true, SrcIntf: []string{"lan"}, DstIntf: []string{"all"}, SrcAddr: []string{"lan-net"}, DstAddr: []string{"all"}, Service: []string{"any"}, Action: "accept", NAT: true, ThreatFeeds: []string{"drop"}, IPSProfile: "ids"},
	}
	cfg.Profiles.IPS = []config.IPSProfile{{Name: "ids", Mode: "detect"}}
	return cfg
}

var noon = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func runSim(t *testing.T, cfg *config.Config, rt SimRuntime, src, dst, proto string, port uint16, at time.Time) *SimResult {
	t.Helper()
	res, err := Simulate(cfg, SimFlow{SrcIP: netip.MustParseAddr(src), DstIP: netip.MustParseAddr(dst), Protocol: proto, DstPort: port, SrcPort: 40000, At: at}, rt)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestSimulate(t *testing.T) {
	cfg := simConfig()
	type want struct {
		verdict, chain string
		policy         uint32 // 0 = none
		reason         string // substring
	}
	for _, tc := range []struct {
		name            string
		src, dst, proto string
		port            uint16
		at              time.Time
		want            want
	}{
		{"iot reaches HA", "192.168.20.5", "192.168.1.23", "tcp", 8123, noon, want{"accept", "forward", 1, "accepted by policy 1"}},
		{"iot ssh to HA is isolated", "192.168.20.5", "192.168.1.23", "tcp", 22, noon, want{"deny", "forward", 2, "denied by policy 2"}},
		{"iot to the internet", "192.168.20.5", "93.184.216.34", "tcp", 443, noon, want{"accept", "forward", 3, "policy 3"}},
		{"kid at noon", "192.168.1.50", "93.184.216.34", "tcp", 443, noon, want{"accept", "forward", 6, "policy 6"}},
		{"kid at night", "192.168.1.50", "93.184.216.34", "tcp", 443, noon.Add(11 * time.Hour), want{"deny", "forward", 5, "policy 5"}},
		{"port forward from the internet", "203.0.113.9", "198.51.100.2", "tcp", 32400, noon, want{"accept", "forward", 0, "port forward plex"}},
		{"ssh from the internet", "203.0.113.9", "198.51.100.2", "tcp", 22, noon, want{"deny", "input", 0, "does not allow it"}},
		{"web UI from the LAN", "192.168.1.10", "192.168.1.1", "tcp", 443, noon, want{"accept", "input", 0, "lan: https admin"}},
		{"web UI from iot", "192.168.20.5", "192.168.20.1", "tcp", 443, noon, want{"deny", "input", 0, "allowed access on iot: ping"}},
		{"ping the box from iot", "192.168.20.5", "192.168.20.1", "icmp", 0, noon, want{"accept", "input", 0, "iot: ping"}},
		{"DNS from iot", "192.168.20.5", "192.168.20.1", "udp", 53, noon, want{"accept", "input", 0, "iot: dns"}},
		{"iot to an unlisted network", "192.168.20.5", "10.9.9.9", "tcp", 80, noon, want{"accept", "forward", 3, "policy 3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := runSim(t, cfg, SimRuntime{}, tc.src, tc.dst, tc.proto, tc.port, tc.at)
			got := want{res.Verdict, res.Chain, 0, res.Reason}
			if res.Policy != nil {
				got.policy = res.Policy.ID
			}
			if got.verdict != tc.want.verdict || got.chain != tc.want.chain || got.policy != tc.want.policy || !strings.Contains(res.Reason, tc.want.reason) {
				t.Fatalf("got %+v, want %+v\nsteps %+v\nnotes %v", res, tc.want, res.Steps, res.Notes)
			}
		})
	}
}

func TestSimulateDetails(t *testing.T) {
	cfg := simConfig()

	// steps explain each earlier policy; the disabled one says so
	res := runSim(t, cfg, SimRuntime{}, "192.168.20.5", "192.168.1.23", "tcp", 22, noon)
	if len(res.Steps) != 2 || res.Steps[0].ID != 1 || res.Steps[0].Matched || !strings.Contains(res.Steps[0].Reason, `tcp port 22 is not in service "ha-web"`) || !res.Steps[1].Matched {
		t.Fatalf("steps %+v", res.Steps)
	}
	res = runSim(t, cfg, SimRuntime{}, "192.168.1.50", "93.184.216.34", "tcp", 443, noon)
	reasons := map[uint32]string{}
	for _, s := range res.Steps {
		reasons[s.ID] = s.Reason
	}
	if reasons[1] == "" || !strings.Contains(reasons[1], "source interface lan is not iot") || reasons[4] != "disabled" ||
		!strings.Contains(reasons[5], "outside the schedule") || reasons[6] != "matches" {
		t.Fatalf("reasons %v", reasons)
	}
	if !res.NAT || res.DstIntf != "wan1" || res.Profiles["ips"] != "ids" {
		t.Fatalf("details %+v", res)
	}

	// port forward: translated, then accepted by its own rule
	res = runSim(t, cfg, SimRuntime{}, "203.0.113.9", "198.51.100.2", "tcp", 32400, noon)
	if !strings.Contains(res.Translated, "192.168.1.50") || res.DstIntf != "lan" || res.SrcIntf != "wan1" || len(res.Notes) == 0 {
		t.Fatalf("port forward %+v", res)
	}

	// temporary block and threat feeds from the runtime
	blocked := SimRuntime{Blocked: func(a netip.Addr) bool { return a == netip.MustParseAddr("93.184.216.34") }}
	res = runSim(t, cfg, blocked, "192.168.1.10", "93.184.216.34", "tcp", 443, noon)
	if res.Verdict != "deny" || res.BlockedBy == nil || res.BlockedBy.Kind != "temp_block" {
		t.Fatalf("temp block %+v", res)
	}
	feed := SimRuntime{InFeed: func(f string, a netip.Addr) bool { return f == "drop" && a == netip.MustParseAddr("6.6.6.6") }}
	res = runSim(t, cfg, feed, "192.168.1.10", "6.6.6.6", "tcp", 443, noon)
	if res.Verdict != "deny" || res.BlockedBy == nil || res.BlockedBy.Kind != "feed" || res.BlockedBy.Name != "drop" {
		t.Fatalf("feed %+v", res)
	}

	// the candidate's own errors and bad flows
	if _, err := Simulate(cfg, SimFlow{SrcIP: netip.MustParseAddr("192.168.1.10"), DstIP: netip.MustParseAddr("1.1.1.1"), Protocol: "tcp"}, SimRuntime{}); err == nil {
		t.Error("tcp without a port accepted")
	}
	if _, err := Simulate(cfg, SimFlow{SrcIP: netip.MustParseAddr("192.168.1.10"), DstIP: netip.MustParseAddr("2001:db8::1"), Protocol: "icmp"}, SimRuntime{}); err == nil {
		t.Error("mixed families accepted")
	}
	if _, err := Simulate(cfg, SimFlow{SrcIP: netip.MustParseAddr("192.168.1.10"), DstIP: netip.MustParseAddr("1.1.1.1"), Protocol: "icmp", SrcIntf: "ghost"}, SimRuntime{}); err == nil {
		t.Error("unknown interface accepted")
	}
}

// TestSimulateReadsEveryRule: the simulator understands every rule the
// renderer writes for the richest test configs (a rule it cannot read
// would make every simulation fail).
func TestSimulateReadsEveryRule(t *testing.T) {
	for name, cfg := range map[string]*config.Config{"full": fullConfig(), "multiwan": rendertest.MultiWAN(), "sim": simConfig()} {
		r, err := prepare(cfg)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		s := newSim(r, SimRuntime{})
		lines := append(append(append(append(append(append([]string{}, r.input...), r.forward...), r.pre...), r.dnsRedir...), r.feedFwd...), r.feedIn...)
		lines = append(append(lines, r.spoof...), r.dnsFwd...)
		for _, proto := range []string{"tcp", "udp", "icmp", ""} {
			for _, fam := range []string{"10.0.0.1", "2001:db8::1"} {
				src := netip.MustParseAddr(fam)
				pk := &packet{iif: "eth1", oif: "eth0", src: src, dst: src.Next(), proto: proto, dport: 443, sport: 1234, at: noon}
				for _, l := range lines {
					if strings.HasPrefix(l, "#") {
						continue
					}
					if _, err := s.eval(l, pk); err != nil {
						t.Errorf("%s: %v", name, err)
					}
				}
			}
		}
	}
}
