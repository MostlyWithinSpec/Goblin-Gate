// Command hello is the example Goblin Gate plugin (docs/dev/plugins.md).
// Every interval it asks goblind who it is, reads the latest events and tries to read the config,
// logging what goblind allowed. With notify:send it sends one greeting on
// start. Standard library only.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"syscall"
	"time"
)

// client talks to goblind on the plugin socket. No credentials: goblind
// knows the plugin by its systemd unit.
type client struct {
	http *http.Client
}

func newClient(socket string) *client {
	return &client{http: &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}},
	}}
}

// call sends a request to goblind; out (if not nil) receives the JSON reply.
func (c *client) call(method, path string, body, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "http://goblind"+path, rd)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode < 300 {
		return resp.StatusCode, json.NewDecoder(resp.Body).Decode(out)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

type self struct {
	Name         string         `json:"name"`
	Capabilities []string       `json:"capabilities"`
	Settings     map[string]any `json:"settings"`
}

func main() {
	log.SetFlags(0) // the journal adds timestamps
	socket := os.Getenv("GOBLIN_PLUGIN_SOCKET")
	if socket == "" {
		socket = "/run/goblin-plugins/api.sock"
	}
	c := newClient(socket)

	var me self
	if code, err := c.call("GET", "/api/v1/plugin/self", nil, &me); err != nil || code != 200 {
		log.Fatalf("hello: plugin/self: status %d, %v", code, err)
	}
	greeting, _ := me.Settings["greeting"].(string)
	interval := 60 * time.Second
	if n, ok := me.Settings["interval_seconds"].(float64); ok && n >= 10 {
		interval = time.Duration(n) * time.Second
	}
	log.Printf("hello: %s, I am %s with capabilities %v", greeting, me.Name, me.Capabilities)

	if slices.Contains(me.Capabilities, "notify:send") {
		code, err := c.call("POST", "/api/v1/plugin/notify", map[string]string{"title": greeting, "message": "the hello plugin started", "key": "start"}, nil)
		log.Printf("hello: notify: status %d %v", code, errText(err))
	}
	if dir := os.Getenv("STATE_DIRECTORY"); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, "started"), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// An API for the admin UI (goblind proxies /api/v1/plugins/hello/api/
	// to it): JSON only.
	if sock := os.Getenv("GOBLIN_PLUGIN_API_SOCKET"); sock != "" {
		_ = os.Remove(sock)
		ln, err := net.Listen("unix", sock)
		if err != nil {
			log.Fatalf("hello: api socket: %v", err)
		}
		mux := http.NewServeMux()
		mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"greeting": greeting, "asked_by": r.Header.Get("X-Goblin-User")})
		})
		go http.Serve(ln, mux)
	}
	for {
		code, err := c.call("GET", "/api/v1/logs?limit=1", nil, nil)
		log.Printf("hello: read events: status %d %v", code, errText(err))
		code, err = c.call("GET", "/api/v1/config/running", nil, nil)
		log.Printf("hello: read config: status %d %v", code, errText(err))
		select {
		case <-ctx.Done():
			log.Printf("hello: bye")
			return
		case <-time.After(interval):
		}
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("(%v)", err)
}
