package api

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/devices"
)

const annotatePluginManifest = `{
  "name": "unifi",
  "version": "1.0.0",
  "capabilities": ["devices:annotate"],
  "settings_schema": {"type": "object", "properties": {}}
}`

func TestPluginDeviceAnnotations(t *testing.T) {
	e := newPluginEnv(t)
	addPlugin(t, e, "unifi", annotatePluginManifest)
	inv := newDeviceStore(t)
	seedLease(t, inv, "192.168.1.50", "aa:bb:cc:00:00:01")
	ann := &devices.AnnotationStore{Path: filepath.Join(e.dir, "ann.json")}
	e.server.Devices, e.server.Annotations = inv, ann
	run, _ := e.store.Running()
	run.Plugins = []config.Plugin{
		{Name: "unifi", Enabled: true, Capabilities: []string{"devices:annotate"}},
		{Name: "hello", Enabled: true, Capabilities: []string{"events:read"}},
	}
	if _, err := e.store.SaveRevision(run, "test", "plugins"); err != nil {
		t.Fatal(err)
	}
	e.sync()
	sock := e.socketAs("unifi")

	set := map[string]any{"devices": map[string]any{
		"AA-BB-CC-00-00-01": map[string]string{"switch": "Office switch", "port": "7", "speed_mbps": "1000"},
		"aa:bb:cc:00:00:02": map[string]string{"ap": "Hall AP", "ssid": "home"},
	}}
	if code, out := call(t, sock, "PUT", "/api/v1/plugin/devices/annotations", set); code != 200 || out["changed"] != true || out["devices"] != float64(2) {
		t.Fatalf("set: %d %v", code, out)
	}
	if code, out := call(t, sock, "PUT", "/api/v1/plugin/devices/annotations", set); code != 200 || out["changed"] != false {
		t.Fatalf("same again: %d %v", code, out)
	}
	code, out := call(t, sock, "GET", "/api/v1/plugin/devices/annotations", nil)
	if d, _ := out["devices"].(map[string]any); code != 200 || len(d) != 2 || d["aabbcc000001"] == nil {
		t.Fatalf("get: %d %v", code, out)
	}
	// refused: bad MAC, bad field name, too many fields, a control character
	for _, f := range []map[string]any{
		{"nope": map[string]string{"a": "b"}},
		{"aa:bb:cc:00:00:01": map[string]string{"Port": "7"}},
		{"aa:bb:cc:00:00:01": map[string]string{"a": "1", "b": "2", "c": "3", "d": "4", "e": "5", "f": "6", "g": "7", "h": "8", "i": "9"}},
		{"aa:bb:cc:00:00:01": map[string]string{"a": "x\ny"}},
		{"aa:bb:cc:00:00:01": map[string]string{"a": strings.Repeat("x", 65)}},
	} {
		if code, out := call(t, sock, "PUT", "/api/v1/plugin/devices/annotations", map[string]any{"devices": f}); code != 400 {
			t.Errorf("%v: %d %v", f, code, out)
		}
	}
	// a plugin without the capability may not
	if code, _ := call(t, e.socketAs("hello"), "PUT", "/api/v1/plugin/devices/annotations", set); code != 403 {
		t.Errorf("hello: %d", code)
	}

	// the device list shows them next to the device
	b := e.browser()
	b.login("admin")
	resp := b.expect("GET", "/api/v1/devices", nil, 200)
	if upd, _ := resp["annotations_updated"].(map[string]any); upd["unifi"] == nil {
		t.Errorf("annotations_updated %v", resp["annotations_updated"])
	}
	list := resp["devices"].([]any)
	if len(list) != 1 {
		t.Fatalf("devices %v", list)
	}
	got := list[0].(map[string]any)["annotations"].(map[string]any)["unifi"].(map[string]any)
	if got["port"] != "7" || got["switch"] != "Office switch" {
		t.Fatalf("annotations %v", list[0])
	}

	// withdrawing the capability hides them, and pruning drops them
	run, _ = e.store.Running()
	run.Plugins[0].Capabilities = nil
	e.store.SaveRevision(run, "test", "no annotate")
	list = b.expect("GET", "/api/v1/devices", nil, 200)["devices"].([]any)
	if _, has := list[0].(map[string]any)["annotations"]; has {
		t.Fatalf("still shown: %v", list[0])
	}
	if dropped := ann.Prune(AnnotatingPlugins(run)); len(dropped) != 1 || dropped[0] != "unifi" {
		t.Fatalf("pruned %v", dropped)
	}
}
