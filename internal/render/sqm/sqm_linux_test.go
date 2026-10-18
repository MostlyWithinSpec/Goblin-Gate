package sqm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// TestScriptLoads runs the generated script against a dummy WAN device
// (GOBLIN_SQM_LOAD=1, root, a kernel with sch_cake and ifb): CAKE must
// appear on both directions, and the "off" script must remove all of it.
func TestScriptLoads(t *testing.T) {
	if os.Getenv("GOBLIN_SQM_LOAD") == "" {
		t.Skip("set GOBLIN_SQM_LOAD=1 (needs root and the sch_cake/ifb modules)")
	}
	run := func(args ...string) string {
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	exec.Command("ip", "link", "del", "gqwan0").Run()
	run("ip", "link", "add", "gqwan0", "type", "dummy")
	run("ip", "link", "set", "gqwan0", "up")
	t.Cleanup(func() {
		exec.Command("ip", "link", "del", "gqwan0").Run()
		exec.Command("ip", "link", "del", "ifb4gqwan0").Run()
	})
	// Debian's kernel has sch_cake; some (WSL2 under Docker Desktop) do not
	if out, err := exec.Command("tc", "qdisc", "replace", "dev", "gqwan0", "root", "cake").CombinedOutput(); err != nil {
		t.Skipf("this kernel has no CAKE: %s", strings.TrimSpace(string(out)))
	}
	run("tc", "qdisc", "del", "dev", "gqwan0", "root")

	cfg := config.Default()
	cfg.Interfaces[0].Device = "gqwan0"
	cfg.Interfaces[1].Device = "gqlan0"
	script := func() string {
		files, _ := Renderer{}.Render(cfg)
		p := filepath.Join(t.TempDir(), "sqm.sh")
		os.WriteFile(p, files[ScriptPath], 0o644)
		return p
	}
	cfg.Interfaces[0].SQM = &config.SQM{Enabled: true, DownloadMbit: 100, UploadMbit: 20}
	p := script()
	run("sh", p)
	run("sh", p) // twice: the script is idempotent
	if out := run("tc", "qdisc", "show", "dev", "gqwan0"); !strings.Contains(out, "qdisc cake") || !strings.Contains(out, "20Mbit") || !strings.Contains(out, "ingress") {
		t.Fatalf("wan qdiscs:\n%s", out)
	}
	if out := run("tc", "qdisc", "show", "dev", "ifb4gqwan0"); !strings.Contains(out, "qdisc cake") || !strings.Contains(out, "100Mbit") {
		t.Fatalf("ifb qdiscs:\n%s", out)
	}

	cfg.Interfaces[0].SQM.Enabled = false
	run("sh", script())
	if out := run("tc", "qdisc", "show", "dev", "gqwan0"); strings.Contains(out, "cake") || strings.Contains(out, "ingress") {
		t.Fatalf("off left shaping:\n%s", out)
	}
	if exec.Command("ip", "link", "show", "ifb4gqwan0").Run() == nil {
		t.Fatal("off left the ifb device")
	}
}
