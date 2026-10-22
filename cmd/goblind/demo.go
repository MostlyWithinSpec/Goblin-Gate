package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/devices"
	"github.com/networkgoblinlabs/goblin-gate/internal/logs"
)

// Demo mode (task 116): --demo runs a public, self-resetting showcase. It is
// --dev (fake applier, no login, plain HTTP behind a proxy) plus:
//   - a fresh config directory per run, seeded with a home network
//     (demoConfig) and a device inventory, removed on exit;
//   - goblind exits after --demo-reset (default 1 h) so the service manager
//     starts it again from the seed;
//   - nothing reaches out: the workers see the running config without
//     feeds, notification channels, dynamic DNS, log export, git push,
//     CrowdSec or a UPS server (demoScrub), and Go's default HTTP transport
//     refuses every connection besides;
//   - routes about accounts and credentials, uploads, and actions that
//     touch real networks answer 403 (demoDenied);
//   - synthetic traffic, IPS and DNS events keep the dashboards alive.

// demoDenied lists what the demo refuses, as "METHOD /path" prefixes.
var demoDenied = []string{
	"POST /api/v1/admins", "PATCH /api/v1/admins", "DELETE /api/v1/admins",
	"POST /api/v1/auth/tokens", "DELETE /api/v1/auth/tokens",
	"POST /api/v1/auth/password", "POST /api/v1/auth/2fa", "DELETE /api/v1/auth/sessions",
	"POST /api/v1/system/restore", "POST /api/v1/import/",
	"POST /api/v1/system/updates/apply", "POST /api/v1/system/reboot",
	"POST /api/v1/plugins/", "PUT /api/v1/plugins/",
	"POST /api/v1/capture",
	"POST /api/v1/diag/",
	"POST /api/v1/devices/", // wake-on-LAN
	"POST /api/v1/git-backup/",
	"POST /api/v1/notifications/test",
	"POST /api/v1/feeds/", "POST /api/v1/geoip/refresh", "POST /api/v1/ddns/",
	"POST /api/v1/vpn/tailscale/",
	"PUT /api/v1/vpn/wireguard-clients/",
}

// demoGuard refuses the demoDenied requests.
func demoGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := r.Method + " " + r.URL.Path
		for _, d := range demoDenied {
			if strings.HasPrefix(req, d) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
					"code": "demo", "message": "not available in the demo: install Goblin Gate to try this"}})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// blockOutbound makes Go's default HTTP transport refuse every connection.
func blockOutbound() {
	http.DefaultTransport = &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("the demo makes no outside connections")
		},
	}
}

// demoScrub is the running config as the background workers see it in the
// demo: without anything that would contact another host.
func demoScrub(c *config.Config) *config.Config {
	c = c.Clone()
	c.Feeds = nil
	c.Notifications.Channels = nil
	c.DDNS = nil
	c.LogExport = nil
	c.GitBackup = config.GitBackup{}
	c.CrowdSec = config.CrowdSec{}
	c.UPS = config.UPS{}
	c.GeoIP.SourceURL = ""
	c.System.UpdateURL = ""
	c.IPS = config.IPSSettings{}
	return c
}

