package sqm

import (
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func TestRender(t *testing.T) {
	cfg := config.Default() // wan1 on eth0, lan on eth1
	files, err := Renderer{}.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	off := string(files[ScriptPath])
	for _, want := range []string{"tc qdisc del dev eth0 root", "tc qdisc del dev eth0 ingress", "ip link del ifb4eth0", "exit 0"} {
		if !strings.Contains(off, want) {
			t.Errorf("off: missing %q in\n%s", want, off)
		}
	}
	if strings.Contains(off, "cake") || strings.Contains(off, "eth1") || strings.Contains(off, "set -e") {
		t.Errorf("off: shapes something or touches the LAN:\n%s", off)
	}

	cfg.Interfaces[0].SQM = &config.SQM{Enabled: true, DownloadMbit: 450, UploadMbit: 36}
	files, _ = Renderer{}.Render(cfg)
	on := string(files[ScriptPath])
	want := []string{
		"tc qdisc del dev eth0 root 2>/dev/null",
		"set -e",
		"tc qdisc replace dev eth0 root cake bandwidth 36mbit diffserv4 dual-srchost nat",
		"ip link add ifb4eth0 type ifb",
		"ip link set ifb4eth0 up",
		"tc qdisc add dev eth0 handle ffff: ingress",
		"tc filter add dev eth0 parent ffff: protocol all matchall action mirred egress redirect dev ifb4eth0",
		"tc qdisc replace dev ifb4eth0 root cake bandwidth 450mbit diffserv4 dual-dsthost nat wash ingress",
	}
	last := -1
	for _, w := range want {
		i := strings.Index(on, w)
		if i < 0 || i < last {
			t.Fatalf("on: %q missing or out of order in\n%s", w, on)
		}
		last = i
	}

	// a disabled WAN is not shaped
	cfg.Interfaces[0].Enabled = false
	files, _ = Renderer{}.Render(cfg)
	if strings.Contains(string(files[ScriptPath]), "cake") {
		t.Fatal("disabled WAN shaped")
	}
}

func TestIFBName(t *testing.T) {
	if got := IFBName("ens192"); got != "ifb4ens192" {
		t.Fatal(got)
	}
	long := IFBName("enp0s31f6.1000")
	if len(long) > 15 || !strings.HasPrefix(long, "ifb") || long == IFBName("enp0s31f6.1001") {
		t.Fatal(long)
	}
}
