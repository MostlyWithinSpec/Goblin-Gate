//go:build linux

package nft

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// TestScheduleLoad (task 057) loads schedule matches into the kernel the way
// the box does (rendered in UTC from system.timezone, nft run with TZ=UTC)
// and checks that a window around "now" matches and one three hours later
// does not. Needs root and nft in a throwaway network namespace:
// scripts/linux-test.ps1 runs it with GOBLIN_NFT_LOAD=1.
func TestScheduleLoad(t *testing.T) {
	if os.Getenv("GOBLIN_NFT_LOAD") != "1" {
		t.Skip("set GOBLIN_NFT_LOAD=1 (root, throwaway netns)")
	}
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	local := time.Now().In(loc)
	days := []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
	window := func(from, to time.Time, all bool) config.ScheduleRange {
		r := config.ScheduleRange{Days: []string{days[from.Weekday()]}, Start: from.Format("15:04"), End: to.Format("15:04")}
		if all {
			r.Days = days
		}
		return r
	}
	cfg := config.Default()
	cfg.System.Timezone = "America/New_York"
	cfg.Schedules = []config.Schedule{
		{Name: "now", Ranges: []config.ScheduleRange{window(local.Add(-time.Hour), local.Add(time.Hour), false)}},
		// every day, but three to four hours from now: also loads a day set
		{Name: "later", Ranges: []config.ScheduleRange{window(local.Add(3*time.Hour), local.Add(4*time.Hour), true)}},
	}
	if errs := config.Validate(cfg); len(errs) > 0 {
		t.Fatalf("fixture invalid: %v", errs)
	}
	var b strings.Builder
	b.WriteString("table inet gg_sched\ndelete table inet gg_sched\ntable inet gg_sched {\n\tcounter now {}\n\tcounter later {}\n")
	b.WriteString("\tchain o {\n\t\ttype filter hook output priority filter; policy accept;\n")
	for _, name := range []string{"now", "later"} {
		ms, err := scheduleMatches(cfg, name)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range ms {
			fmt.Fprintf(&b, "\t\toifname \"lo\" udp dport 9 %s counter name %q\n", m, name)
		}
	}
	b.WriteString("\t}\n}\n")
	t.Logf("ruleset:\n%s", b.String())
	f := t.TempDir() + "/sched.nft"
	if err := os.WriteFile(f, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("nft", "-f", f)
	cmd.Env = append(os.Environ(), "TZ=UTC") // as goblind.service and nftables.service run it
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("nft -f: %v\n%s", err, out)
	}
	defer exec.Command("nft", "delete", "table", "inet", "gg_sched").Run()

	exec.Command("ip", "link", "set", "lo", "up").Run()
	c, err := net.Dial("udp", "127.0.0.1:9")
	if err != nil {
		t.Fatal(err)
	}
	c.Write([]byte("x"))
	c.Close()

	packets := func(name string) string {
		out, _ := exec.Command("nft", "list", "counter", "inet", "gg_sched", name).CombinedOutput()
		m := regexp.MustCompile(`packets (\d+)`).FindStringSubmatch(string(out))
		if m == nil {
			t.Fatalf("counter %s: %s", name, out)
		}
		return m[1]
	}
	if got := packets("now"); got != "1" {
		t.Errorf("window around now matched %s packets, want 1", got)
	}
	if got := packets("later"); got != "0" {
		t.Errorf("window three hours from now matched %s packets, want 0", got)
	}
}
