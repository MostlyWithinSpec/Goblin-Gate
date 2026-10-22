package main

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/checks"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func TestDemoConfig(t *testing.T) {
	c := demoConfig()
	if errs := config.Validate(c); len(errs) > 0 {
		t.Fatalf("demo config invalid: %v", errs)
	}
	// the demo should show Goblin Checks at work, but not look like a disaster
	r := checks.Run(c, checks.State{}, nil)
	if len(r.Findings) == 0 || r.Score < 50 {
		t.Fatalf("score %d, findings %d", r.Score, len(r.Findings))
	}
}

func TestDemoScrub(t *testing.T) {
	c := demoConfig()
	c.Feeds = []config.Feed{{Name: "f", Type: "ip", URL: "http://169.254.169.254/latest", Enabled: true}}
	c.Notifications.Channels = []config.NotifyChannel{{Name: "hook", Type: "webhook", URL: "http://10.0.0.1/", Enabled: true}}
	c.LogExport = []config.LogExport{{Name: "s", Type: "syslog", Enabled: true}}
	c.CrowdSec = config.CrowdSec{Enabled: true, URL: "http://10.0.0.2:8080", APIKey: "k"}
	c.UPS = config.UPS{Enabled: true, Mode: "remote", Host: "10.0.0.3"}
	c.GitBackup = config.GitBackup{Enabled: true, Remote: "git@example.com:x/y.git"}
	s := demoScrub(c)
	if len(s.Feeds) != 0 || len(s.Notifications.Channels) != 0 || len(s.LogExport) != 0 || s.CrowdSec.Enabled || s.UPS.Enabled || s.GitBackup.Enabled {
		t.Fatalf("outbound settings survived: %+v", s)
	}
	if len(c.Feeds) != 1 || len(s.Policies) != len(c.Policies) {
		t.Fatal("scrub changed its input or dropped the network")
	}
}

func TestDemoGuard(t *testing.T) {
	h := demoGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(299) }))
	for _, req := range []struct {
		method, path string
		want         int
	}{
		{"POST", "/api/v1/admins", 403},
		{"POST", "/api/v1/auth/password", 403},
		{"POST", "/api/v1/auth/2fa/setup", 403},
		{"POST", "/api/v1/import/pfsense", 403},
		{"POST", "/api/v1/diag/ping", 403},
		{"POST", "/api/v1/devices/aa:bb:cc:dd:ee:ff/wake", 403},
		{"POST", "/api/v1/feeds/x/refresh", 403},
		{"POST", "/api/v1/notifications/test", 403},
		{"GET", "/api/v1/admins", 299},
		{"PATCH", "/api/v1/config/candidate", 299},
		{"POST", "/api/v1/commit", 299},
		{"POST", "/api/v1/policy/simulate", 299},
		{"PATCH", "/api/v1/devices/aa:bb:cc:dd:ee:ff", 299}, // naming a device is fine
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(req.method, req.path, nil))
		if rec.Code != req.want {
			t.Errorf("%s %s: %d, want %d", req.method, req.path, rec.Code, req.want)
		}
	}
}

func TestDemoEvents(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	now := time.Now()
	kinds := map[string]int{}
	for range 30 {
		for _, e := range demoEvents(now, r) {
			kinds[e.Kind]++
			if e.Time > now.Unix() || e.Time < now.Add(-time.Minute).Unix() {
				t.Fatalf("event outside its minute: %+v", e)
			}
		}
	}
	for _, k := range []string{"traffic", "ips", "webfilter"} {
		if kinds[k] == 0 {
			t.Errorf("no %s events in 30 minutes: %v", k, kinds)
		}
	}
}

func TestServeDemo(t *testing.T) {
	orig := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = orig })
	// the demo blocks the default transport: this client dials directly
	client := &http.Client{Transport: &http.Transport{DialContext: (&net.Dialer{}).DialContext}}

	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- serve(context.Background(), options{listen: "127.0.0.1:0", configDir: t.TempDir(), demo: true, demoReset: 4 * time.Second}, ready)
	}()
	var addr string
	select {
	case addr = <-ready:
	case err := <-done:
		t.Fatalf("serve exited early: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("server did not start")
	}
	get := func(path string, v any) int {
		t.Helper()
		resp, err := client.Get("http://" + addr + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		json.NewDecoder(resp.Body).Decode(v)
		return resp.StatusCode
	}
	var status map[string]any
	if get("/api/v1/system/status", &status); status["demo"] != true {
		t.Fatalf("status %v", status)
	}
	var setup map[string]any
	if get("/api/v1/setup/state", &setup); setup["needed"] != false {
		t.Fatalf("the demo asks for setup: %v", setup)
	}
	var devs struct {
		Devices []any `json:"devices"`
	}
	if code := get("/api/v1/devices", &devs); code != 200 || len(devs.Devices) < 10 {
		t.Fatalf("devices %d %d", code, len(devs.Devices))
	}
	var logsResp struct {
		Events []any `json:"events"`
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(logsResp.Events) == 0 && time.Now().Before(deadline) {
		get("/api/v1/logs?kind=traffic", &logsResp)
		time.Sleep(100 * time.Millisecond)
	}
	if len(logsResp.Events) == 0 {
		t.Fatal("no synthetic history")
	}
	resp, err := client.Post("http://"+addr+"/api/v1/admins", "application/json", bytes.NewReader([]byte(`{"username":"x"}`)))
	if err != nil || resp.StatusCode != 403 {
		t.Fatalf("admins in the demo: %v %v", resp, err)
	}
	// nothing reaches out: the default transport refuses
	if _, err := http.Get("http://192.0.2.1/"); err == nil || !strings.Contains(err.Error(), "no outside connections") {
		t.Fatalf("outbound request: %v", err)
	}
	// the demo ends by itself and leaves nothing behind
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("demo exit: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the demo did not reset")
	}
	if runtime.GOOS != "linux" {
		return // Windows can't remove files still open (the log database); the demo runs on Linux
	}
	left, _ := filepath.Glob(filepath.Join(os.TempDir(), "goblin-demo-*"))
	for _, d := range left {
		if st, err := os.Stat(d); err == nil && time.Since(st.ModTime()) < time.Minute {
			t.Errorf("demo directory left behind: %s", d)
		}
	}
}
