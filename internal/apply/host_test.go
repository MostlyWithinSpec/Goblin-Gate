package apply

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// fakeRunner records commands; commands whose joined text starts with a
// key in fail return an error with the value as output.
type fakeRunner struct {
	cmds [][]string
	fail map[string]string
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	cmd := append([]string{name}, args...)
	r.cmds = append(r.cmds, cmd)
	line := strings.Join(cmd, " ")
	for prefix, out := range r.fail {
		if strings.HasPrefix(line, prefix) {
			return []byte(out), errors.New("exit status 1")
		}
	}
	return []byte("ok"), nil
}

// lines returns the recorded commands with temp check paths normalised.
func (r *fakeRunner) lines(root string) []string {
	var out []string
	for _, c := range r.cmds {
		line := strings.Join(c, " ")
		line = strings.ReplaceAll(line, filepath.Join(root, "var", "lib", "goblingate", "apply", "check")+string(filepath.Separator), "CHECK/")
		out = append(out, line)
	}
	r.cmds = nil
	return out
}

const (
	nftPath     = "/etc/nftables.d/goblin.nft"
	keaPath     = "/etc/kea/kea-dhcp4.conf"
	unboundPath = "/etc/unbound/unbound.conf.d/goblin.conf"
	lanPath     = "/etc/systemd/network/10-goblin-lan.network"
	wanPath     = "/etc/systemd/network/10-goblin-wan1.network"
	sysctlPath  = "/etc/sysctl.d/90-goblin.conf"
)

func baseFiles() map[string][]byte {
	return map[string][]byte{
		nftPath:     []byte("nft v1\n"),
		keaPath:     []byte("kea v1\n"),
		unboundPath: []byte("unbound v1\n"),
		lanPath:     []byte("lan v1\n"),
		wanPath:     []byte("wan v1\n"),
		sysctlPath:  []byte("net.ipv4.ip_forward=1\n"),
	}
}

func newHost(t *testing.T) (*Host, *fakeRunner) {
	t.Helper()
	r := &fakeRunner{fail: map[string]string{}}
	return &Host{Root: t.TempDir(), Runner: r}, r
}

func readFile(t *testing.T, h *Host, p string) string {
	t.Helper()
	b, ok, err := h.read(p)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		return "<missing>"
	}
	return string(b)
}

func TestApplyFirstRun(t *testing.T) {
	h, r := newHost(t)
	if err := h.Apply(context.Background(), baseFiles()); err != nil {
		t.Fatal(err)
	}
	for p, want := range baseFiles() {
		if got := readFile(t, h, p); got != string(want) {
			t.Errorf("%s = %q, want %q", p, got, want)
		}
	}
	want := []string{
		"nft -c -f CHECK/nft-goblin.nft",
		"kea-dhcp4 -t CHECK/kea-kea-dhcp4.conf",
		"unbound-checkconf CHECK/unbound-goblin.conf",
		"/usr/lib/systemd/systemd-sysctl /etc/sysctl.d/90-goblin.conf",
		"networkctl reload",
		"nft -f /etc/nftables.d/goblin.nft", upnpRestart,
		"systemctl reload-or-restart kea-dhcp4-server.service",
		"systemctl restart unbound.service",
	}
	if diff := cmp.Diff(want, r.lines(h.Root)); diff != "" {
		t.Errorf("commands (-want +got):\n%s", diff)
	}
	if got := readFile(t, h, "/var/lib/goblingate/apply/manifest.json"); !strings.Contains(got, nftPath) {
		t.Errorf("manifest missing %s: %s", nftPath, got)
	}
	// Check files are cleaned up.
	entries, _ := os.ReadDir(filepath.Join(h.Root, "var", "lib", "goblingate", "apply", "check"))
	if len(entries) != 0 {
		t.Errorf("check dir not empty: %v", entries)
	}
}

