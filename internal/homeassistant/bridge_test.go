package homeassistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeAPI struct {
	mu       sync.Mutex
	wans     []WAN
	gws      []Gateway
	sys      System
	devs     []Device
	pols     []Policy
	denied   map[string]bool
	failing  map[string]bool
	switched []string
}

func (f *fakeAPI) err(what string) error {
	if f.denied[what] {
		return &APIError{Status: 403, Message: "denied"}
	}
	if f.failing[what] {
		return errors.New("socket hiccup")
	}
	return nil
}
func (f *fakeAPI) WANs(context.Context) ([]WAN, error)         { return f.wans, f.err("wan") }
func (f *fakeAPI) Gateways(context.Context) ([]Gateway, error) { return f.gws, f.err("gateways") }
func (f *fakeAPI) System(context.Context) (System, error)      { return f.sys, f.err("system") }
func (f *fakeAPI) Devices(context.Context) ([]Device, error)   { return f.devs, f.err("devices") }
func (f *fakeAPI) Policies(context.Context) ([]Policy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Policy(nil), f.pols...), f.err("policies")
}
func (f *fakeAPI) SwitchPolicy(_ context.Context, id uint32, on bool) (Policy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.denied["switch"] {
		return Policy{}, &APIError{Status: 409, Message: "a commit is waiting to be confirmed"}
	}
	for i := range f.pols {
		if f.pols[i].ID == id {
			f.pols[i].Enabled = on
			f.switched = append(f.switched, f.pols[i].Name)
			return f.pols[i], nil
		}
	}
	return Policy{}, &APIError{Status: 403, Message: "not allowed"}
}

// fakeConn records what the bridge publishes (the retained view, like a
// broker keeps it).
type fakeConn struct {
	mu       sync.Mutex
	retained map[string]string
	log      []string
	subs     []string
	msgs     chan Message
	done     chan struct{}
}

func newFakeConn() *fakeConn {
	return &fakeConn{retained: map[string]string{}, msgs: make(chan Message, 8), done: make(chan struct{})}
}
func (c *fakeConn) Publish(topic string, payload []byte, retain bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.log = append(c.log, topic)
	if retain {
		if len(payload) == 0 {
			delete(c.retained, topic)
		} else {
			c.retained[topic] = string(payload)
		}
	}
	return nil
}
func (c *fakeConn) Subscribe(f ...string) error { c.subs = append(c.subs, f...); return nil }
func (c *fakeConn) Messages() <-chan Message    { return c.msgs }
func (c *fakeConn) Done() <-chan struct{}       { return c.done }
func (c *fakeConn) Err() error                  { return errors.New("closed") }
func (c *fakeConn) Close() error                { return nil }

func (c *fakeConn) get(topic string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.retained[topic]
}

func (c *fakeConn) configs() map[string]map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]map[string]any{}
	for t, v := range c.retained {
		if strings.HasSuffix(t, "/config") {
			var m map[string]any
			json.Unmarshal([]byte(v), &m)
			out[t] = m
		}
	}
	return out
}

var now = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func testBridge(t *testing.T, api *fakeAPI, caps ...string) *Bridge {
	t.Helper()
	s, err := SettingsFrom(map[string]any{"broker": "10.0.0.5", "devices": []any{"AA-BB-CC-DD-EE-01", "aabbccddee02"}, "interval_seconds": 30.0, "away_minutes": 10.0})
	if err != nil {
		t.Fatal(err)
	}
	b := &Bridge{API: api, Settings: s, Capabilities: caps, Version: "0.2.0", StateDir: t.TempDir(), Now: func() time.Time { return now }}
	if err := b.loadState(); err != nil {
		t.Fatal(err)
	}
	b.sentCfg, b.sentVal = map[string]string{}, map[string]string{}
	return b
}

func sampleAPI() *fakeAPI {
	return &fakeAPI{
		wans: []WAN{{Name: "wan1", Device: "eth0", Up: true, Addresses: []string{"2001:db8::5/64", "24.101.68.119/24"}, Gateway: "24.101.68.1"}},
		gws:  []Gateway{{Name: "fiber", State: "up", LatencyMs: 4, LossPercent: 0}, {Name: "lte", State: "unknown"}},
		sys:  System{CPUPercent: 12.345, MemTotalBytes: 4 << 30, MemUsedBytes: 1 << 30, UptimeSeconds: 7200, Sessions: 321},
		devs: []Device{
			{MAC: "aa:bb:cc:dd:ee:01", Name: "Sam's phone", IPs: []string{"192.168.1.40"}, Interface: "lan", LastSeen: now.Add(-2 * time.Minute)},
			{MAC: "AA:BB:CC:DD:EE:02", Hostname: "tablet", LastSeen: now.Add(-time.Hour)},
		},
		pols: []Policy{{ID: 50, Name: "guest-internet", Enabled: true}},
	}
}

