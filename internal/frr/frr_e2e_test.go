package frr_test

import (
	"context"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/frr"
	renderfrr "github.com/networkgoblinlabs/goblin-gate/internal/render/frr"
)

// e2eConfig is the gate of the e2e: WAN "wan1" (eth0) 10.99.0.2/24 towards the peer
// (10.99.0.3, AS 65002), LAN "lan0" 192.168.50.1/24 announced by BGP and
// passive in OSPF.
func e2eConfig() *config.Config {
	wan := netip.MustParsePrefix("10.99.0.2/24")
	wan6 := netip.MustParsePrefix("fd99::2/64")
	lan := netip.MustParsePrefix("192.168.50.1/24")
	cfg := config.Default()
	cfg.System.Hostname = "gate"
	cfg.Interfaces[0].Mode, cfg.Interfaces[0].Address = "static", &wan
	cfg.Interfaces[0].Gateway = nil
	cfg.Interfaces[0].IPv6 = &config.IPv6{Mode: "static", Address: &wan6}
	cfg.Interfaces[1].Device, cfg.Interfaces[1].Address = "lan0", &lan
	cfg.DHCP = nil
	cfg.Routing.BGP = config.BGP{Enabled: true, AS: 65001, Announce: []string{"lan"}, Neighbors: []config.BGPNeighbor{
		{Address: netip.MustParseAddr("10.99.0.3"), RemoteAS: 65002, Enabled: true, Description: "lab peer", Password: "s3cret-pw",
			// learn 172.20.0.0/24, refuse 172.21.0.0/24
			Import: []config.PrefixRule{{Action: "deny", Prefix: netip.MustParsePrefix("172.21.0.0/24")}, {Action: "permit", Prefix: netip.MustParsePrefix("172.16.0.0/12"), MaxLength: 24}}},
		{Address: netip.MustParseAddr("fd99::3"), RemoteAS: 65002, Enabled: true},
	}}
	cfg.Routing.OSPF = config.OSPF{Enabled: true, Areas: []config.OSPFArea{{ID: "0", Interfaces: []config.OSPFInterface{{Name: "wan1", Cost: 10}, {Name: "lan", Passive: true}}}}}
	return cfg
}