// seedDemo fills a fresh config directory: the demo network as the first
// revision (and candidate), and the device inventory.
func seedDemo(store *config.Store, dataDir string, now time.Time) error {
	cfg := demoConfig()
	if errs := config.Validate(cfg); len(errs) > 0 {
		return fmt.Errorf("demo config: %v", errs)
	}
	if _, err := store.SaveRevision(cfg, "demo", "the demo network"); err != nil {
		return err
	}
	if err := store.SetCandidate(cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(demoDevices(now), "", "  ")
	return os.WriteFile(filepath.Join(dataDir, "devices.json"), b, 0o600)
}

func addr(s string) *netip.Addr     { a := netip.MustParseAddr(s); return &a }
func prefix(s string) *netip.Prefix { p := netip.MustParsePrefix(s); return &p }

// demoConfig is a believable home network: a LAN, IoT and guest VLANs, a
// NAS reachable from home countries only, a kids' bedtime, the usual
// profiles. Its admin's password is random: nobody logs in (no login in
// the demo), and the setup wizard counts it as done.
func demoConfig() *config.Config {
	c := config.Default()
	c.System.Hostname = "goblin-demo"
	c.System.Domain = "home.arpa"
	c.System.Timezone = "America/Chicago"
	c.Admins = []config.Admin{{Username: "admin", Role: "admin", PasswordHash: "$argon2id$v=19$m=65536,t=3,p=2$ZGVtby1ub2xvZ2lu$ZGVtby1ub2xvZ2luLWRlbW8tbm9sb2dpbg"}}
	c.Interfaces[0].Description = "Fiber"
	c.Interfaces[1].Address = prefix("192.168.10.1/24")
	c.Interfaces[1].AllowAccess = []string{"ping", "https", "ssh"}
	c.VLANs = []config.VLAN{
		{Name: "iot", Parent: "lan", VLANID: 20, Mode: "static", Address: prefix("192.168.20.1/24"), Enabled: true, Description: "Smart home", AllowAccess: []string{"ping"}},
		{Name: "guest", Parent: "lan", VLANID: 50, Mode: "static", Address: prefix("192.168.50.1/24"), Enabled: true, Description: "Guest Wi-Fi", AllowAccess: []string{"ping"}},
	}
	c.DHCP = []config.DHCPServer{
		{Interface: "lan", RangeStart: netip.MustParseAddr("192.168.10.100"), RangeEnd: netip.MustParseAddr("192.168.10.199"), LeaseSeconds: 86400,
			DNS: []netip.Addr{netip.MustParseAddr("192.168.10.1")}, Gateway: addr("192.168.10.1"),
			StaticLeases: []config.DHCPStaticLease{
				{MAC: "00:11:32:aa:bb:01", IP: netip.MustParseAddr("192.168.10.20"), Hostname: "nas"},
				{MAC: "b8:27:eb:12:34:56", IP: netip.MustParseAddr("192.168.10.21"), Hostname: "homeassistant"},
			}},
		{Interface: "iot", RangeStart: netip.MustParseAddr("192.168.20.100"), RangeEnd: netip.MustParseAddr("192.168.20.199"), LeaseSeconds: 86400,
			DNS: []netip.Addr{netip.MustParseAddr("192.168.20.1")}, Gateway: addr("192.168.20.1")},
		{Interface: "guest", RangeStart: netip.MustParseAddr("192.168.50.100"), RangeEnd: netip.MustParseAddr("192.168.50.199"), LeaseSeconds: 3600,
			DNS: []netip.Addr{netip.MustParseAddr("192.168.50.1")}, Gateway: addr("192.168.50.1")},
	}
	c.Addresses = []config.Address{
		{Name: "nas", Type: "host", Value: "192.168.10.20", Comment: "Synology"},
		{Name: "homeassistant", Type: "host", Value: "192.168.10.21"},
		{Name: "kids-tablet", Type: "host", Value: "192.168.10.140"},
		{Name: "kids-console", Type: "host", Value: "192.168.10.141"},
		{Name: "home-countries", Type: "country", Value: "US,CA"},
	}
	c.AddressGroups = []config.AddressGroup{{Name: "kids-devices", Members: []string{"kids-tablet", "kids-console"}}}
	c.Schedules = []config.Schedule{{Name: "bedtime", Ranges: []config.ScheduleRange{
		{Days: []string{"sun", "mon", "tue", "wed", "thu"}, Start: "21:00", End: "07:00"}}}}
	c.Profiles.IPS = []config.IPSProfile{{Name: "default"}}
	c.Profiles.DNSFilter = []config.DNSFilterProfile{{Name: "family"}}
	all, any := []string{"all"}, []string{"all"}
	c.Policies = []config.Policy{
		{ID: 1, Name: "kids-bedtime", Enabled: true, SrcIntf: []string{"lan"}, DstIntf: []string{"wan1"}, SrcAddr: []string{"kids-devices"},
			DstAddr: all, Service: any, Action: "deny", Schedule: "bedtime", Log: "all"},
		{ID: 2, Name: "lan-to-internet", Enabled: true, SrcIntf: []string{"lan"}, DstIntf: []string{"wan1"}, SrcAddr: all, DstAddr: all,
			Service: any, Action: "accept", NAT: true, IPSProfile: "default"},
		{ID: 3, Name: "lan-to-iot", Enabled: true, SrcIntf: []string{"lan"}, DstIntf: []string{"iot"}, SrcAddr: all, DstAddr: all,
			Service: any, Action: "accept"},
		{ID: 4, Name: "iot-to-internet", Enabled: true, SrcIntf: []string{"iot"}, DstIntf: []string{"wan1"}, SrcAddr: all, DstAddr: all,
			Service: any, Action: "accept", NAT: true, IPSProfile: "default", Log: "all"},
		{ID: 5, Name: "guest-internet", Enabled: true, SrcIntf: []string{"guest"}, DstIntf: []string{"wan1"}, SrcAddr: all, DstAddr: all,
			Service: any, Action: "accept", NAT: true, DNSFilterProfile: "family"},
	}
	c.NAT.PortForwards = []config.PortForward{{Name: "nas-https", InIntf: "wan1", ExtPort: "8443", Protocol: "tcp",
		ToAddr: "192.168.10.20", ToPort: "443", Source: []string{"home-countries"}}}
	return c
}

// demoDevice is one machine of the demo network.
type demoDevice struct {
	mac, ip, host, vendor, iface string
}

var demoMachines = []demoDevice{
	{"00:11:32:aa:bb:01", "192.168.10.20", "nas", "Synology", "lan"},
	{"b8:27:eb:12:34:56", "192.168.10.21", "homeassistant", "Raspberry Pi", "lan"},
	{"3c:22:fb:10:20:30", "192.168.10.101", "macbook", "Apple", "lan"},
	{"f4:5c:89:01:02:03", "192.168.10.102", "iphone", "Apple", "lan"},
	{"a4:83:e7:44:55:66", "192.168.10.140", "kids-tablet", "Apple", "lan"},
	{"7c:bb:8a:77:88:99", "192.168.10.141", "kids-console", "Nintendo", "lan"},
	{"d8:31:34:aa:00:01", "192.168.20.101", "living-room-tv", "Roku", "iot"},
	{"70:ee:50:12:ab:cd", "192.168.20.102", "thermostat", "Netatmo", "iot"},
	{"2c:aa:8e:33:44:55", "192.168.20.103", "doorbell", "Wyze", "iot"},
	{"68:57:2d:66:77:88", "192.168.20.104", "smart-plug", "Tuya", "iot"},
	{"9a:4c:12:de:ad:01", "192.168.50.101", "", "", "guest"},
}

func demoDevices(now time.Time) []devices.Device {
	var out []devices.Device
	for i, m := range demoMachines {
		out = append(out, devices.Device{
			MAC: m.mac, IPs: []string{m.ip}, Hostname: m.host, Vendor: m.vendor, Interface: m.iface,
			FirstSeen: now.Add(-time.Duration(30-i) * 24 * time.Hour), LastSeen: now.Add(-time.Duration(i) * time.Minute),
			Randomized: m.vendor == "",
		})
	}
	return out
}

// demoEvents makes a minute of believable traffic: web browsing from the
// LAN, IoT devices calling home, a guest, a blocked bedtime attempt, some
// scans dropped at the WAN, IPS alerts and DNS filter blocks.
func demoEvents(now time.Time, r *rand.Rand) []*logs.Event {
	pick := func(xs []string) string { return xs[r.IntN(len(xs))] }
	t := func() int64 { return now.Add(-time.Duration(r.IntN(60)) * time.Second).Unix() }
	ext := func() string {
		firsts := []int{104, 142, 151, 172, 185, 52, 34, 13}
		return fmt.Sprintf("%d.%d.%d.%d", firsts[r.IntN(len(firsts))], r.IntN(256), r.IntN(256), 1+r.IntN(254))
	}
	var out []*logs.Event
	lan := []string{"192.168.10.101", "192.168.10.102", "192.168.10.20", "192.168.10.21"}
	iot := []string{"192.168.20.101", "192.168.20.102", "192.168.20.103", "192.168.20.104"}
	apps := []string{"Netflix", "YouTube", "Zoom", "Steam", "iCloud", "Spotify", "GitHub"}
	for range 20 + r.IntN(20) {
		out = append(out, &logs.Event{Time: t(), Kind: "traffic", Action: "accept", PolicyID: "2", SrcIP: pick(lan), SrcPort: 40000 + r.IntN(20000),
			DstIP: ext(), DstPort: []int{443, 443, 443, 80, 53, 993}[r.IntN(6)], Proto: "tcp", InIntf: "lan", OutIntf: "wan1",
			Bytes: 2000 + r.IntN(4_000_000), App: pick(apps)})
	}
	for range 8 + r.IntN(8) {
		out = append(out, &logs.Event{Time: t(), Kind: "traffic", Action: "accept", PolicyID: "4", SrcIP: pick(iot), SrcPort: 40000 + r.IntN(20000),
			DstIP: ext(), DstPort: []int{443, 8883, 123}[r.IntN(3)], Proto: "tcp", InIntf: "iot", OutIntf: "wan1", Bytes: 500 + r.IntN(50_000)})
	}
	for range 3 + r.IntN(6) {
		out = append(out, &logs.Event{Time: t(), Kind: "traffic", Action: "deny", SrcIP: ext(), SrcPort: 1024 + r.IntN(60000),
			DstIP: "198.51.100.23", DstPort: []int{22, 23, 3389, 445, 5900, 8080}[r.IntN(6)], Proto: "tcp", InIntf: "wan1"})
	}
	if r.IntN(3) == 0 {
		out = append(out, &logs.Event{Time: t(), Kind: "traffic", Action: "deny", PolicyID: "1", SrcIP: "192.168.10.141", SrcPort: 50000 + r.IntN(10000),
			DstIP: ext(), DstPort: 443, Proto: "tcp", InIntf: "lan", OutIntf: "wan1"})
	}
	if r.IntN(4) == 0 {
		sigs := []struct{ sig, sev string }{
			{"2010935: ET SCAN Suspicious inbound to MSSQL port 1433", "2"},
			{"2024897: ET USER_AGENTS Go HTTP Client User-Agent", "3"},
			{"2027865: ET INFO Observed DNS Query to .cloud TLD", "3"},
			{"2013028: ET POLICY curl User-Agent Outbound", "3"},
			{"2019401: ET POLICY SSLv3 outbound connection from client vulnerable to POODLE attack", "2"},
		}
		s := sigs[r.IntN(len(sigs))]
		out = append(out, &logs.Event{Time: t(), Kind: "ips", Action: []string{"alert", "drop"}[r.IntN(2)], SrcIP: pick(iot), DstIP: ext(),
			DstPort: 443, Proto: "tcp", Signature: s.sig, Severity: s.sev})
	}
	for range r.IntN(4) {
		out = append(out, &logs.Event{Time: t(), Kind: "webfilter", Action: "deny", SrcIP: "192.168.50.101", App: "family",
			Message: pick([]string{"ads.example-tracker.com", "telemetry.example.net", "malware-c2.example.org", "doubleclick.example"}), Category: pick([]string{"ads", "tracking", "malware"})})
	}
	return out
}

// runDemoEvents adds a minute of events every minute, after an hour of
// history so the charts aren't empty at the start.
func runDemoEvents(ctx context.Context, store *logs.Store) {
	r := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 116))
	now := time.Now()
	for m := 60; m > 0; m-- {
		store.InsertBatch(demoEvents(now.Add(-time.Duration(m)*time.Minute), r))
	}
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-tick.C:
			store.InsertBatch(demoEvents(t, r))
		}
	}
}