func TestApplyOnlyChangedGroups(t *testing.T) {
	h, r := newHost(t)
	ctx := context.Background()
	if err := h.Apply(ctx, baseFiles()); err != nil {
		t.Fatal(err)
	}
	r.lines(h.Root)

	if err := h.Apply(ctx, baseFiles()); err != nil {
		t.Fatal(err)
	}
	if got := r.lines(h.Root); len(got) != 0 {
		t.Errorf("unchanged apply ran commands: %v", got)
	}

	files := baseFiles()
	files[nftPath] = []byte("nft v2\n")
	if err := h.Apply(ctx, files); err != nil {
		t.Fatal(err)
	}
	want := []string{"nft -c -f CHECK/nft-goblin.nft", "nft -f /etc/nftables.d/goblin.nft", upnpRestart}
	if diff := cmp.Diff(want, r.lines(h.Root)); diff != "" {
		t.Errorf("commands (-want +got):\n%s", diff)
	}
	if got := readFile(t, h, "/var/lib/goblingate/apply/prev"+nftPath); got != "nft v1\n" {
		t.Errorf("backup = %q, want previous content", got)
	}
}

func TestApplyRemovesStaleFiles(t *testing.T) {
	h, r := newHost(t)
	ctx := context.Background()
	// A file the applier never wrote must survive.
	foreign := "/etc/systemd/network/99-user.network"
	if err := h.writeAtomic(foreign, []byte("mine\n")); err != nil {
		t.Fatal(err)
	}
	if err := h.Apply(ctx, baseFiles()); err != nil {
		t.Fatal(err)
	}
	r.lines(h.Root)

	files := baseFiles()
	delete(files, wanPath)
	if err := h.Apply(ctx, files); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, h, wanPath); got != "<missing>" {
		t.Errorf("stale file still present: %q", got)
	}
	if got := readFile(t, h, foreign); got != "mine\n" {
		t.Errorf("foreign file touched: %q", got)
	}
	if diff := cmp.Diff([]string{"networkctl reload"}, r.lines(h.Root)); diff != "" {
		t.Errorf("commands (-want +got):\n%s", diff)
	}
}

func TestApplyCheckFailureWritesNothing(t *testing.T) {
	h, r := newHost(t)
	ctx := context.Background()
	if err := h.Apply(ctx, baseFiles()); err != nil {
		t.Fatal(err)
	}
	r.lines(h.Root)

	files := baseFiles()
	files[nftPath] = []byte("broken\n")
	files[lanPath] = []byte("lan v2\n")
	r.fail["nft -c"] = "Error: syntax error, unexpected junk"
	err := h.Apply(ctx, files)
	if err == nil || !strings.Contains(err.Error(), "unexpected junk") {
		t.Fatalf("want check error with nft output, got %v", err)
	}
	if got := readFile(t, h, nftPath); got != "nft v1\n" {
		t.Errorf("nft file changed after failed check: %q", got)
	}
	if got := readFile(t, h, lanPath); got != "lan v1\n" {
		t.Errorf("lan file changed after failed check: %q", got)
	}
	for _, l := range r.lines(h.Root) {
		if !strings.Contains(l, "-c") {
			t.Errorf("unexpected command after failed check: %s", l)
		}
	}
}

func TestApplyActivationFailureRollsBack(t *testing.T) {
	h, r := newHost(t)
	ctx := context.Background()
	if err := h.Apply(ctx, baseFiles()); err != nil {
		t.Fatal(err)
	}
	r.lines(h.Root)

	files := baseFiles()
	files[nftPath] = []byte("nft v2\n")
	files[keaPath] = []byte("kea v2\n")
	newPath := "/etc/systemd/network/20-goblin-guest.network"
	files[newPath] = []byte("guest\n")
	r.fail["systemctl reload-or-restart kea"] = "Job for kea-dhcp4-server.service failed"
	err := h.Apply(ctx, files)
	if err == nil || !strings.Contains(err.Error(), "kea-dhcp4-server.service failed") {
		t.Fatalf("want error with kea output, got %v", err)
	}
	if got := readFile(t, h, nftPath); got != "nft v1\n" {
		t.Errorf("nft not restored: %q", got)
	}
	if got := readFile(t, h, keaPath); got != "kea v1\n" {
		t.Errorf("kea not restored: %q", got)
	}
	if got := readFile(t, h, newPath); got != "<missing>" {
		t.Errorf("new file not removed on rollback: %q", got)
	}
	got := r.lines(h.Root)
	// Forward: checks, networkd, nft, kea (fails). Rollback: networkd, nft, kea again
	// (the rollback kea restart fails too in this fake, which is reported).
	wantTail := []string{"networkctl reload", "nft -f /etc/nftables.d/goblin.nft", upnpRestart, "systemctl reload-or-restart kea-dhcp4-server.service"}
	if len(got) < 6 {
		t.Fatalf("too few commands: %v", got)
	}
	if diff := cmp.Diff(wantTail, got[len(got)-len(wantTail):]); diff != "" {
		t.Errorf("rollback commands (-want +got):\n%s", diff)
	}
	if !strings.Contains(err.Error(), "rollback also failed") {
		t.Errorf("rollback failure not reported: %v", err)
	}

	// The manifest still describes the old generation: a clean retry works.
	delete(r.fail, "systemctl reload-or-restart kea")
	if err := h.Apply(ctx, files); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got := readFile(t, h, newPath); got != "guest\n" {
		t.Errorf("retry did not write new file: %q", got)
	}
}

