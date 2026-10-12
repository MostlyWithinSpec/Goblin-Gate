//go:build linux

package suricata_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/ips"
	"github.com/networkgoblinlabs/goblin-gate/internal/render"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/suricata"
)

// TestSuricataE2E runs the rendered configs in Suricata 7 with the real
// ET Open rules: suricata -T accepts both profiles, then HTTP traffic to a
// local server answering "uid=0(root)" (the testmynids.org check; trips ET
// rule 2100498, severity 2) is queued like the nft renderer does, first to
// the detect profile (alert, page loads), then to the prevent profile
// (alert, page blocked). Needs root, suricata, suricata-update's download,
// NFQUEUE and a throwaway container: scripts/linux-ips-test.ps1 runs it.
func TestSuricataE2E(t *testing.T) {
	if os.Getenv("GOBLIN_IPS_E2E") != "1" {
		t.Skip("set GOBLIN_IPS_E2E=1 (root, suricata, throwaway container)")
	}
	cfg := config.Default()
	cfg.Profiles.IPS = []config.IPSProfile{{Name: "prevent"}, {Name: "watch", Mode: "detect"}}
	cfg.Policies[0].IPSProfile = "prevent"
	p2 := cfg.Policies[0]
	p2.ID, p2.Name, p2.IPSProfile = 2, "second", "watch"
	cfg.Policies = append(cfg.Policies, p2)
	if errs := config.Validate(cfg); len(errs) > 0 {
		t.Fatal(errs)
	}
	files, err := suricata.Renderer{}.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for p, b := range files {
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	uses := map[string]render.IPSUse{}
	for _, u := range render.IPSProfilesInUse(cfg) {
		uses[u.Profile.Name] = u
		st, err := ips.BuildRules(ips.DefaultPaths, suricata.Instance(u.Profile.Name))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %d drop, %d alert, %d skipped rules", u.Profile.Name, st.Drop, st.Alert, st.Skipped)
		if st.Drop+st.Alert < 1000 {
			t.Fatalf("%s: too few rules; is the ET Open download there?", u.Profile.Name)
		}
		out, err := exec.Command("suricata", "-T", "-c", suricata.YAMLPath(u.Profile.Name), "-l", "/tmp").CombinedOutput()
		if err != nil {
			t.Fatalf("suricata -T %s: %v\n%s", u.Profile.Name, err, tail(string(out), 20))
		}
	}

	ln, err := net.Listen("tcp", "127.0.0.1:18080")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "uid=0(root) gid=0(root) groups=0(root)\n")
	})}
	go srv.Serve(ln)
	defer srv.Close()
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	// queue both directions of the test traffic like ct-marked IPS flows
	// (on loopback every packet passes the output hook once)
	queueTo := func(u render.IPSUse) {
		exec.Command("nft", "delete", "table", "inet", "ipstest").Run()
		v := fmt.Sprintf("queue flags bypass,fanout to %d-%d", u.QueueFirst, u.QueueLast)
		run("nft", "add", "table", "inet", "ipstest")
		run("nft", "add", "chain", "inet", "ipstest", "out", "{ type filter hook output priority 0; }")
		run("sh", "-c", fmt.Sprintf("nft add rule inet ipstest out tcp dport 18080 %s", v))
		run("sh", "-c", fmt.Sprintf("nft add rule inet ipstest out tcp sport 18080 %s", v))
	}
	start := func(u render.IPSUse) *exec.Cmd {
		t.Helper()
		args := []string{"-c", suricata.YAMLPath(u.Profile.Name)}
		for q := u.QueueFirst; q <= u.QueueLast; q++ {
			args = append(args, "-q", fmt.Sprint(q))
		}
		cmd := exec.Command("suricata", args...)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		logf := "/var/log/suricata/goblin-" + suricata.Instance(u.Profile.Name) + ".log"
		deadline := time.Now().Add(3 * time.Minute)
		for {
			b, _ := os.ReadFile(logf)
			if strings.Contains(string(b), "Engine started") {
				return cmd
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s did not start:\n%s", u.Profile.Name, tail(string(b), 20))
			}
			time.Sleep(time.Second)
		}
	}
	fetch := func() (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "curl", "-s", "-m", "6", "http://127.0.0.1:18080/uid/index.html").Output()
		return string(out), err
	}
	alertsFor := func(sensor string) []map[string]any {
		f, err := os.Open(suricata.EVELog)
		if err != nil {
			return nil
		}
		defer f.Close()
		var out []map[string]any
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			var e map[string]any
			if json.Unmarshal(sc.Bytes(), &e) == nil && e["event_type"] == "alert" && e["host"] == sensor {
				out = append(out, e)
			}
		}
		return out
	}
	alertAction := func(sensor string) string {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			for _, a := range alertsFor(sensor) {
				al := a["alert"].(map[string]any)
				if al["signature_id"] == float64(2100498) {
					return al["action"].(string)
				}
			}
			time.Sleep(500 * time.Millisecond)
		}
		return ""
	}

	// detect: the page loads and an "allowed" alert is logged
	watch := uses["watch"]
	cmd := start(watch)
	queueTo(watch)
	body, err := fetch()
	if err != nil || !strings.Contains(body, "uid=0(root)") {
		t.Errorf("detect profile: page should load: %v %q", err, body)
	}
	if a := alertAction("goblin-watch"); a != "allowed" {
		t.Errorf("detect profile alert action %q, want allowed", a)
	}
	cmd.Process.Kill()
	cmd.Wait()

	// prevent: the answer is dropped and a "blocked" alert is logged
	prev := uses["prevent"]
	cmd = start(prev)
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	queueTo(prev)
	body, err = fetch()
	if strings.Contains(body, "uid=0(root)") {
		t.Errorf("prevent profile: page was not blocked")
	}
	if a := alertAction("goblin-prevent"); a != "blocked" {
		t.Errorf("prevent profile alert action %q, want blocked", a)
	}
	t.Logf("prevent fetch: err=%v body=%d bytes", err, len(body))
}

func tail(s string, n int) string {
	l := strings.Split(strings.TrimSpace(s), "\n")
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, "\n")
}
