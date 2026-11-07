package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

const switchPluginManifest = `{
  "name": "switcher",
  "version": "1.0.0",
  "capabilities": ["policies:toggle", "config:read"],
  "settings_schema": {"type": "object", "properties": {
    "broker": {"type": "string", "maxLength": 64},
    "password": {"type": "string", "secret": true, "maxLength": 64}
  }}
}`

func addPlugin(t *testing.T, e *pluginEnv, name, manifest string) {
	t.Helper()
	share := filepath.Join(e.mgr.ManifestDir, name)
	lib := filepath.Join(e.mgr.ProgramDir, name)
	for _, d := range []string{share, lib} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(share, "plugin.json"), []byte(manifest), 0o644)
	os.WriteFile(filepath.Join(lib, "run"), []byte("#!/bin/sh\n"), 0o755)
	e.mgr.Rescan()
}

func TestPluginSecretSettings(t *testing.T) {
	e := newPluginEnv(t)
	addPlugin(t, e, "switcher", switchPluginManifest)
	b := e.browser()
	b.login("admin")
	b.expect("POST", "/api/v1/plugins/switcher/enable", nil, 200)
	b.expect("PUT", "/api/v1/plugins/switcher/settings", map[string]any{"settings": map[string]any{"broker": "10.0.0.5:1883", "password": "hunter2-secret"}}, 200)
	cand, _ := e.store.Candidate()
	p := cand.Plugins[0]
	if p.Secrets["password"] != "hunter2-secret" || p.Settings["password"] != nil || p.Settings["broker"] != "10.0.0.5:1883" {
		t.Fatalf("stored: %+v", p)
	}
	// the list says it is set, never what it is
	_, bodyMap, _ := b.do("GET", "/api/v1/plugins", nil)
	body, _ := json.Marshal(bodyMap)
	if strings.Contains(string(body), "hunter2") || !strings.Contains(string(body), `"secrets_set":["password"]`) {
		t.Fatalf("list: %s", body)
	}
	// saving the form again with the secret blank keeps it; null clears it
	b.expect("PUT", "/api/v1/plugins/switcher/settings", map[string]any{"settings": map[string]any{"broker": "10.0.0.6:1883", "password": ""}}, 200)
	cand, _ = e.store.Candidate()
	if cand.Plugins[0].Secrets["password"] != "hunter2-secret" || cand.Plugins[0].Settings["broker"] != "10.0.0.6:1883" {
		t.Fatalf("blank secret: %+v", cand.Plugins[0])
	}
	b.expect("POST", "/api/v1/commit", map[string]string{"comment": "plugin"}, 200)

	// a read-only user and another plugin with config:read do not see it
	viewer := e.browser()
	viewer.login("viewer")
	_, vMap, _ := viewer.do("GET", "/api/v1/config/running", nil)
	vbody, _ := json.Marshal(vMap)
	if strings.Contains(string(vbody), "hunter2") || !strings.Contains(string(vbody), `"switcher"`) {
		t.Fatalf("viewer: %s", vbody)
	}
	run, _ := e.store.Running()
	run.Plugins = append(run.Plugins, config.Plugin{Name: "hello", Enabled: true, Capabilities: []string{"config:read"}})
	e.store.SaveRevision(run, "test", "two plugins")
	e.sync()
	resp, err := http.Get(e.socketAs("hello") + "/api/v1/config/running")
	if err != nil {
		t.Fatal(err)
	}
	other, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || strings.Contains(string(other), "hunter2") {
		t.Fatalf("another plugin: %d %s", resp.StatusCode, other)
	}
	// the plugin itself gets it with its settings
	code, self := call(t, e.socketAs("switcher"), "GET", "/api/v1/plugin/self", nil)
	if code != 200 || self["settings"].(map[string]any)["password"] != "hunter2-secret" {
		t.Fatalf("self: %d %v", code, self)
	}

	// a secret in the plain settings, or a secret the manifest does not have
	run, _ = e.store.Running()
	run.Plugins[0].Settings = map[string]any{"password": "x"}
	run.Plugins[0].Secrets = map[string]string{"broker": "y"}
	errs := config.CheckInstalledPlugins(run)
	if !hasPath(errs, "plugins[0].settings") || !hasPath(errs, "plugins[0].secrets") {
		t.Fatalf("misplaced secrets: %v", errs)
	}
}

