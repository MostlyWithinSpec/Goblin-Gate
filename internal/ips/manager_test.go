package ips

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testPaths(t *testing.T) Paths {
	dir := t.TempDir()
	p := Paths{
		Source:         filepath.Join(dir, "rules", "suricata.rules"),
		Classification: "testdata/classification.config",
		Dir:            filepath.Join(dir, "etc"),
		RulesDir:       filepath.Join(dir, "lib"),
	}
	os.MkdirAll(p.Dir, 0o755)
	os.WriteFile(filepath.Join(p.Dir, "home.profile.json"), []byte(`{"name":"home","mode":"prevent"}`), 0o644)
	os.WriteFile(filepath.Join(p.Dir, "kids.profile.json"), []byte(`{"name":"kids","mode":"detect"}`), 0o644)
	return p
}

func TestBuildRules(t *testing.T) {
	p := testPaths(t)
	// no download yet: an empty rules file so Suricata can start
	if _, err := BuildRules(p, "home"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(p.RulesDir, "home.rules")); !strings.HasPrefix(string(b), "# Generated") || strings.Count(string(b), "\n") != 1 {
		t.Fatalf("empty rules: %q", b)
	}
	src, _ := os.ReadFile("testdata/sample.rules")
	os.MkdirAll(filepath.Dir(p.Source), 0o755)
	os.WriteFile(p.Source, src, 0o644)
	st, err := BuildRules(p, "home")
	if err != nil || st.Drop != 3 {
		t.Fatalf("%+v %v", st, err)
	}
	if _, err := BuildRules(p, "nope"); err == nil {
		t.Fatal("unknown instance accepted")
	}
	insts, _ := Instances(p)
	if strings.Join(insts, ",") != "home,kids" {
		t.Fatalf("instances %v", insts)
	}
}

func TestUpdateOnce(t *testing.T) {
	p := testPaths(t)
	src, _ := os.ReadFile("testdata/sample.rules")
	var cmds []string
	u := &Updater{Paths: p, MinRules: 3, Exec: func(_ context.Context, name string, args ...string) error {
		cmds = append(cmds, name+" "+strings.Join(args, " "))
		if name == "suricata-update" {
			os.MkdirAll(filepath.Dir(p.Source), 0o755)
			return os.WriteFile(p.Source, src, 0o644)
		}
		return nil
	}}
	if err := u.UpdateOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"suricata-update --no-test --no-reload -o " + filepath.Dir(p.Source),
		"systemctl reload goblin-suricata@home.service",
		"systemctl reload goblin-suricata@kids.service",
	}
	if strings.Join(cmds, "\n") != strings.Join(want, "\n") {
		t.Fatalf("commands:\n%s", strings.Join(cmds, "\n"))
	}
	if b, _ := os.ReadFile(filepath.Join(p.RulesDir, "kids.rules")); strings.Contains(string(b), "\ndrop ") {
		t.Fatal("detect profile got drop rules")
	}
}

// suricata-update exits 0 offline and leaves only Suricata's own rules:
// that is a failed update (retried soon), not a fresh download.
func TestUpdateOnceRejectsAnOfflineDownload(t *testing.T) {
	p := testPaths(t)
	var cmds []string
	u := &Updater{Paths: p, Exec: func(_ context.Context, name string, args ...string) error {
		cmds = append(cmds, name)
		if name == "suricata-update" {
			os.MkdirAll(filepath.Dir(p.Source), 0o755)
			return os.WriteFile(p.Source, []byte(`alert http any any -> any any (msg:"SURICATA HTTP unable to match response"; sid:2221000;)`+"\n"), 0o644)
		}
		return nil
	}}
	err := u.UpdateOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "only 0 usable rules") {
		t.Fatalf("err = %v", err)
	}
	if strings.Join(cmds, ",") != "suricata-update" {
		t.Fatalf("instances reloaded after a failed download: %v", cmds)
	}
}
