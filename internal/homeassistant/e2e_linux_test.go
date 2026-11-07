//go:build linux

package homeassistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMosquittoE2E runs the bridge against Debian's mosquitto with a
// password, plus a client playing Home Assistant: the discovery configs and
// states arrive retained, a switch command reaches goblind's policy switch
// and the new state comes back, Home Assistant's birth message makes the
// bridge send everything again, the last will marks the box offline when
// the connection dies, and a stop says offline too. Needs mosquitto and
// mosquitto_passwd: scripts/linux-ha-test.ps1 runs it with GOBLIN_MQTT_E2E=1.
func TestMosquittoE2E(t *testing.T) {
	if os.Getenv("GOBLIN_MQTT_E2E") != "1" {
		t.Skip("set GOBLIN_MQTT_E2E=1 (mosquitto)")
	}
	dir := t.TempDir()
	pw := filepath.Join(dir, "passwd")
	if out, err := exec.Command("mosquitto_passwd", "-b", "-c", pw, "gate", "s3cret pass").CombinedOutput(); err != nil {
		t.Fatalf("mosquitto_passwd: %v %s", err, out)
	}
	os.Chmod(pw, 0o600)
	conf := filepath.Join(dir, "mosquitto.conf")
	os.WriteFile(conf, []byte(fmt.Sprintf("user root\nlistener 18830 127.0.0.1\nallow_anonymous false\npassword_file %s\npersistence false\n", pw)), 0o644)
	broker := exec.Command("mosquitto", "-c", conf)
	var blog strings.Builder
	broker.Stdout, broker.Stderr = &blog, &blog
	if err := broker.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { broker.Process.Kill(); broker.Wait() })
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("mosquitto:\n%s", blog.String())
		}
	})
	for i := 0; ; i++ {
		if c, err := net.Dial("tcp", "127.0.0.1:18830"); err == nil {
			c.Close()
			break
		}
		if i == 50 {
			t.Fatal("mosquitto did not start")
		}
		time.Sleep(100 * time.Millisecond)
	}

	// goblind's plugin API, as the plugin sees it
	var mu sync.Mutex
	policy := Policy{ID: 50, Name: "guest-internet", Enabled: true}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/wan/status":
			json.NewEncoder(w).Encode(map[string]any{"wans": []WAN{{Name: "wan1", Device: "eth0", Up: true, Addresses: []string{"24.101.68.119/24"}}}})
		case "GET /api/v1/gateways/status":
			json.NewEncoder(w).Encode(map[string]any{"gateways": []Gateway{}})
		case "GET /api/v1/stats/system":
			json.NewEncoder(w).Encode(System{CPUPercent: 3, MemTotalBytes: 100, MemUsedBytes: 40, UptimeSeconds: 600, Sessions: 12})
		case "GET /api/v1/devices":
			json.NewEncoder(w).Encode(map[string]any{"devices": []Device{{MAC: "aa:bb:cc:dd:ee:01", Name: "phone", LastSeen: time.Now()}}})
		case "GET /api/v1/plugin/policies":
			json.NewEncoder(w).Encode(map[string]any{"policies": []Policy{policy}})
		case "PUT /api/v1/plugin/policies/50":
			var req struct{ Enabled bool }
			json.NewDecoder(r.Body).Decode(&req)
			policy.Enabled = req.Enabled
			json.NewEncoder(w).Encode(policy)
		default:
			w.WriteHeader(404)
		}
	}))
	defer api.Close()

	settings, err := SettingsFrom(map[string]any{"broker": "127.0.0.1:18830", "username": "gate", "password": "s3cret pass", "devices": []any{"aa:bb:cc:dd:ee:01"}, "interval_seconds": 10.0})
	if err != nil {
		t.Fatal(err)
	}
	var dialed []*MQTT
	var dmu sync.Mutex
	b := &Bridge{API: &Client{HTTP: api.Client(), Base: api.URL}, Settings: settings, Version: "test",
		Capabilities: []string{"stats:read", "devices:read", "policies:toggle"}, StateDir: dir,
		Dial: func(ctx context.Context, o MQTTOptions) (Conn, error) {
			c, err := DialMQTT(ctx, o)
			if err == nil {
				dmu.Lock()
				dialed = append(dialed, c)
				dmu.Unlock()
			}
			return c, err
		}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- b.Run(ctx) }()

	// Home Assistant's side; a wrong password is refused
	if _, err := DialMQTT(context.Background(), MQTTOptions{Addr: "127.0.0.1:18830", ClientID: "intruder", Username: "gate", Password: "nope"}); err == nil {
		t.Fatal("a wrong password was accepted")
	}
	ha, err := DialMQTT(context.Background(), MQTTOptions{Addr: "127.0.0.1:18830", ClientID: "ha", Username: "gate", Password: "s3cret pass"})
	if err != nil {
		t.Fatal(err)
	}
	defer ha.Close()
	ha.Subscribe("homeassistant/#", "goblin_gate/#")
	seen := map[string]string{}
	waitFor := func(topic, want string) {
		t.Helper()
		deadline := time.After(20 * time.Second)
		for seen[topic] != want {
			select {
			case m := <-ha.Messages():
				seen[m.Topic] = string(m.Payload)
			case <-deadline:
				t.Fatalf("%s = %q, want %q", topic, seen[topic], want)
			}
		}
	}
	waitFor("goblin_gate/status", "online")
	waitFor("goblin_gate/policy/50/state", "ON")
	waitFor("goblin_gate/wan/wan1/state", "ON")
	waitFor("goblin_gate/presence/aabbccddee01/state", "home")
	node := b.node()
	var sw map[string]any
	json.Unmarshal([]byte(seen["homeassistant/switch/"+node+"/policy_50/config"]), &sw)
	if sw["command_topic"] != "goblin_gate/policy/50/set" {
		t.Fatalf("switch discovery: %v", seen)
	}

	// Home Assistant flips the switch
	ha.Publish("goblin_gate/policy/50/set", []byte("OFF"), false)
	waitFor("goblin_gate/policy/50/state", "OFF")
	mu.Lock()
	if policy.Enabled {
		t.Fatal("the policy was not switched")
	}
	mu.Unlock()

	// Home Assistant restarts and says so: the configs come again
	delete(seen, "homeassistant/switch/"+node+"/policy_50/config")
	ha.Publish("homeassistant/status", []byte("online"), false)
	deadline := time.After(10 * time.Second)
	for seen["homeassistant/switch/"+node+"/policy_50/config"] == "" {
		select {
		case m := <-ha.Messages():
			seen[m.Topic] = string(m.Payload)
		case <-deadline:
			t.Fatal("no discovery after Home Assistant's birth message")
		}
	}

	// the bridge's connection dies (no DISCONNECT): the broker sends the
	// last will, then the bridge reconnects and says online again
	dmu.Lock()
	first := dialed[0]
	dmu.Unlock()
	first.conn.Close()
	waitFor("goblin_gate/status", "offline")
	waitFor("goblin_gate/status", "online")

	// a clean stop says offline
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	waitFor("goblin_gate/status", "offline")
}

