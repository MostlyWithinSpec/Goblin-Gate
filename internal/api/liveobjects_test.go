package api

import (
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/liveobjects"
)

const livePluginManifest = `{
  "name": "homelab",
  "version": "1.0.0",
  "capabilities": ["objects:write"],
  "settings_schema": {"type": "object", "properties": {}}
}`

func TestPluginLiveObjects(t *testing.T) {
	e := newPluginEnv(t)
	addPlugin(t, e, "homelab", livePluginManifest)
	store := &liveobjects.Store{Path: filepath.Join(e.dir, "live.json")}
	var mu sync.Mutex
	var refilled []string
	e.server.LiveObjects = store
	e.server.RefillLive = func(objs ...string) {
		mu.Lock()
		refilled = append(refilled, objs...)
		mu.Unlock()
	}
	run, _ := e.store.Running()
	run.Plugins = []config.Plugin{
		{Name: "homelab", Enabled: true, Capabilities: []string{"objects:write"}},
		{Name: "hello", Enabled: true, Capabilities: []string{"events:read"}},
	}
	run.Addresses = append(run.Addresses,
		config.Address{Name: "containers", Type: "live", Value: "homelab"},
		config.Address{Name: "vms", Type: "live", Value: "homelab"},
		config.Address{Name: "office", Type: "host", Value: "10.9.9.9"},
	)
	if errs := config.Validate(run); len(errs) > 0 {
		t.Fatal(errs)
	}
	if _, err := e.store.SaveRevision(run, "test", "live objects"); err != nil {
		t.Fatal(err)
	}
	e.sync()
	sock := e.socketAs("homelab")

	// its objects, empty so far
	code, out := call(t, sock, "GET", "/api/v1/plugin/objects", nil)
	list, _ := out["objects"].([]any)
	if code != 200 || len(list) != 2 || list[0].(map[string]any)["name"] != "containers" || len(list[0].(map[string]any)["members"].([]any)) != 0 {
		t.Fatalf("list: %d %v", code, out)
	}
	// fill one
	code, out = call(t, sock, "PUT", "/api/v1/plugin/objects/containers", map[string]any{"members": []string{"10.20.0.5", "10.20.1.0/24", "fd00:20::7"}})
	if code != 200 || out["changed"] != true || out["members"] != float64(3) || out["added"] != float64(3) {
		t.Fatalf("set: %d %v", code, out)
	}
	if !slices.Equal(refilled, []string{"containers"}) {
		t.Errorf("refilled %v", refilled)
	}
	if !store.Contains("containers", netip.MustParseAddr("10.20.1.9")) {
		t.Error("members not stored")
	}
	// the same again: no refill, no audit
	if code, out := call(t, sock, "PUT", "/api/v1/plugin/objects/containers", map[string]any{"members": []string{"fd00:20::7", "10.20.1.0/24", "10.20.0.5"}}); code != 200 || out["changed"] != false {
		t.Fatalf("no-op: %d %v", code, out)
	}
	if len(refilled) != 1 {
		t.Errorf("a no-op refilled: %v", refilled)
	}
	// refused: another object, a static one, members too wide, no list
	for path, body := range map[string]any{
		"/api/v1/plugin/objects/office":     map[string]any{"members": []string{"10.0.0.1"}},
		"/api/v1/plugin/objects/nope":       map[string]any{"members": []string{"10.0.0.1"}},
		"/api/v1/plugin/objects/containers": map[string]any{"members": []string{"0.0.0.0/0"}},
	} {
		code, out := call(t, sock, "PUT", path, body)
		want := 403
		if strings.HasSuffix(path, "containers") {
			want = 400
		}
		if code != want {
			t.Errorf("%s: %d %v, want %d", path, code, out, want)
		}
	}
	if code, _ := call(t, sock, "PUT", "/api/v1/plugin/objects/containers", map[string]any{}); code != 400 {
		t.Errorf("no members: %d", code)
	}
	// another plugin may not write it, nor one without objects:write
	if code, _ := call(t, e.socketAs("hello"), "PUT", "/api/v1/plugin/objects/containers", map[string]any{"members": []string{"10.0.0.1"}}); code != 403 {
		t.Errorf("another plugin: %d", code)
	}
	// clearing works
	if code, out := call(t, sock, "PUT", "/api/v1/plugin/objects/containers", map[string]any{"members": []string{}}); code != 200 || out["removed"] != float64(3) {
		t.Fatalf("clear: %d %v", code, out)
	}

	// the admin list shows every live object
	b := e.browser()
	b.login("viewer")
	_, all, _ := b.do("GET", "/api/v1/live-objects", nil)
	objs, _ := all["objects"].([]any)
	if len(objs) != 2 || objs[0].(map[string]any)["plugin"] != "homelab" {
		t.Fatalf("admin list: %v", all)
	}

	audit, _ := os.ReadFile(filepath.Join(e.dir, "audit.log"))
	for _, want := range []string{`"live_object_update"`, `"result":"updated"`, `"result":"not_owned"`} {
		if !strings.Contains(string(audit), want) {
			t.Errorf("audit lacks %s", want)
		}
	}
}

func TestLiveLimiter(t *testing.T) {
	var l liveLimiter
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	for i := 0; i < MaxLiveUpdatesPerHour; i++ {
		if !l.allow("homelab", now) {
			t.Fatalf("refused update %d", i)
		}
	}
	if l.allow("homelab", now) {
		t.Error("allowed one over the limit")
	}
	if !l.allow("other", now) {
		t.Error("the limit is per plugin")
	}
	if !l.allow("homelab", now.Add(time.Hour)) {
		t.Error("the window does not move")
	}
}
