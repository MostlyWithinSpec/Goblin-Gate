package suricata

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/")

// twoProfiles: "home" (prevent, suppressions) on the lan policy and
// "Watch.Only" (detect) on a second policy; "unused" is on no policy and a
// deny policy's profile does not count.
func twoProfiles() *config.Config {
	cfg := config.Default()
	cfg.Profiles.IPS = []config.IPSProfile{
		{Name: "home", Suppress: []config.IPSSuppress{{SID: 2013028}, {SID: 2001219, Track: "by_src", IP: "192.168.1.50"}}},
		{Name: "Watch.Only", Mode: "detect"},
		{Name: "unused"},
	}
	cfg.Policies[0].IPSProfile = "home"
	p2 := cfg.Policies[0]
	p2.ID, p2.Name, p2.IPSProfile, p2.DstAddr = 2, "lan-to-dns", "Watch.Only", []string{"all"}
	p3 := p2
	p3.ID, p3.Name, p3.Action, p3.IPSProfile, p3.NAT = 3, "deny-x", "deny", "unused", false
	cfg.Policies = append(cfg.Policies, p2, p3)
	return cfg
}

func TestGolden(t *testing.T) {
	cfg := twoProfiles()
	if errs := config.Validate(cfg); len(errs) > 0 {
		t.Fatal(errs)
	}
	files, err := Renderer{}.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 8 {
		t.Fatalf("files: %v", keys(files))
	}
	for p, got := range files {
		golden := filepath.Join("testdata", filepath.Base(p))
		if *update {
			os.MkdirAll("testdata", 0o755)
			if err := os.WriteFile(golden, got, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != strings.ReplaceAll(string(want), "\r\n", "\n") {
			t.Errorf("%s differs from %s:\n%s", p, golden, got)
		}
	}
	if got := string(files[EnvPath("home")]); !strings.Contains(got, `QUEUES="-q 24 -q 25 -q 26 -q 27"`) {
		t.Errorf("home queues: %s", got)
	}
	if got := string(files[EnvPath("Watch.Only")]); !strings.Contains(got, `-q 16 `) || !strings.HasPrefix(filepath.Base(EnvPath("Watch.Only")), "watch_only") {
		t.Errorf("second profile queues/name: %s", got)
	}
}

func TestNoIPS(t *testing.T) {
	files, err := Renderer{}.Render(config.Default())
	if err != nil || len(files) != 0 {
		t.Fatalf("%v %v", files, err)
	}
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