func TestDiscoveryAndStates(t *testing.T) {
	api := sampleAPI()
	b := testBridge(t, api, "stats:read", "devices:read", "policies:toggle")
	c := newFakeConn()
	if err := b.refresh(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	node := b.node()
	cfgs := c.configs()
	want := map[string]string{
		"homeassistant/binary_sensor/" + node + "/wan_wan1/config":               "WAN wan1",
		"homeassistant/sensor/" + node + "/wan_wan1_ipv4/config":                 "WAN wan1 IPv4 address",
		"homeassistant/binary_sensor/" + node + "/gateway_fiber/config":          "Gateway fiber",
		"homeassistant/sensor/" + node + "/gateway_lte_latency/config":           "Gateway lte latency",
		"homeassistant/sensor/" + node + "/cpu/config":                           "CPU",
		"homeassistant/sensor/" + node + "/boot_time/config":                     "Boot time",
		"homeassistant/device_tracker/" + node + "/presence_aabbccddee01/config": "Sam's phone",
		"homeassistant/device_tracker/" + node + "/presence_aabbccddee02/config": "tablet",
		"homeassistant/switch/" + node + "/policy_50/config":                     "Policy guest-internet",
	}
	for topic, name := range want {
		cfg, ok := cfgs[topic]
		if !ok || cfg["name"] != name || cfg["unique_id"] == nil || cfg["availability_topic"] != "goblin_gate/status" {
			t.Errorf("%s: %v", topic, cfg)
		}
	}
	if len(cfgs) != 15 { // WAN 2, gateways 2x3, system 4, presence 2, switch 1
		t.Errorf("%d discovery configs, want 15", len(cfgs))
	}
	sw := cfgs["homeassistant/switch/"+node+"/policy_50/config"]
	if sw["command_topic"] != "goblin_gate/policy/50/set" || sw["device"].(map[string]any)["name"] != "Goblin Gate" {
		t.Errorf("switch: %v", sw)
	}
	if _, hasDevice := cfgs["homeassistant/device_tracker/"+node+"/presence_aabbccddee01/config"]["device"]; hasDevice {
		t.Error("a tracker is filed under the box")
	}
	for topic, v := range map[string]string{
		"goblin_gate/wan/wan1/state":              "ON",
		"goblin_gate/wan/wan1/ipv4":               "24.101.68.119",
		"goblin_gate/gateway/fiber/state":         "ON",
		"goblin_gate/gateway/fiber/latency":       "4",
		"goblin_gate/gateway/lte/state":           "None",
		"goblin_gate/gateway/lte/latency":         "None",
		"goblin_gate/system/cpu":                  "12.3",
		"goblin_gate/system/memory":               "25",
		"goblin_gate/system/sessions":             "321",
		"goblin_gate/system/boot_time":            "2026-11-06T10:00:00Z",
		"goblin_gate/presence/aabbccddee01/state": "home",
		"goblin_gate/presence/aabbccddee02/state": "not_home",
		"goblin_gate/policy/50/state":             "ON",
	} {
		if got := c.get(topic); got != v {
			t.Errorf("%s = %q, want %q", topic, got, v)
		}
	}
	if a := c.get("goblin_gate/presence/aabbccddee01/attributes"); !strings.Contains(a, `"ip":"192.168.1.40"`) {
		t.Errorf("attributes: %s", a)
	}

	// nothing changed: nothing is sent again
	n := len(c.log)
	b.refresh(context.Background(), c)
	if len(c.log) != n {
		t.Errorf("unchanged refresh sent %v", c.log[n:])
	}
	// a value changes: only that is sent
	api.wans[0].Up = false
	b.refresh(context.Background(), c)
	if got := c.log[n:]; len(got) != 1 || got[0] != "goblin_gate/wan/wan1/state" {
		t.Errorf("changed refresh sent %v", got)
	}
}

func TestEntitiesFollowCapabilitiesAndErrors(t *testing.T) {
	api := sampleAPI()
	b := testBridge(t, api, "stats:read", "policies:toggle")
	c := newFakeConn()
	b.refresh(context.Background(), c)
	cfgs := c.configs()
	for topic := range cfgs {
		if strings.Contains(topic, "device_tracker") {
			t.Errorf("tracker without devices:read: %s", topic)
		}
	}
	switchTopic := "homeassistant/switch/" + b.node() + "/policy_50/config"
	if _, ok := cfgs[switchTopic]; !ok {
		t.Fatal("no switch")
	}
	// a hiccup keeps the entities; a refusal (the admin took the
	// capability back) removes them
	api.failing = map[string]bool{"policies": true}
	b.refresh(context.Background(), c)
	if _, ok := c.configs()[switchTopic]; !ok {
		t.Fatal("a hiccup removed the switch")
	}
	api.failing, api.denied = nil, map[string]bool{"policies": true}
	b.refresh(context.Background(), c)
	if _, ok := c.configs()[switchTopic]; ok {
		t.Fatal("a refused capability kept the switch")
	}
	// the removal survives a restart: the new process still knows the topic was published
	api.denied = nil
	b.refresh(context.Background(), c)
	b2 := testBridge(t, api, "stats:read")
	b2.StateDir = b.StateDir
	b2.st = persisted{}
	b2.loadState()
	if b2.st.BoxID != b.st.BoxID || !containsSuffix(b2.st.Published, "/policy_50/config") {
		t.Fatalf("state: %+v", b2.st)
	}
	b2.sentCfg, b2.sentVal = map[string]string{}, map[string]string{}
	b2.refresh(context.Background(), c)
	if _, ok := c.configs()[switchTopic]; ok {
		t.Fatal("the restarted bridge left a stale switch")
	}
}

func containsSuffix(list []string, suffix string) bool {
	for _, s := range list {
		if strings.HasSuffix(s, suffix) {
			return true
		}
	}
	return false
}

func TestSwitchCommands(t *testing.T) {
	api := sampleAPI()
	b := testBridge(t, api, "policies:toggle")
	c := newFakeConn()
	ctx := context.Background()
	b.refresh(ctx, c)
	if err := b.handle(ctx, c, Message{Topic: "goblin_gate/policy/50/set", Payload: []byte("OFF")}); err != nil {
		t.Fatal(err)
	}
	if c.get("goblin_gate/policy/50/state") != "OFF" || len(api.switched) != 1 {
		t.Fatalf("after OFF: %q %v", c.get("goblin_gate/policy/50/state"), api.switched)
	}
	// junk and other topics are ignored
	for _, m := range []Message{{"goblin_gate/policy/50/set", []byte("maybe")}, {"goblin_gate/policy/x/set", []byte("ON")}, {"other/policy/50/set", []byte("ON")}} {
		b.handle(ctx, c, m)
	}
	if len(api.switched) != 1 {
		t.Fatalf("junk switched: %v", api.switched)
	}
	// refused (a commit-confirm waits): the state is sent again as it is,
	// so Home Assistant's switch flips back
	api.denied = map[string]bool{"switch": true}
	n := len(c.log)
	b.handle(ctx, c, Message{Topic: "goblin_gate/policy/50/set", Payload: []byte("ON")})
	if c.get("goblin_gate/policy/50/state") != "OFF" || !contains(c.log[n:], "goblin_gate/policy/50/state") {
		t.Fatalf("refused: %q %v", c.get("goblin_gate/policy/50/state"), c.log[n:])
	}
	// Home Assistant restarts: everything is sent again
	n = len(c.log)
	b.handle(ctx, c, Message{Topic: "homeassistant/status", Payload: []byte("online")})
	if !contains(c.log[n:], "homeassistant/switch/"+b.node()+"/policy_50/config") {
		t.Fatalf("birth: %v", c.log[n:])
	}
	// without the capability, commands are ignored
	b.Capabilities = nil
	api.denied = nil
	b.handle(ctx, c, Message{Topic: "goblin_gate/policy/50/set", Payload: []byte("ON")})
	if len(api.switched) != 1 {
		t.Fatal("switched without the capability")
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestSettings(t *testing.T) {
	s, err := SettingsFrom(map[string]any{"broker": "ha.lan", "tls": true})
	if err != nil || s.Broker != "ha.lan:8883" || s.DiscoveryPrefix != "homeassistant" || s.BaseTopic != "goblin_gate" || s.Interval != 30*time.Second {
		t.Fatalf("%+v %v", s, err)
	}
	if s, _ := SettingsFrom(map[string]any{"broker": "[fd00::5]"}); s.Broker != "[fd00::5]:1883" {
		t.Fatalf("v6: %s", s.Broker)
	}
	for name, m := range map[string]map[string]any{
		"no broker":   {},
		"wildcard":    {"broker": "x", "base_topic": "gate/#"},
		"bad mac":     {"broker": "x", "devices": []any{"phone"}},
		"empty topic": {"broker": "x", "discovery_prefix": "/"},
	} {
		if _, err := SettingsFrom(m); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