func hasPath(errs []config.ValidationError, path string) bool {
	for _, e := range errs {
		if e.Path == path {
			return true
		}
	}
	return false
}

func TestPluginPolicySwitch(t *testing.T) {
	e := newPluginEnv(t)
	addPlugin(t, e, "switcher", switchPluginManifest)
	run, _ := e.store.Running()
	run.Policies = append(run.Policies,
		config.Policy{ID: 50, Name: "guest-internet", Enabled: true, SrcIntf: []string{"lan"}, DstIntf: []string{"wan1"}, SrcAddr: []string{"all"}, DstAddr: []string{"all"}, Service: []string{"any"}, Action: "accept"},
		config.Policy{ID: 51, Name: "not-this-one", Enabled: true, SrcIntf: []string{"lan"}, DstIntf: []string{"wan1"}, SrcAddr: []string{"all"}, DstAddr: []string{"all"}, Service: []string{"any"}, Action: "accept"},
	)
	run.Plugins = []config.Plugin{{Name: "switcher", Enabled: true, Capabilities: []string{"policies:toggle"}, Policies: []uint32{50}}}
	if errs := config.Validate(run); len(errs) > 0 {
		t.Fatal(errs)
	}
	if _, err := e.store.SaveRevision(run, "test", "switchable"); err != nil {
		t.Fatal(err)
	}
	e.sync()
	// an admin's half-done edit in the candidate
	cand, _ := e.store.Running()
	cand.Policies[len(cand.Policies)-1].Name = "renamed-in-candidate"
	e.store.SetCandidate(cand)

	sock := e.socketAs("switcher")
	code, out := call(t, sock, "GET", "/api/v1/plugin/policies", nil)
	list, _ := out["policies"].([]any)
	if code != 200 || len(list) != 1 || list[0].(map[string]any)["name"] != "guest-internet" || list[0].(map[string]any)["enabled"] != true {
		t.Fatalf("list: %d %v", code, out)
	}
	code, out = call(t, sock, "PUT", "/api/v1/plugin/policies/50", map[string]bool{"enabled": false})
	if code != 200 || out["changed"] != true || out["revision"] == nil {
		t.Fatalf("switch: %d %v", code, out)
	}
	run, _ = e.store.Running()
	cand, _ = e.store.Candidate()
	for _, cfg := range []*config.Config{run, cand} {
		for _, p := range cfg.Policies {
			if p.ID == 50 && p.Enabled {
				t.Fatal("policy 50 still on")
			}
			if p.ID == 51 && !p.Enabled {
				t.Fatal("policy 51 switched")
			}
		}
	}
	if cand.Policies[len(cand.Policies)-1].Name != "renamed-in-candidate" {
		t.Fatal("the candidate's own edit was lost")
	}
	if run.Policies[len(run.Policies)-1].Name != "not-this-one" {
		t.Fatal("the candidate's edit was committed")
	}
	revs, _ := e.store.ListRevisions()
	if last := revs[len(revs)-1]; last.User != "plugin:switcher" || !strings.Contains(last.Comment, `turned policy "guest-internet" off`) {
		t.Fatalf("revision: %+v", last)
	}
	// already off: nothing to commit
	if code, out := call(t, sock, "PUT", "/api/v1/plugin/policies/50", map[string]bool{"enabled": false}); code != 200 || out["changed"] != false {
		t.Fatalf("no-op: %d %v", code, out)
	}
	// a policy it was not given, a bad body
	if code, out := call(t, sock, "PUT", "/api/v1/plugin/policies/51", map[string]bool{"enabled": false}); code != 403 || errCode(out) != "scope_denied" {
		t.Fatalf("other policy: %d %v", code, out)
	}
	if code, _ := call(t, sock, "PUT", "/api/v1/plugin/policies/50", map[string]any{}); code != 400 {
		t.Fatalf("no enabled: %d", code)
	}
	audit, _ := os.ReadFile(filepath.Join(e.dir, "audit.log"))
	for _, want := range []string{`"event":"policy_switch"`, `"result":"switched"`, `"result":"not_allowed"`} {
		if !strings.Contains(string(audit), want) {
			t.Fatalf("audit lacks %s", want)
		}
	}

	// without the capability the routes are closed
	run, _ = e.store.Running()
	run.Plugins[0].Capabilities = nil
	run.Plugins[0].Policies = nil
	e.store.SaveRevision(run, "test", "no toggle")
	e.sync()
	if code, out := call(t, sock, "GET", "/api/v1/plugin/policies", nil); code != 403 || errCode(out) != "scope_denied" {
		t.Fatalf("without capability: %d %v", code, out)
	}
}

