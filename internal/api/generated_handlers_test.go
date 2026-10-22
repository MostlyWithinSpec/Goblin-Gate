package api

import (
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/commit"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/kea"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/nft"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/system"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/wireguard"
)

// secretRenderer writes a file with every kind of secret line the redaction
// must catch.
type secretRenderer struct{}

func (secretRenderer) Name() string { return "test" }
func (secretRenderer) Render(cfg *config.Config) (map[string][]byte, error) {
	return map[string][]byte{"/etc/test.conf": []byte("[Peer]\nPrivateKey = aGVsbG8=\nPresharedKey=c2VjcmV0\npassword: hunter2\nauth-key = tskey-abc\nMONITOR apc@nas:3493 1 mon \"nut-pass\" secondary\nMONITOR ups@localhost 1 goblin-monitor bare-pass primary\nPrivateKeyFile=/etc/keys/wg0.key\nhost = " + cfg.System.Hostname + "\n")}, nil
}

func newGeneratedAPI(t *testing.T) *testAPI {
	return newTestAPIWith(t, func(s *Server) {
		s.Commit.Renderers = []commit.Renderer{nft.Renderer{}, kea.Renderer{}, system.Renderer{}, wireguard.Renderer{}, secretRenderer{}}
	})
}

func TestGeneratedConfig(t *testing.T) {
	a := newGeneratedAPI(t)
	out := a.expect("GET", "/api/v1/system/generated", nil, 200)
	files, _ := out["files"].([]any)
	byPath := map[string]map[string]any{}
	for _, f := range files {
		m := f.(map[string]any)
		byPath[m["path"].(string)] = m
	}
	if byPath[nft.Path] == nil || byPath[nft.Path]["renderer"] != "nft" || byPath["/etc/test.conf"]["redacted"] != true || byPath[nft.Path]["redacted"] != false {
		t.Fatalf("list: %v", out)
	}

	f := a.expect("GET", "/api/v1/system/generated/file?path=/etc/test.conf", nil, 200)
	content := f["content"].(string)
	for _, secret := range []string{"aGVsbG8=", "c2VjcmV0", "hunter2", "tskey-abc", "nut-pass", "bare-pass"} {
		if strings.Contains(content, secret) {
			t.Fatalf("secret %q not redacted:\n%s", secret, content)
		}
	}
	if !strings.Contains(content, "PrivateKeyFile=/etc/keys/wg0.key") || f["redacted"] != true {
		t.Fatalf("redaction went too far or not flagged:\n%s", content)
	}
	if f := a.expect("GET", "/api/v1/system/generated/file?path="+nft.Path, nil, 200); !strings.Contains(f["content"].(string), "table inet goblin") {
		t.Fatalf("nft file: %v", f)
	}
	a.expect("GET", "/api/v1/system/generated/file?path=/nope", nil, 404)
	a.expect("GET", "/api/v1/system/generated?config=other", nil, 400)

	// the candidate differs from running only in the hostname
	cfg, _ := a.store.Candidate()
	cfg.System.Hostname = "goblin-test"
	if err := a.store.SetCandidate(cfg); err != nil {
		t.Fatal(err)
	}
	d := a.expect("GET", "/api/v1/system/generated/diff?path=/etc/test.conf", nil, 200)
	diff := d["diff"].(string)
	if !strings.Contains(diff, "+host = goblin-test") || !strings.Contains(diff, "@@ ") || strings.Contains(diff, "hunter2") {
		t.Fatalf("diff:\n%s", diff)
	}
	if d := a.expect("GET", "/api/v1/system/generated/diff?path="+nft.Path, nil, 200); d["diff"] != "" {
		t.Fatalf("unchanged file has a diff: %v", d)
	}
	if f := a.expect("GET", "/api/v1/system/generated/file?path=/etc/test.conf&config=candidate", nil, 200); !strings.Contains(f["content"].(string), "goblin-test") {
		t.Fatalf("candidate file: %v", f)
	}
}

func TestUnifiedDiff(t *testing.T) {
	a := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n"
	b := "1\n2\nthree\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\n"
	got, err := unifiedDiff("f", a, b)
	if err != nil {
		t.Fatal(err)
	}
	want := "--- running/f\n+++ candidate/f\n@@ -1,6 +1,6 @@\n 1\n 2\n-3\n+three\n 4\n 5\n 6\n@@ -10,3 +10,4 @@\n 10\n 11\n 12\n+13\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if d, _ := unifiedDiff("f", a, a); d != "" {
		t.Fatalf("equal files: %q", d)
	}
	big := strings.Repeat("x\n", maxDiffLines+1)
	if _, err := unifiedDiff("f", big, strings.Repeat("y\n", maxDiffLines+1)); err == nil {
		t.Fatal("huge diff accepted")
	}
}
