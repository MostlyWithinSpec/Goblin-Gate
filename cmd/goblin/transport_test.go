package main

import (
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
)

func TestClientOverUnixSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "api.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip("unix sockets unavailable:", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.URL.Path+" "+r.Header.Get("Authorization"))
	})}
	go srv.Serve(ln)
	defer srv.Close()

	c := newGoblinClient("unix://"+sock, "gg_x_y")
	resp, err := c.doRequest("GET", "/api/v1/auth/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "/api/v1/auth/me Bearer gg_x_y" {
		t.Fatalf("got %q", body)
	}
}