func TestPluginPoliciesValidation(t *testing.T) {
	c := config.Default()
	c.AllowNoAdmin = true
	id := c.Policies[0].ID
	for name, tc := range map[string]struct {
		p    config.Plugin
		path string
	}{
		"unknown policy":    {config.Plugin{Name: "x", Capabilities: []string{"policies:toggle"}, Policies: []uint32{id, 9999}}, "plugins[0].policies[1]"},
		"twice":             {config.Plugin{Name: "x", Capabilities: []string{"policies:toggle"}, Policies: []uint32{id, id}}, "plugins[0].policies[1]"},
		"needs capability":  {config.Plugin{Name: "x", Enabled: true, Policies: []uint32{id}}, "plugins[0].policies"},
		"unknown cap stays": {config.Plugin{Name: "x", Capabilities: []string{"policies:nope"}}, "plugins[0].capabilities[0]"},
	} {
		cfg := c.Clone()
		cfg.Plugins = []config.Plugin{tc.p}
		if !hasPath(config.Validate(cfg), tc.path) {
			t.Errorf("%s: no error at %s: %v", name, tc.path, config.Validate(cfg))
		}
	}
	cfg := c.Clone()
	cfg.Plugins = []config.Plugin{{Name: "x", Enabled: true, Capabilities: []string{"policies:toggle"}, Policies: []uint32{id}}}
	if errs := config.Validate(cfg); len(errs) > 0 {
		t.Errorf("valid: %v", errs)
	}
}

func TestSwitchLimiter(t *testing.T) {
	var l switchLimiter
	now := time.Now()
	for i := 0; i < MaxPolicySwitchesPerHour; i++ {
		if !l.allow("p", now) {
			t.Fatalf("switch %d refused", i)
		}
	}
	if l.allow("p", now) {
		t.Fatal("over the limit")
	}
	if !l.allow("other", now) {
		t.Fatal("the limit is per plugin")
	}
	if !l.allow("p", now.Add(time.Hour)) {
		t.Fatal("the hour passed")
	}
}

func TestPluginPoliciesSettingAPI(t *testing.T) {
	e := newPluginEnv(t)
	addPlugin(t, e, "switcher", switchPluginManifest)
	run, _ := e.store.Running()
	run.Policies = append(run.Policies, config.Policy{ID: 50, Name: "guest-internet", Enabled: true, SrcIntf: []string{"lan"}, DstIntf: []string{"wan1"}, SrcAddr: []string{"all"}, DstAddr: []string{"all"}, Service: []string{"any"}, Action: "accept"})
	e.store.SaveRevision(run, "test", "policy")
	b := e.browser()
	b.login("admin")
	// enabled without policies:toggle: listing policies is refused
	b.expect("POST", "/api/v1/plugins/switcher/enable", map[string]any{"capabilities": []string{"config:read"}}, 200)
	if out := b.expect("PUT", "/api/v1/plugins/switcher/policies", map[string]any{"policies": []int{50}}, 400); errCode(out) != "validation_failed" {
		t.Fatalf("without the capability: %v", out)
	}
	b.expect("POST", "/api/v1/plugins/switcher/enable", nil, 200)
	b.expect("PUT", "/api/v1/plugins/switcher/policies", map[string]any{"policies": []int{50}}, 200)
	b.expect("PUT", "/api/v1/plugins/switcher/policies", map[string]any{"policies": []int{999}}, 400)
	cand, _ := e.store.Candidate()
	if len(cand.Plugins) != 1 || len(cand.Plugins[0].Policies) != 1 || cand.Plugins[0].Policies[0] != 50 {
		t.Fatalf("candidate: %+v", cand.Plugins)
	}
	list := b.expect("GET", "/api/v1/plugins", nil, 200)["plugins"].([]any)
	for _, p := range list {
		if m := p.(map[string]any); m["name"] == "switcher" && len(m["policies"].([]any)) != 1 {
			t.Fatalf("list: %v", m)
		}
	}
	viewer := e.browser()
	viewer.login("viewer")
	viewer.expect("PUT", "/api/v1/plugins/switcher/policies", map[string]any{"policies": []int{}}, 403)
}