func TestApplyRollbackSucceeds(t *testing.T) {
	h, r := newHost(t)
	ctx := context.Background()
	if err := h.Apply(ctx, baseFiles()); err != nil {
		t.Fatal(err)
	}
	files := baseFiles()
	files[unboundPath] = []byte("unbound v2\n")
	// Fails once (forward), then succeeds (rollback).
	fails := 0
	r2 := &onceRunner{fakeRunner: r, prefix: "systemctl restart unbound", fails: &fails}
	h.Runner = r2
	err := h.Apply(ctx, files)
	if err == nil || !strings.Contains(err.Error(), "previous files restored") || strings.Contains(err.Error(), "rollback also failed") {
		t.Fatalf("got %v", err)
	}
	if got := readFile(t, h, unboundPath); got != "unbound v1\n" {
		t.Errorf("unbound not restored: %q", got)
	}
}

type onceRunner struct {
	*fakeRunner
	prefix string
	fails  *int
}

func (o *onceRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if strings.HasPrefix(strings.Join(append([]string{name}, args...), " "), o.prefix) && *o.fails == 0 {
		*o.fails++
		return []byte("boom"), errors.New("exit status 1")
	}
	return o.fakeRunner.Run(ctx, name, args...)
}

func TestApplyAfterRestartActivatesAll(t *testing.T) {
	h, r := newHost(t)
	ctx := context.Background()
	if err := h.Apply(ctx, baseFiles()); err != nil {
		t.Fatal(err)
	}
	r.lines(h.Root)
	files := baseFiles()
	files[nftPath] = []byte("nft v2\n")
	if err := h.Apply(ctx, files); err != nil {
		t.Fatal(err)
	}
	r.lines(h.Root)

	// New process, same disk: files unchanged, kernel state gone.
	h2 := &Host{Root: h.Root, Runner: r}
	if err := h2.Apply(ctx, files); err != nil {
		t.Fatal(err)
	}
	got := r.lines(h.Root)
	want := []string{
		"/usr/lib/systemd/systemd-sysctl /etc/sysctl.d/90-goblin.conf",
		"networkctl reload",
		"nft -f /etc/nftables.d/goblin.nft", upnpRestart,
		"systemctl reload-or-restart kea-dhcp4-server.service",
		"systemctl restart unbound.service",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("commands (-want +got):\n%s", diff)
	}
	if b := readFile(t, h, "/var/lib/goblingate/apply/prev"+nftPath); b != "nft v1\n" {
		t.Errorf("no-change activation wiped the backup: %q", b)
	}
}

func TestApplyRejectsUnmanagedPaths(t *testing.T) {
	for _, p := range []string{
		"/etc/passwd",
		"/etc/nftables.d/../passwd",
		"etc/nftables.d/x.nft",
		"/etc/nftables.d/",
		"/etc/nftables.d",
		"/etc/kea//kea.conf",
	} {
		h, r := newHost(t)
		err := h.Apply(context.Background(), map[string][]byte{p: []byte("x")})
		if err == nil {
			t.Errorf("%q: want error", p)
		}
		if len(r.cmds) != 0 {
			t.Errorf("%q: commands ran: %v", p, r.cmds)
		}
	}
}

