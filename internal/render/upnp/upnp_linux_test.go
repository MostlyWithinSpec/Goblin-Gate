//go:build linux

package upnp_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/nft"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/upnp"
	lease "github.com/networkgoblinlabs/goblin-gate/internal/upnp"
)

// TestUPnPE2E (task 059) runs the real miniupnpd (Debian's
// miniupnpd-nftables) with the rendered config and ruleset in a network
// namespace "gw" (eth0 = WAN, eth1 = LAN) and a client namespace: the client
// opens a port with upnpc, the DNAT and forward rules appear in the Goblin
// chains, a ruleset reload plus miniupnpd restart (what a commit does) keeps
// the mapping, and disabling UPnP removes it. Needs root, nft, miniupnpd and
// upnpc: scripts/linux-test.ps1 runs it with GOBLIN_UPNP_E2E=1.
func TestUPnPE2E(t *testing.T) {
	if os.Getenv("GOBLIN_UPNP_E2E") != "1" {
		t.Skip("set GOBLIN_UPNP_E2E=1 (root, nft, miniupnpd, upnpc)")
	}
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	gw := func(args ...string) string { return run(append([]string{"ip", "netns", "exec", "gw"}, args...)...) }
	cl := func(args ...string) string { return run(append([]string{"ip", "netns", "exec", "cl"}, args...)...) }
	t.Cleanup(func() {
		exec.Command("ip", "netns", "del", "gw").Run()
		exec.Command("ip", "netns", "del", "cl").Run()
	})
	run("ip", "netns", "add", "gw")
	run("ip", "netns", "add", "cl")
	gw("ip", "link", "set", "lo", "up")
	gw("ip", "link", "add", "eth0", "type", "dummy")
	// a public-looking WAN address: miniupnpd maps nothing on private or
	// reserved ones (documentation ranges included); the namespace is isolated
	gw("ip", "addr", "add", "5.5.5.10/24", "dev", "eth0")
	gw("ip", "link", "set", "eth0", "up")
	gw("ip", "route", "add", "default", "via", "5.5.5.1")
	gw("ip", "link", "add", "eth1", "type", "veth", "peer", "name", "c0", "netns", "cl")
	gw("ip", "addr", "add", "192.168.1.1/24", "dev", "eth1")
	gw("ip", "link", "set", "eth1", "up")
	cl("ip", "link", "set", "lo", "up")
	cl("ip", "addr", "add", "192.168.1.50/24", "dev", "c0")
	cl("ip", "link", "set", "c0", "up")
	cl("ip", "route", "add", "default", "via", "192.168.1.1")

	dir := t.TempDir()
	cfg := config.Default()
	cfg.UPnP = config.UPnP{Enabled: true, Interfaces: []string{"lan"}}
	if errs := config.Validate(cfg); len(errs) > 0 {
		t.Fatalf("fixture invalid: %v", errs)
	}
	loadRuleset := func(c *config.Config) {
		t.Helper()
		files, err := nft.Renderer{}.Render(c)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "goblin.nft")
		os.WriteFile(p, files[nft.Path], 0o644)
		gw("nft", "-f", p)
	}
	leases := filepath.Join(dir, "leases")
	pid := filepath.Join(dir, "miniupnpd.pid")
	conf := filepath.Join(dir, "miniupnpd.conf")
	files, err := upnp.Renderer{}.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(conf, []byte(strings.Replace(string(files[upnp.ConfPath]), upnp.LeaseFile, leases, 1)), 0o644)
	// miniupnpd in the foreground (-d) with its log in a file: shown on failure
	logPath := filepath.Join(dir, "miniupnpd.log")
	var proc *exec.Cmd
	start := func() {
		t.Helper()
		logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		proc = exec.Command("ip", "netns", "exec", "gw", "miniupnpd", "-d", "-f", conf, "-P", pid)
		proc.Stdout, proc.Stderr = logf, logf
		if err := proc.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
	}
	stop := func() {
		if proc != nil && proc.Process != nil {
			proc.Process.Signal(os.Interrupt)
			proc.Wait()
			proc = nil
		}
	}
	t.Cleanup(stop)
	t.Cleanup(func() {
		if t.Failed() {
			b, _ := os.ReadFile(logPath)
			t.Logf("miniupnpd log:\n%s", b)
		}
	})

	loadRuleset(cfg)
	start()
	desc := "http://192.168.1.1:" + strconv.Itoa(upnp.HTTPPort) + "/rootDesc.xml"
	t.Log(cl("upnpc", "-u", desc, "-a", "192.168.1.50", "8080", "8080", "TCP"))

	hasMapping := func() bool {
		dnat := gw("nft", "list", "chain", "inet", upnp.NATTable, upnp.DNATChain)
		fwd := gw("nft", "list", "chain", "inet", upnp.FilterTable, upnp.ForwardChain)
		// in an inet table nft prints the forward rule's address as a raw
		// payload match: 0xc0a80132 = 192.168.1.50
		return strings.Contains(dnat, "192.168.1.50:8080") && strings.Contains(fwd, "dport 8080") && strings.Contains(fwd, "0xc0a80132")
	}
	if !hasMapping() {
		t.Fatalf("no mapping in the Goblin chains:\n%s", gw("nft", "list", "ruleset"))
	}
	stop() // miniupnpd writes the lease file on exit at the latest
	ms, err := lease.Read(leases)
	if err != nil || len(ms) != 1 || ms[0].ExternalPort != 8080 || ms[0].InternalIP != "192.168.1.50" {
		b, _ := os.ReadFile(leases)
		t.Fatalf("lease file: %v %+v\n%s", err, ms, b)
	}

	// a commit: the ruleset reload empties the chains, the restart refills them
	loadRuleset(cfg)
	if hasMapping() {
		t.Fatal("the reload should have emptied the chains")
	}
	start()
	if !hasMapping() {
		t.Fatalf("mapping not restored from the lease file:\n%s", gw("nft", "list", "ruleset"))
	}

	// disabling UPnP removes the NAT table (and with it every mapping)
	stop()
	off := config.Default()
	loadRuleset(off)
	if out, _ := exec.Command("ip", "netns", "exec", "gw", "nft", "list", "table", "inet", upnp.NATTable).CombinedOutput(); !strings.Contains(string(out), "No such file") {
		t.Fatalf("table %s still there after disabling UPnP:\n%s", upnp.NATTable, out)
	}
}
