package api

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render"
)

// checkExposition parses every line as the Prometheus text format 0.0.4:
// "# HELP name text", "# TYPE name type" (before the family's samples) or
// "name{labels} value". It returns the sample lines.
func checkExposition(t *testing.T, body string) []string {
	t.Helper()
	name := `[a-zA-Z_:][a-zA-Z0-9_:]*`
	label := `[a-zA-Z_][a-zA-Z0-9_]*="(?:[^"\\\n]|\\["\\n])*"`
	help := regexp.MustCompile(`^# HELP (` + name + `) [^\n]*$`)
	typ := regexp.MustCompile(`^# TYPE (` + name + `) (counter|gauge|untyped)$`)
	sample := regexp.MustCompile(`^(` + name + `)(\{` + label + `(?:,` + label + `)*\})? (NaN|[+-]Inf|-?[0-9.]+(?:[eE][+-]?[0-9]+)?)$`)
	typed := map[string]bool{}
	var samples []string
	if !strings.HasSuffix(body, "\n") {
		t.Fatalf("output does not end with a newline")
	}
	for i, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		switch {
		case help.MatchString(line):
		case typ.MatchString(line):
			n := typ.FindStringSubmatch(line)[1]
			if typed[n] {
				t.Fatalf("line %d: second TYPE for %s", i+1, n)
			}
			typed[n] = true
		case sample.MatchString(line):
			if n := sample.FindStringSubmatch(line)[1]; !typed[n] {
				t.Fatalf("line %d: sample of %s before its TYPE", i+1, n)
			}
			samples = append(samples, line)
		default:
			t.Fatalf("line %d is not valid exposition format: %q", i+1, line)
		}
	}
	return samples
}

func TestPromWriterEscaping(t *testing.T) {
	var p promWriter
	p.family("goblin_x", "gauge", "a \\ help\nline")
	p.sample("goblin_x", 1.5, "name", `say "hi" \ bye`+"\n")
	p.sample("goblin_x", 2)
	got := p.b.String()
	want := "# HELP goblin_x a \\\\ help\\nline\n# TYPE goblin_x gauge\n" +
		`goblin_x{name="say \"hi\" \\ bye\n"} 1.5` + "\n" + "goblin_x 2\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	checkExposition(t, got)
}

// fakeSysNet creates /sys/class/net counters for every device of cfg.
func fakeSysNet(t *testing.T, cfg *config.Config) {
	dir := t.TempDir()
	for _, dev := range render.Devices(cfg) {
		os.MkdirAll(filepath.Join(dir, dev, "statistics"), 0o755)
		os.WriteFile(filepath.Join(dir, dev, "statistics", "rx_bytes"), []byte("1000\n"), 0o644)
		os.WriteFile(filepath.Join(dir, dev, "statistics", "tx_bytes"), []byte("2000\n"), 0o644)
		os.WriteFile(filepath.Join(dir, dev, "operstate"), []byte("up\n"), 0o644)
	}
	old := sysClassNet
	sysClassNet = dir
	t.Cleanup(func() { sysClassNet = old })
}

func TestMetricsAccess(t *testing.T) {
	a := newAuthAPI(t)
	scrape := func(header map[string]string) (int, string, string) {
		t.Helper()
		req, _ := http.NewRequest("GET", a.srv.URL+"/metrics", nil)
		for k, v := range header {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Content-Type"), string(b)
	}
	setMetrics := func(m config.Metrics) {
		t.Helper()
		cfg, _ := a.store.Running()
		cfg.System.Metrics = m
		if _, err := a.store.SaveRevision(cfg, "test", "metrics"); err != nil {
			t.Fatal(err)
		}
		fakeSysNet(t, cfg)
	}

	if code, _, _ := scrape(nil); code != 404 {
		t.Fatalf("disabled: %d, want 404", code)
	}

	setMetrics(config.Metrics{Enabled: true, Allow: []string{"10.0.0.0/8"}})
	if code, _, _ := scrape(nil); code != 403 {
		t.Fatalf("outside allow: %d, want 403", code)
	}
	if code, _, _ := scrape(map[string]string{"Authorization": "Bearer gg_nope_nope"}); code != 403 {
		t.Fatalf("bad token: %d, want 403", code)
	}

	admin := a.browser()
	admin.login("admin")
	tok := admin.expect("POST", "/api/v1/auth/tokens", map[string]any{"name": "prometheus", "readonly": true}, 201)["token"].(string)
	if code, _, _ := scrape(map[string]string{"Authorization": "Bearer " + tok}); code != 200 {
		t.Fatalf("token: %d, want 200", code)
	}

	setMetrics(config.Metrics{Enabled: true, Allow: []string{"127.0.0.0/8", "::1/128"}})
	code, ctype, body := scrape(nil)
	if code != 200 || ctype != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("allow list: %d %q\n%s", code, ctype, body)
	}
	samples := strings.Join(checkExposition(t, body), "\n")
	for _, want := range []string{
		`goblin_info{version="`,
		`goblin_interface_receive_bytes_total{interface="lan",device="`,
		`goblin_interface_up{interface="lan"`,
		"goblin_config_revision 3",
		"goblin_commits_total 3",
	} {
		if !strings.Contains(samples, want) {
			t.Errorf("missing %q in\n%s", want, samples)
		}
	}
}