func TestApplyContextCancelledIsReported(t *testing.T) {
	h, r := newHost(t)
	r.fail["networkctl"] = "context canceled"
	err := h.Apply(context.Background(), baseFiles())
	if err == nil || !strings.HasPrefix(err.Error(), "apply: ") {
		t.Fatalf("want apply error, got %v", err)
	}
}

func netdev(name, id string) []byte {
	return []byte("# Generated by Goblin Gate — do not edit\n[NetDev]\nName=" + name + "\nKind=vlan\n\n[VLAN]\nId=" + id + "\n")
}

func TestApplyRecreatesChangedNetdev(t *testing.T) {
	h, r := newHost(t)
	ctx := context.Background()
	const guest = "/etc/systemd/network/20-goblin-guest.netdev"
	const iot = "/etc/systemd/network/20-goblin-iot.netdev"
	files := baseFiles()
	files[guest] = netdev("eth1.20", "20")
	files[iot] = netdev("eth1.30", "30")
	if err := h.Apply(ctx, files); err != nil {
		t.Fatal(err)
	}
	r.lines(h.Root)

	// guest: id changes -> delete the old link; iot: removed -> delete it.
	files[guest] = netdev("eth1.21", "21")
	delete(files, iot)
	if err := h.Apply(ctx, files); err != nil {
		t.Fatal(err)
	}
	want := []string{"networkctl delete eth1.20", "networkctl delete eth1.30", "networkctl reload"}
	if diff := cmp.Diff(want, r.lines(h.Root)); diff != "" {
		t.Errorf("commands (-want +got):\n%s", diff)
	}

	// A comment-only change keeps the link.
	files[guest] = append([]byte("# x\n"), netdev("eth1.21", "21")...)
	if err := h.Apply(ctx, files); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"networkctl reload"}, r.lines(h.Root)); diff != "" {
		t.Errorf("commands (-want +got):\n%s", diff)
	}
}

func TestApplyNetdevRollbackDeletesNewLink(t *testing.T) {
	h, r := newHost(t)
	ctx := context.Background()
	const guest = "/etc/systemd/network/20-goblin-guest.netdev"
	files := baseFiles()
	files[guest] = netdev("eth1.20", "20")
	if err := h.Apply(ctx, files); err != nil {
		t.Fatal(err)
	}
	r.lines(h.Root)

	files[guest] = netdev("eth1.21", "21")
	files[nftPath] = []byte("nft v2\n")
	r.fail["nft -f"] = "boom"
	if err := h.Apply(ctx, files); err == nil {
		t.Fatal("expected error")
	}
	got := r.lines(h.Root)
	want := []string{
		"nft -c -f CHECK/nft-goblin.nft",
		"networkctl delete eth1.20", "networkctl reload", "nft -f /etc/nftables.d/goblin.nft",
		// rollback
		"networkctl delete eth1.21", "networkctl reload", "nft -f /etc/nftables.d/goblin.nft",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("commands (-want +got):\n%s", diff)
	}
	if got := readFile(t, h, guest); got != string(netdev("eth1.20", "20")) {
		t.Errorf("netdev not restored: %q", got)
	}
}

const keaJSON = `{"Dhcp4":{"interfaces-config":{"interfaces":["eth1","eth1.20"]},"subnet4":[{"id":1,"subnet":"192.168.20.0/24","interface":"eth1.20"}]}}`

func TestKeaCheckCopyDropsInterfaces(t *testing.T) {
	b, err := keaWithoutInterfaces([]byte(keaJSON))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "eth1") || !strings.Contains(string(b), `"interfaces":[]`) {
		t.Errorf("check copy still names interfaces: %s", b)
	}
	if got := keaInterfaces([]byte(keaJSON)); strings.Join(got, " ") != "eth1 eth1.20" {
		t.Errorf("keaInterfaces = %v", got)
	}
}