// TestServeForHomeAssistant runs the bridge against a fake goblind until
// killed, for scripts/linux-ha-test.ps1 -HomeAssistant: a real Home
// Assistant shows the entities and flips the switch. GOBLIN_HA_SERVE is
// the broker's address; each switch is printed as "switched <name> <on>".
func TestServeForHomeAssistant(t *testing.T) {
	broker := os.Getenv("GOBLIN_HA_SERVE")
	if broker == "" {
		t.Skip("started by scripts/linux-ha-test.ps1 -HomeAssistant")
	}
	var mu sync.Mutex
	policy := Policy{ID: 50, Name: "guest-internet", Enabled: true}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/wan/status":
			json.NewEncoder(w).Encode(map[string]any{"wans": []WAN{{Name: "wan1", Device: "eth0", Up: true, Addresses: []string{"24.101.68.119/24"}, Gateway: "24.101.68.1"}}})
		case "GET /api/v1/gateways/status":
			json.NewEncoder(w).Encode(map[string]any{"gateways": []Gateway{{Name: "fiber", State: "up", LatencyMs: 4}, {Name: "lte", State: "unknown"}}})
		case "GET /api/v1/stats/system":
			json.NewEncoder(w).Encode(System{CPUPercent: 3.25, MemTotalBytes: 4 << 30, MemUsedBytes: 1 << 30, UptimeSeconds: 7200, Sessions: 321})
		case "GET /api/v1/devices":
			json.NewEncoder(w).Encode(map[string]any{"devices": []Device{{MAC: "aa:bb:cc:dd:ee:01", Name: "Sam's phone", IPs: []string{"192.168.1.40"}, LastSeen: time.Now()}}})
		case "GET /api/v1/plugin/policies":
			json.NewEncoder(w).Encode(map[string]any{"policies": []Policy{policy}})
		case "PUT /api/v1/plugin/policies/50":
			var req struct{ Enabled bool }
			json.NewDecoder(r.Body).Decode(&req)
			policy.Enabled = req.Enabled
			fmt.Printf("switched %s %v\n", policy.Name, policy.Enabled)
			json.NewEncoder(w).Encode(policy)
		default:
			w.WriteHeader(404)
		}
	}))
	defer api.Close()
	settings, err := SettingsFrom(map[string]any{"broker": broker, "devices": []any{"aa:bb:cc:dd:ee:01", "aa:bb:cc:dd:ee:02"}, "interval_seconds": 10.0})
	if err != nil {
		t.Fatal(err)
	}
	b := &Bridge{API: &Client{HTTP: api.Client(), Base: api.URL}, Settings: settings, Version: "e2e",
		Capabilities: []string{"stats:read", "devices:read", "policies:toggle"}, StateDir: t.TempDir()}
	b.Run(context.Background())
}
