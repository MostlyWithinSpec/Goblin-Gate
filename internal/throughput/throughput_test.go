package throughput

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"
)

const tcpUpload = `{"start":{"connected":[{"remote_host":"192.168.1.50","remote_port":50112}],
 "test_start":{"protocol":"TCP","num_streams":1,"reverse":0,"duration":10}},
 "end":{"sum_sent":{"seconds":10.0,"bits_per_second":941000000,"retransmits":0},
 "sum_received":{"seconds":10.01,"bits_per_second":939500000}}}`

const tcpDownload = `{"start":{"connected":[{"remote_host":"192.168.1.50"}],
 "test_start":{"protocol":"TCP","num_streams":4,"reverse":1}},
 "end":{"sum_sent":{"seconds":10,"bits_per_second":905000000,"retransmits":37},
 "sum_received":{"seconds":10,"bits_per_second":902000000}}}`

const udp = `{"start":{"connected":[{"remote_host":"192.168.1.51"}],"test_start":{"protocol":"UDP","num_streams":1,"reverse":0}},
 "end":{"sum":{"seconds":10,"bits_per_second":99800000,"jitter_ms":0.042,"lost_percent":0.3}}}`

func TestParseResult(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	for name, tc := range map[string]struct {
		in   string
		want Result
	}{
		"tcp upload":   {tcpUpload, Result{Time: now, Client: "192.168.1.50", Protocol: "tcp", Direction: "upload", Streams: 1, Seconds: 10.01, BitsPerSecond: 939500000}},
		"tcp download": {tcpDownload, Result{Time: now, Client: "192.168.1.50", Protocol: "tcp", Direction: "download", Streams: 4, Seconds: 10, BitsPerSecond: 905000000, Retransmits: 37}},
		"udp":          {udp, Result{Time: now, Client: "192.168.1.51", Protocol: "udp", Direction: "upload", Streams: 1, Seconds: 10, BitsPerSecond: 99800000, JitterMs: 0.042, LostPercent: 0.3}},
	} {
		got, err := ParseResult([]byte(tc.in), now)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %+v %v", name, got, err)
		}
	}
	if _, err := ParseResult([]byte(`{"start":{},"end":{},"error":"the client has unexpectedly closed the connection"}`), now); err == nil {
		t.Error("an iperf3 error should fail")
	}
	if _, err := ParseResult([]byte(`not json`), now); err == nil {
		t.Error("garbage should fail")
	}
}

// fakeServer hands out one report per test a "client" runs.
type fakeServer struct {
	tests chan string
}

func (f *fakeServer) serve(ctx context.Context, addr string, port int) ([]byte, error) {
	select {
	case r := <-f.tests:
		return []byte(r), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestManager(t *testing.T) {
	f := &fakeServer{tests: make(chan string)}
	var mu sync.Mutex
	var opened [][]string
	m := &Manager{Serve: f.serve, SetOpen: func(d []string) error {
		mu.Lock()
		defer mu.Unlock()
		opened = append(opened, d)
		return nil
	}}
	st, err := m.Start("lan", "eth1", "192.168.1.1", time.Minute)
	if err != nil || !st.Running || st.Address != "192.168.1.1" || st.Port != Port || st.Expires.Sub(st.Started) != time.Minute {
		t.Fatalf("start: %+v %v", st, err)
	}
	if _, err := m.Start("lan", "eth1", "192.168.1.1", 0); err != ErrRunning {
		t.Fatalf("second start: %v", err)
	}
	f.tests <- tcpUpload
	f.tests <- tcpDownload
	deadline := time.Now().Add(2 * time.Second)
	for len(m.Status().Results) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if r := m.Status().Results; len(r) != 2 || r[0].Direction != "upload" || r[1].Direction != "download" {
		t.Fatalf("results: %+v", r)
	}
	m.Resync()
	m.Stop()
	st = m.Status()
	if st.Running || len(st.Results) != 2 {
		t.Fatalf("after stop: %+v", st)
	}
	mu.Lock()
	defer mu.Unlock()
	want := [][]string{{"eth1"}, {"eth1"}, nil}
	if !reflect.DeepEqual(opened, want) {
		t.Fatalf("firewall: %v, want %v", opened, want)
	}
	m.Resync() // not running: leaves the firewall alone
	if len(opened) != 3 {
		t.Fatalf("resync while stopped opened the port: %v", opened)
	}
}

func TestManagerStopsByItself(t *testing.T) {
	f := &fakeServer{tests: make(chan string)}
	closed := make(chan struct{})
	var once sync.Once
	m := &Manager{Serve: f.serve, SetOpen: func(d []string) error {
		if d == nil {
			once.Do(func() { close(closed) })
		}
		return nil
	}}
	if _, err := m.Start("lan", "eth1", "192.168.1.1", 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("the window did not close by itself")
	}
	for m.Status().Running {
		time.Sleep(time.Millisecond)
	}
	if d := MaxDuration; func() time.Duration {
		st, _ := m.Start("lan", "eth1", "192.168.1.1", time.Hour)
		defer m.Stop()
		return st.Expires.Sub(st.Started)
	}() != d {
		t.Fatal("durations are capped at MaxDuration")
	}
}