func TestApplyWaitsForKeaLinks(t *testing.T) {
	h, _ := newHost(t)
	h.LinkWait = 2 * time.Second
	files := baseFiles()
	files[keaPath] = []byte(keaJSON)
	mkLink := func(dev string) {
		if err := os.MkdirAll(filepath.Join(h.Root, "sys", "class", "net", dev), 0o755); err != nil {
			t.Error(err)
		}
	}
	mkLink("eth1")
	// eth1.20 appears while Apply is waiting, as networkd would create it.
	go func() { time.Sleep(300 * time.Millisecond); mkLink("eth1.20") }()
	if err := h.Apply(context.Background(), files); err != nil {
		t.Fatal(err)
	}
}

func TestApplyMissingKeaLinkRollsBack(t *testing.T) {
	h, r := newHost(t)
	h.LinkWait = 200 * time.Millisecond
	ctx := context.Background()
	if err := h.Apply(ctx, baseFiles()); err != nil {
		t.Fatal(err)
	}
	r.lines(h.Root)
	files := baseFiles()
	files[keaPath] = []byte(keaJSON)
	err := h.Apply(ctx, files)
	if err == nil || !strings.Contains(err.Error(), "eth1, eth1.20 did not appear") {
		t.Fatalf("err = %v, want missing devices", err)
	}
	if got := readFile(t, h, keaPath); got != "kea v1\n" {
		t.Errorf("kea config not restored: %q", got)
	}
	// Only the rollback (back to the old config) may reload kea.
	n := 0
	for _, l := range r.lines(h.Root) {
		if strings.HasPrefix(l, "systemctl reload-or-restart kea") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("kea reloaded %d times, want once (rollback only)", n)
	}
}

func TestApplyRPZZonesBelongToUnbound(t *testing.T) {
	h, r := newHost(t)
	if err := h.Apply(context.Background(), baseFiles()); err != nil {
		t.Fatal(err)
	}
	r.lines(h.Root) // drop the first run's commands
	files := baseFiles()
	files["/var/lib/unbound/rpz/kids.zone"] = []byte("$ORIGIN kids.rpz.goblin.\n")
	if err := h.Apply(context.Background(), files); err != nil {
		t.Fatal(err)
	}
	// a zone file is not run through unbound-checkconf, and restarts unbound
	if diff := cmp.Diff([]string{"systemctl restart unbound.service"}, r.lines(h.Root)); diff != "" {
		t.Errorf("commands (-want +got):\n%s", diff)
	}
	if got := readFile(t, h, "/var/lib/unbound/rpz/kids.zone"); !strings.Contains(got, "kids.rpz") {
		t.Errorf("zone file not written: %q", got)
	}
}

func TestApplySuricataInstances(t *testing.T) {
	h, r := newHost(t)
	files := baseFiles()
	files["/etc/suricata/goblin/home.yaml"] = []byte("%YAML 1.1\n")
	files["/etc/suricata/goblin/home.env"] = []byte("QUEUES=\"-q 16\"\n")
	if err := h.Apply(context.Background(), files); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(r.lines(h.Root), "\n")
	for _, want := range []string{"systemctl enable goblin-suricata@home.service", "systemctl restart goblin-suricata@home.service"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// the profile goes away: its instance is stopped and disabled
	delete(files, "/etc/suricata/goblin/home.yaml")
	delete(files, "/etc/suricata/goblin/home.env")
	if err := h.Apply(context.Background(), files); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"systemctl disable --now goblin-suricata@home.service"}, r.lines(h.Root)); diff != "" {
		t.Errorf("removal (-want +got):\n%s", diff)
	}
}

func TestApplyTailscale(t *testing.T) {
	h, r := newHost(t)
	files := baseFiles()
	files["/etc/goblingate/tailscale/prefs.json"] = []byte(`{"enabled":false}`)
	if err := h.Apply(context.Background(), files); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.lines(h.Root), "\n"); !strings.Contains(got, "/usr/sbin/goblind tailscale-apply") {
		t.Fatalf("commands:\n%s", got)
	}
}

// zoneCheckRunner is a fakeRunner whose unbound-checkconf, like the real
// one, opens the zone files the checked config names.
type zoneCheckRunner struct {
	*fakeRunner
	t      *testing.T
	opened []string
}

