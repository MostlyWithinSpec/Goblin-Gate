package ips

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestETProSource(t *testing.T) {
	p := testPaths(t)
	p.UpdateDir = filepath.Join(t.TempDir(), "update")
	src, _ := os.ReadFile("testdata/sample.rules")
	code := "0123456789abcdef"
	var cmds []string
	u := &Updater{Paths: p, MinRules: 3, ETProCode: func() string { return code },
		Exec: func(_ context.Context, name string, args ...string) error {
			cmds = append(cmds, name+" "+strings.Join(args, " "))
			if len(args) > 0 && args[0] == "update-sources" {
				os.MkdirAll(filepath.Join(p.UpdateDir, "cache"), 0o755)
				return os.WriteFile(filepath.Join(p.UpdateDir, "cache", "index.yaml"), []byte("sources: {}\n"), 0o644)
			}
			if name == "suricata-update" {
				os.MkdirAll(filepath.Dir(p.Source), 0o755)
				return os.WriteFile(p.Source, src, 0o644)
			}
			return nil
		}}
	if err := u.UpdateOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(p.UpdateDir, "sources", "et-pro.yaml")
	b, err := os.ReadFile(source)
	if err != nil || string(b) != "params:\n  secret-code: "+code+"\nsource: et/pro\n" {
		t.Fatalf("source file %q %v", b, err)
	}
	if runtime.GOOS == "linux" {
		if st, _ := os.Stat(source); st.Mode().Perm() != 0o600 {
			t.Fatalf("source file mode %v", st.Mode().Perm())
		}
	}
	for _, c := range cmds {
		if strings.Contains(c, code) {
			t.Fatalf("code on a command line: %s", c)
		}
	}
	if cmds[0] != "suricata-update update-sources" {
		t.Fatalf("index not fetched first: %v", cmds)
	}
	if st := u.Status(); st.Ruleset != RulesetPro || st.Rules == 0 || st.LastError != "" {
		t.Fatalf("status %+v", st)
	}

	// the index is cached: the next update doesn't fetch it again
	cmds = nil
	u.UpdateOnce(context.Background())
	if strings.Contains(strings.Join(cmds, "\n"), "update-sources") {
		t.Fatalf("index fetched again: %v", cmds)
	}

	// code removed: back to ET Open
	code = ""
	if err := u.UpdateOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("ET Pro source left behind")
	}
	if st := u.Status(); st.Ruleset != RulesetOpen {
		t.Fatalf("status %+v", st)
	}
}

func TestETProErrorsAreScrubbed(t *testing.T) {
	p := testPaths(t)
	p.UpdateDir = t.TempDir()
	os.MkdirAll(filepath.Join(p.UpdateDir, "cache"), 0o755)
	os.WriteFile(filepath.Join(p.UpdateDir, "cache", "index.yaml"), nil, 0o644)
	code := "feedfacecafebeef"
	u := &Updater{Paths: p, ETProCode: func() string { return code },
		Exec: func(context.Context, string, ...string) error {
			return errors.New("suricata-update: 403 for https://rules.emergingthreatspro.com/" + code + "/suricata-7.0.3/etpro.rules.tar.gz")
		}}
	err := u.UpdateOnce(context.Background())
	if err == nil || strings.Contains(err.Error(), code) || strings.Contains(u.Status().LastError, code) {
		t.Fatalf("code leaked: %v / %q", err, u.Status().LastError)
	}

	// an invalid code is never written into the source file
	code = "abc\nsource: evil"
	if err := u.UpdateOnce(context.Background()); err == nil {
		t.Fatal("invalid code accepted")
	}
	if b, _ := os.ReadFile(filepath.Join(p.UpdateDir, "sources", "et-pro.yaml")); strings.Contains(string(b), "evil") {
		t.Fatalf("invalid code written: %q", b)
	}
}
