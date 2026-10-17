package api

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/blocks"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// recordingNft records nft scripts instead of running them.
type recordingNft struct {
	mu      sync.Mutex
	scripts []string
}

func (n *recordingNft) run(_ context.Context, s []byte) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.scripts = append(n.scripts, string(s))
	return nil
}

func (n *recordingNft) all() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return strings.Join(n.scripts, "")
}

func withBlocks(t *testing.T, s *Server) *recordingNft {
	t.Helper()
	nft := &recordingNft{}
	s.Blocks = &blocks.Manager{Path: filepath.Join(t.TempDir(), "blocks.json"), Run: nft.run}
	s.LiveAddrs = func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("203.0.113.2")} } // the DHCP WAN address
	return nft
}

func TestBlocksAdmin(t *testing.T) {
	a := newAuthAPI(t)
	nft := withBlocks(t, a.server)
	b := a.browser()
	b.login("admin")

	out := b.expect("POST", "/api/v1/blocks", map[string]any{"ip": "198.51.100.7", "minutes": 60, "reason": "port scan"}, 201)
	if out["ip"] != "198.51.100.7" || out["source"] != "admin" || out["created_by"] != "admin" || out["expires_in"].(float64) < 3590 {
		t.Fatalf("added: %v", out)
	}
	if !strings.Contains(nft.all(), "block_tmp { 198.51.100.7 timeout 3600s }") {
		t.Fatalf("nft:\n%s", nft.all())
	}
	list := b.expect("GET", "/api/v1/blocks", nil, 200)["blocks"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["reason"] != "port scan" {
		t.Fatalf("list: %v", list)
	}

	for _, bad := range []map[string]any{
		{"ip": "198.51.100.0/24", "minutes": 5, "reason": "x"},
		{"ip": "198.51.100.8", "minutes": 0, "reason": "x"},
		{"ip": "198.51.100.8", "minutes": 10081, "reason": "x"},
		{"ip": "198.51.100.8", "minutes": 5, "reason": " "},
	} {
		b.expect("POST", "/api/v1/blocks", bad, 400)
	}
	for ip, why := range map[string]string{
		"192.168.1.1":  "address of the box",
		"203.0.113.2":  "box or one of its gateways",
		"127.0.0.1":    "special",
		"192.168.1.77": "own networks",
	} {
		out := b.expect("POST", "/api/v1/blocks", map[string]any{"ip": ip, "minutes": 5, "reason": "x"}, 403)
		msg, _ := out["error"].(map[string]any)["message"].(string)
		if errCode(out) != "protected_target" || !strings.Contains(msg, why) {
			t.Fatalf("%s: %v", ip, out)
		}
	}
	// an admin may block a LAN host when asking explicitly
	b.expect("POST", "/api/v1/blocks", map[string]any{"ip": "192.168.1.77", "minutes": 5, "reason": "infected", "allow_internal": true}, 201)

	viewer := a.browser()
	viewer.login("viewer")
	viewer.expect("GET", "/api/v1/blocks", nil, 200)
	viewer.expect("POST", "/api/v1/blocks", map[string]any{"ip": "198.51.100.9", "minutes": 5, "reason": "x"}, 403)

	b.expect("DELETE", "/api/v1/blocks/198.51.100.7", nil, 204)
	b.expect("DELETE", "/api/v1/blocks/198.51.100.7", nil, 404)
	if !strings.Contains(nft.all(), "delete element inet goblin block_tmp { 198.51.100.7 }") {
		t.Fatalf("nft:\n%s", nft.all())
	}

	audit, _ := os.ReadFile(filepath.Join(a.dir, "audit.log"))
	for _, want := range []string{`"event":"block_add"`, `"result":"added"`, `"result":"refused: an address of the box"`, `"event":"block_remove"`} {
		if !strings.Contains(string(audit), want) {
			t.Fatalf("audit lacks %s:\n%s", want, audit)
		}
	}
}

func TestBlocksPlugin(t *testing.T) {
	e := newPluginEnv(t)
	withBlocks(t, e.server)
	run, _ := e.store.Running()
	run.Plugins = []config.Plugin{{Name: "hello", Enabled: true, Capabilities: []string{"events:read"}}}
	if _, err := e.store.SaveRevision(run, "test", "plugin"); err != nil {
		t.Fatal(err)
	}
	e.sync()
	sock := e.socketAs("hello")
	// without blocks:write
	if code, out := call(t, sock, "POST", "/api/v1/blocks", map[string]any{"ip": "198.51.100.7", "minutes": 5, "reason": "x"}); code != 403 || errCode(out) != "scope_denied" {
		t.Fatalf("no capability: %d %v", code, out)
	}
	if code, _ := call(t, sock, "GET", "/api/v1/blocks", nil); code != 403 {
		t.Fatalf("no blocks:read: %d", code)
	}
}

func TestBlocksPluginWrite(t *testing.T) {
	e := newPluginEnv(t)
	withBlocks(t, e.server)
	// the test plugin's manifest asks for these
	manifest := strings.Replace(testPluginManifest, `"notify:send"]`, `"notify:send", "blocks:read", "blocks:write"]`, 1)
	if err := os.WriteFile(filepath.Join(e.mgr.ManifestDir, "hello", "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	run, _ := e.store.Running()
	run.Plugins = []config.Plugin{{Name: "hello", Enabled: true, Capabilities: []string{"blocks:read", "blocks:write"}}}
	if _, err := e.store.SaveRevision(run, "test", "plugin"); err != nil {
		t.Fatal(err)
	}
	e.sync()
	sock := e.socketAs("hello")

	code, out := call(t, sock, "POST", "/api/v1/blocks", map[string]any{"ip": "198.51.100.7", "minutes": 30, "reason": "ET SCAN"})
	if code != 201 || out["source"] != "plugin:hello" || out["created_by"] != "hello" {
		t.Fatalf("plugin block: %d %v", code, out)
	}
	// never internal, not even when asking
	if code, _ := call(t, sock, "POST", "/api/v1/blocks", map[string]any{"ip": "192.168.1.77", "minutes": 5, "reason": "x"}); code != 403 {
		t.Fatalf("internal: %d", code)
	}
	if code, _ := call(t, sock, "POST", "/api/v1/blocks", map[string]any{"ip": "192.168.1.77", "minutes": 5, "reason": "x", "allow_internal": true}); code != 403 {
		t.Fatalf("allow_internal from a plugin: %d", code)
	}
	// a plugin cannot lift an admin's block
	if _, err := e.server.Blocks.Add(context.Background(), blocks.Block{IP: netip.MustParseAddr("198.51.100.8"), Reason: "admin", Source: "admin", CreatedBy: "admin"}, 3600e9, 0); err != nil {
		t.Fatal(err)
	}
	if code, _ := call(t, sock, "DELETE", "/api/v1/blocks/198.51.100.8", nil); code != 403 {
		t.Fatalf("lift admin block: %d", code)
	}
	if code, _ := call(t, sock, "DELETE", "/api/v1/blocks/198.51.100.7", nil); code != 204 {
		t.Fatalf("lift own block: %d", code)
	}
	// rate limit: PluginRate per hour
	limited := false
	for i := range blocks.PluginRate + 1 {
		ip := netip.AddrFrom4([4]byte{198, 18, 0, byte(i + 1)})
		code, _ := call(t, sock, "POST", "/api/v1/blocks", map[string]any{"ip": ip.String(), "minutes": 5, "reason": "x"})
		if code == 429 {
			limited = true
			if i != blocks.PluginRate-1 { // the first block above counts too
				t.Fatalf("limited after %d blocks", i+1)
			}
			break
		}
	}
	if !limited {
		t.Fatal("no rate limit")
	}
}