func (r *zoneCheckRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name == "unbound-checkconf" {
		conf, err := os.ReadFile(args[0])
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(conf), "\n") {
			if f, ok := strings.CutPrefix(strings.TrimSpace(line), "zonefile: "); ok {
				f = strings.Trim(f, `"`)
				if _, err := os.ReadFile(f); err != nil {
					return []byte("cannot open zonefile " + f), errors.New("exit status 1")
				}
				r.opened = append(r.opened, f)
			}
		}
	}
	return r.fakeRunner.Run(ctx, name, args...)
}

func TestApplyNewRPZZoneIsStagedForTheCheck(t *testing.T) {
	h, fr := newHost(t)
	r := &zoneCheckRunner{fakeRunner: fr, t: t}
	h.Runner = r
	if err := h.Apply(context.Background(), baseFiles()); err != nil {
		t.Fatal(err)
	}
	// a new DNS filter profile: the config names a zone file that does not
	// exist yet at its real path
	files := baseFiles()
	files[unboundPath] = []byte("rpz:\n  name: kids.rpz.goblin.\n  zonefile: \"/var/lib/unbound/rpz/kids.zone\"\n")
	files["/var/lib/unbound/rpz/kids.zone"] = []byte("$ORIGIN kids.rpz.goblin.\n")
	if err := h.Apply(context.Background(), files); err != nil {
		t.Fatal(err)
	}
	if len(r.opened) != 1 || !strings.Contains(r.opened[0], "staged-unbound-kids.zone") {
		t.Fatalf("checkconf opened %v, want the staged zone", r.opened)
	}
	if _, err := os.Stat(r.opened[0]); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("staged zone left behind: %v", err)
	}
	// the real files carry the real path
	if got := readFile(t, h, unboundPath); !strings.Contains(got, `"/var/lib/unbound/rpz/kids.zone"`) {
		t.Errorf("config written with the staged path: %q", got)
	}
}

func TestApplySystemSettings(t *testing.T) {
	h, r := newHost(t)
	files := baseFiles()
	files["/var/lib/goblingate/system/hostname"] = []byte("gate\n")
	files["/var/lib/goblingate/system/timezone"] = []byte("UTC\n")
	files["/etc/systemd/timesyncd.conf.d/goblin.conf"] = []byte("[Time]\nNTP=pool.ntp.org\n")
	files["/etc/ssh/sshd_config.d/goblin.conf"] = []byte("PasswordAuthentication no\n")
	files["/etc/ssh/goblin_authorized_keys/root"] = []byte("# none\n")
	if err := h.Apply(context.Background(), files); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(r.lines(h.Root), "\n")
	for _, want := range []string{"hostnamectl set-hostname gate", "timedatectl set-timezone UTC",
		"systemctl try-restart systemd-timesyncd.service", "systemctl try-reload-or-restart ssh.service"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// only the time zone changes: only timedatectl runs
	files["/var/lib/goblingate/system/timezone"] = []byte("America/Chicago\n")
	if err := h.Apply(context.Background(), files); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"timedatectl set-timezone America/Chicago"}, r.lines(h.Root)); diff != "" {
		t.Errorf("time zone change (-want +got):\n%s", diff)
	}
}

// TestAptGroupDisablesCDROM: the installer's cdrom: source is commented out
// by goblind when it writes the APT sources, not by the package's postinst
// (which would cut off the CD while the installer still installs from it).
func TestAptGroupDisablesCDROM(t *testing.T) {
	for _, g := range groups {
		if g.name != "apt" {
			continue
		}
		cmds := g.activate(nil)
		if len(cmds) != 1 || !strings.Contains(strings.Join(cmds[0], " "), `s|^deb cdrom:|# deb cdrom:|`) {
			t.Fatalf("apt activate = %v", cmds)
		}
		return
	}
	t.Fatal("no apt group")
}

// upnpRestart follows every ruleset load: miniupnpd re-adds its mappings.
const upnpRestart = "sh -c ! systemctl cat goblin-miniupnpd.service >/dev/null 2>&1 || systemctl try-restart goblin-miniupnpd.service"