// TestFRRE2E runs the rendered config in Debian 12's FRR against a second
// FRR in Docker: vtysh's check of good and bad configs, the BGP session (with
// a password, IPv4 and IPv6) and the OSPF adjacency come up, routes are
// learned (and filtered) and announced, a reload keeps the session, and the
// status reader understands FRR's answers. Needs Docker and the image from
// testdata/image (scripts/frr-e2e.ps1 builds it). Skipped without
// GG_FRR_E2E=1; GG_FRR_RECORD=1 also saves FRR's JSON to testdata/recorded.
func TestFRRE2E(t *testing.T) {
	if os.Getenv("GG_FRR_E2E") != "1" {
		t.Skip("GG_FRR_E2E not set (run scripts/frr-e2e.ps1)")
	}
	docker := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	cleanup := func() {
		exec.Command("docker", "rm", "-f", "gg-frr-gate", "gg-frr-peer").Run()
		exec.Command("docker", "network", "rm", "gg-frr").Run()
	}
	cleanup()
	t.Cleanup(cleanup)
	docker("network", "create", "--ipv6", "--subnet", "10.99.0.0/24", "--subnet", "fd99::/64", "gg-frr")
	for name, ips := range map[string][2]string{"gg-frr-gate": {"10.99.0.2", "fd99::2"}, "gg-frr-peer": {"10.99.0.3", "fd99::3"}} {
		docker("run", "-d", "--name", name, "--hostname", strings.TrimPrefix(name, "gg-frr-"), "--network", "gg-frr", "--ip", ips[0], "--ip6", ips[1],
			"--cap-add", "NET_ADMIN", "--cap-add", "SYS_ADMIN", "--sysctl", "net.ipv6.conf.all.disable_ipv6=0",
			"--sysctl", "net.ipv4.ip_forward=1", "--sysctl", "net.ipv6.conf.all.forwarding=1", "gg-frr-test")
	}
	gate := func(args ...string) string { return docker(append([]string{"exec", "gg-frr-gate"}, args...)...) }
	peer := func(args ...string) string { return docker(append([]string{"exec", "gg-frr-peer"}, args...)...) }
	gate("sh", "-c", "ip link add lan0 type dummy && ip addr add 192.168.50.1/24 dev lan0 && ip link set lan0 up")
	peer("sh", "-c", `for n in bgpnet:172.20.0.1/24 bgpnet2:172.21.0.1/24 ospfnet:172.22.0.1/24; do
		ip link add ${n%%:*} type dummy && ip addr add ${n#*:} dev ${n%%:*} && ip link set ${n%%:*} up; done
		ip -6 addr add fd20::1/64 dev bgpnet`)

	// the gate's files, as the applier writes them, checked as it checks them
	dir := t.TempDir()
	cfg := e2eConfig()
	if errs := config.Validate(cfg); len(errs) > 0 {
		t.Fatalf("config: %v", errs)
	}
	files, err := renderfrr.Renderer{}.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	put := func(container, path string, data []byte) {
		t.Helper()
		f := filepath.Join(dir, filepath.Base(path))
		if err := os.WriteFile(f, data, 0o644); err != nil {
			t.Fatal(err)
		}
		docker("cp", f, container+":"+path)
	}
	put("gg-frr-gate", "/tmp/check.conf", files[renderfrr.ConfPath])
	gate("vtysh", "--dryrun", "--inputfile", "/tmp/check.conf")
	put("gg-frr-gate", "/tmp/bad.conf", append(files[renderfrr.ConfPath], []byte("router bgp 65001\n neighbor nonsense remote-as\n")...))
	if out, err := exec.Command("docker", "exec", "gg-frr-gate", "vtysh", "--dryrun", "--inputfile", "/tmp/bad.conf").CombinedOutput(); err == nil {
		t.Fatalf("vtysh --dryrun accepted a bad config:\n%s", out)
	}
	put("gg-frr-gate", renderfrr.ConfPath, files[renderfrr.ConfPath])
	put("gg-frr-gate", renderfrr.DaemonsPath, files[renderfrr.DaemonsPath])
	conf, _ := os.ReadFile("testdata/peer-frr.conf")
	put("gg-frr-peer", "/etc/frr/frr.conf", conf)
	daemons, _ := os.ReadFile("testdata/peer-daemons")
	put("gg-frr-peer", "/etc/frr/daemons", daemons)
	for _, c := range []func(...string) string{gate, peer} {
		c("sh", "-c", "chown frr:frr /etc/frr/frr.conf && /usr/lib/frr/frrinit.sh start")
	}

	run := func(ctx context.Context, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, "docker", append([]string{"exec", "gg-frr-gate", "vtysh"}, args...)...).Output()
	}
	ctx := context.Background()
	var st frr.Status
	ok := func(st frr.Status) bool {
		up := 0
		for _, n := range st.BGP {
			if n.Up {
				up++
			}
		}
		full := len(st.OSPF) == 1 && st.OSPF[0].Full
		have := map[string]bool{}
		for _, r := range st.Routes {
			have[r.Protocol+" "+r.Prefix] = true
		}
		return up == 2 && full && have["bgp 172.20.0.0/24"] && have["bgp fd20::/64"] && have["ospf 172.22.0.0/24"]
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		st, err = frr.Read(ctx, run)
		if err == nil && ok(st) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not converged: %v\n%+v\n%s", err, st, gate("vtysh", "-c", "show bgp summary"))
		}
		time.Sleep(2 * time.Second)
	}
	for _, r := range st.Routes {
		if r.Prefix == "172.21.0.0/24" {
			t.Errorf("the import filter let 172.21.0.0/24 in: %+v", r)
		}
	}
	for _, n := range st.BGP {
		if n.Address == "10.99.0.3" && (n.Description != "lab peer" || n.RemoteAS != 65002 || n.PrefixesReceived != 1 || n.PrefixesSent < 1) {
			t.Errorf("neighbour %+v", n)
		}
	}
	// zebra installed the learned routes into the kernel
	if out := gate("ip", "route", "show", "172.20.0.0/24"); !strings.Contains(out, "via 10.99.0.3") {
		t.Errorf("kernel route: %q", out)
	}
	// the peer learned the gate's LAN
	if out := peer("vtysh", "-c", "show ip bgp 192.168.50.0/24"); !strings.Contains(out, "65001") {
		t.Errorf("peer did not learn the LAN:\n%s", out)
	}

	if os.Getenv("GG_FRR_RECORD") == "1" {
		os.MkdirAll("testdata/recorded", 0o755)
		for name, cmd := range frr.Commands {
			out, err := run(ctx, "-c", cmd)
			if err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join("testdata/recorded", name+".json"), out, 0o644)
		}
	}

	// a commit with a changed config: frr-reload.py keeps the session up
	before := uptime(st, "10.99.0.3")
	cfg.Routing.BGP.Neighbors[0].Export = []config.PrefixRule{{Action: "permit", Prefix: netip.MustParsePrefix("192.168.50.0/24")}}
	files, _ = renderfrr.Renderer{}.Render(cfg)
	put("gg-frr-gate", renderfrr.ConfPath, files[renderfrr.ConfPath])
	gate("/usr/lib/frr/frr-reload.py", "--reload", renderfrr.ConfPath)
	time.Sleep(3 * time.Second)
	st, err = frr.Read(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	if after := uptime(st, "10.99.0.3"); after < before {
		t.Errorf("the reload reset the session: uptime %d → %d", before, after)
	}
	if !strings.Contains(gate("vtysh", "-c", "show running-config"), "gg-n1-out seq 5 permit 192.168.50.0/24") {
		t.Error("the reload did not apply the export filter")
	}
}

func uptime(st frr.Status, addr string) int64 {
	for _, n := range st.BGP {
		if n.Address == addr {
			return n.UptimeSeconds
		}
	}
	return -1
}
