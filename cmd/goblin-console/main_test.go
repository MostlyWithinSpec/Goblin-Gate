package main

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testConsole(t *testing.T, h http.HandlerFunc, input string) (*console, *bytes.Buffer) {
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	addr := srv.Listener.Addr().String()
	var out bytes.Buffer
	return &console{
		in:  bufio.NewReader(strings.NewReader(input)),
		out: &out,
		http: &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", addr)
		}}},
		run: func(string, ...string) error { return nil },
	}, &out
}

func TestMenuShowsURLAndSetupHint(t *testing.T) {
	c, out := testConsole(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/config/running":
			w.Write([]byte(`{"system":{"hostname":"goblin-gate","admin_port":443},"interfaces":[
				{"name":"wan1","device":"eth0","role":"wan","mode":"dhcp","enabled":true},
				{"name":"lan","device":"eth1","role":"lan","mode":"static","address":"192.168.1.1/24","enabled":true}]}`))
		case "/api/v1/setup/state":
			w.Write([]byte(`{"needed":true}`))
		}
	}, "0\n")
	if err := c.loop(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Hostname: goblin-gate", "web UI: https://192.168.1.1/", "First start", "1) Assign interfaces"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestAPIError(t *testing.T) {
	c, _ := testConsole(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(422)
		w.Write([]byte(`{"error":{"message":"the config has errors","details":[{"path":"interfaces[0].device","message":"bad"}]}}`))
	}, "")
	err := c.api("POST", "/api/v1/commit", map[string]any{}, nil)
	if err == nil || !strings.Contains(err.Error(), "the config has errors") || !strings.Contains(err.Error(), "interfaces[0].device") {
		t.Fatalf("%v", err)
	}
}
