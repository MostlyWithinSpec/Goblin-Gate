package upnp

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	data := []byte("UDP:3074:192.168.1.50:3074:1792036000:Xbox:Teredo\n" +
		"TCP:8080:192.168.1.60:80:0:web\n" +
		"garbage\n" +
		"TCP:x:192.168.1.60:80:0:bad port\n")
	ms := Parse(data)
	if len(ms) != 2 {
		t.Fatalf("got %+v", ms)
	}
	if ms[0].Protocol != "tcp" || ms[0].ExternalPort != 8080 || ms[0].InternalPort != 80 || !ms[0].Expires.IsZero() {
		t.Errorf("tcp mapping: %+v", ms[0])
	}
	if ms[1].Description != "Xbox:Teredo" || ms[1].Expires.Unix() != 1792036000 || ms[1].InternalIP != "192.168.1.50" {
		t.Errorf("udp mapping: %+v", ms[1])
	}
	if got, err := Read(filepath.Join(t.TempDir(), "missing")); err != nil || got != nil {
		t.Errorf("missing file: %v %v", got, err)
	}
}

func TestWatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leases")
	os.WriteFile(path, []byte("TCP:1000:10.0.0.2:1000:0:old\n"), 0o644)
	var mu sync.Mutex
	var events []string
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Watch(ctx, path, 10*time.Millisecond, func(m Mapping, added bool) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, map[bool]string{true: "+", false: "-"}[added]+m.Key())
		})
		close(done)
	}()
	time.Sleep(30 * time.Millisecond)
	os.WriteFile(path, []byte("UDP:3074:10.0.0.3:3074:0:new\n"), 0o644)
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(events)
		mu.Unlock()
		if n >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 || events[0] != "+udp/3074" || events[1] != "-tcp/1000" {
		t.Fatalf("events %v", events)
	}
}
