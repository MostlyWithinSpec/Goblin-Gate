package pluginsdk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/plugins"
)

// fakeGoblind serves exactly the routes plugins may call (internal/plugins
// Routes, the table goblind enforces), so every SDK call must hit one of them.
type fakeGoblind struct {
	mu    sync.Mutex
	calls []string          // "METHOD path?query body"
	reply map[string]string // route pattern -> JSON answer
}

func newFake(t *testing.T) (*fakeGoblind, *Client) {
	t.Helper()
	f := &fakeGoblind{reply: map[string]string{}}
	mux := http.NewServeMux()
	for pattern := range plugins.Routes {
		pattern := pattern
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			if len(body) > 0 && r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("%s: body without Content-Type application/json", pattern)
			}
			f.mu.Lock()
			f.calls = append(f.calls, strings.TrimSpace(r.Method+" "+r.URL.RequestURI()+" "+string(body)))
			ans, ok := f.reply[pattern]
			f.mu.Unlock()
			if !ok {
				ans = "{}"
			}
			w.Header().Set("Content-Type", "application/json")
			if strings.HasPrefix(ans, "!") { // "!<status> <json>": an error answer
				code, rest, _ := strings.Cut(ans[1:], " ")
				switch code {
				case "403":
					w.WriteHeader(http.StatusForbidden)
				case "409":
					w.WriteHeader(http.StatusConflict)
				}
				io.WriteString(w, rest)
				return
			}
			io.WriteString(w, ans)
		})
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("%s %s is not a plugin route", r.Method, r.URL.Path)
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return f, &Client{HTTP: srv.Client(), Base: srv.URL}
}

func (f *fakeGoblind) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return ""
	}
	return f.calls[len(f.calls)-1]
}

func TestCalls(t *testing.T) {
	f, c := newFake(t)
	ctx := context.Background()
	f.reply["GET /api/v1/plugin/self"] = `{"name":"hello","capabilities":["stats:read"],"settings":{"greeting":"hi","n":5,"on":true,"list":["a","b"]}}`
	f.reply["GET /api/v1/logs"] = `{"events":[{"id":7,"time":100,"kind":"ips","action":"alert","src_ip":"203.0.113.5","signature":"2001: ET SCAN"}],"next_cursor":3}`
	f.reply["GET /api/v1/stats/system"] = `{"cpu_percent":12.5,"mem_total_bytes":100,"mem_used_bytes":40,"uptime_seconds":3600,"sessions":42}`
	f.reply["GET /api/v1/wan/status"] = `{"wans":[{"name":"wan","device":"eth0","up":true,"addresses":["198.51.100.2/24"],"gateway":"198.51.100.1"}]}`
	f.reply["GET /api/v1/gateways/status"] = `{"enabled":true,"gateways":[{"name":"isp1","state":"up","loss_percent":0,"latency_ms":9}],"targets":[],"default":{"name":""}}`
	f.reply["GET /api/v1/blocks"] = `{"blocks":[{"ip":"203.0.113.9","reason":"scan","source":"plugin:hello","expires_in":60}]}`
	f.reply["POST /api/v1/blocks"] = `{"ip":"203.0.113.9","reason":"scan","expires_in":600}`
	f.reply["GET /api/v1/devices"] = `{"devices":[{"mac":"aa:bb:cc:dd:ee:ff","ips":["192.168.1.20"],"name":"Sam's phone"}]}`
	f.reply["GET /api/v1/plugin/policies"] = `{"policies":[{"id":50,"name":"Guest internet","enabled":true}]}`
	f.reply["PUT /api/v1/plugin/policies/{id}"] = `{"id":50,"name":"Guest internet","enabled":false,"changed":true,"revision":12}`
	f.reply["GET /api/v1/plugin/objects"] = `{"objects":[{"name":"containers","plugin":"homelab","members":["10.20.0.5/32"],"updated":"2026-11-07T12:00:00Z"}]}`
	f.reply["PUT /api/v1/plugin/objects/{name}"] = `{"name":"containers","members":2,"added":1,"removed":0,"changed":true}`

	self, err := c.Self(ctx)
	if err != nil || self.Name != "hello" || !self.Has(CapStatsRead) || self.Has(CapBlocksWrite) {
		t.Fatalf("Self = %+v, %v", self, err)
	}
	if self.String("greeting", "x") != "hi" || self.String("nope", "x") != "x" || self.Int("n", 0) != 5 ||
		self.Float("n", 0) != 5 || !self.Bool("on", false) || strings.Join(self.Strings("list"), ",") != "a,b" || self.Strings("nope") != nil {
		t.Errorf("settings helpers on %+v", self.Settings)
	}

	page, err := c.Logs(ctx, LogQuery{Kind: "ips", After: 5, Limit: 100})
	if err != nil || len(page.Events) != 1 || page.Events[0].Signature != "2001: ET SCAN" || page.NextCursor != 3 {
		t.Fatalf("Logs = %+v, %v", page, err)
	}
	if got := f.last(); got != "GET /api/v1/logs?after=5&kind=ips&limit=100" {
		t.Errorf("logs request %q", got)
	}
	if _, err := c.Logs(ctx, LogQuery{}); err != nil || f.last() != "GET /api/v1/logs" {
		t.Errorf("empty query: %q %v", f.last(), err)
	}

	if s, err := c.System(ctx); err != nil || s.Sessions != 42 || s.CPUPercent != 12.5 {
		t.Errorf("System = %+v, %v", s, err)
	}
	if w, err := c.WANs(ctx); err != nil || len(w) != 1 || !w[0].Up || w[0].Gateway != "198.51.100.1" {
		t.Errorf("WANs = %+v, %v", w, err)
	}
	if g, err := c.Gateways(ctx); err != nil || !g.Enabled || len(g.Gateways) != 1 || g.Gateways[0].LatencyMs != 9 {
		t.Errorf("Gateways = %+v, %v", g, err)
	}
	var stats map[string]any
	if err := c.PolicyStats(ctx, &stats); err != nil || f.last() != "GET /api/v1/stats/policies" {
		t.Errorf("PolicyStats: %q %v", f.last(), err)
	}
	if err := c.FeedStats(ctx, &stats); err != nil || f.last() != "GET /api/v1/stats/feeds" {
		t.Errorf("FeedStats: %q %v", f.last(), err)
	}
	if err := c.StatsQuery(ctx, map[string]string{"group_by": "src_ip"}, &stats); err != nil ||
		f.last() != `POST /api/v1/stats/query {"group_by":"src_ip"}` {
		t.Errorf("StatsQuery: %q %v", f.last(), err)
	}
	var cfg struct {
		Hostname string `json:"hostname"`
	}
	if err := c.RunningConfig(ctx, &cfg); err != nil || f.last() != "GET /api/v1/config/running" {
		t.Errorf("RunningConfig: %q %v", f.last(), err)
	}

	if err := c.Notify(ctx, Notification{Title: "t", Message: "m", Key: "k"}); err != nil ||
		f.last() != `POST /api/v1/plugin/notify {"title":"t","message":"m","key":"k"}` {
		t.Errorf("Notify: %q %v", f.last(), err)
	}

	if b, err := c.Blocks(ctx); err != nil || len(b) != 1 || b[0].IP.String() != "203.0.113.9" || b[0].ExpiresIn != 60 {
		t.Errorf("Blocks = %+v, %v", b, err)
	}
	if b, err := c.AddBlock(ctx, "203.0.113.9", 10, "scan"); err != nil || b.ExpiresIn != 600 ||
		f.last() != `POST /api/v1/blocks {"ip":"203.0.113.9","minutes":10,"reason":"scan"}` {
		t.Errorf("AddBlock = %+v, %q %v", b, f.last(), err)
	}
	if err := c.RemoveBlock(ctx, "2001:db8::1"); err != nil || f.last() != "DELETE /api/v1/blocks/2001:db8::1" {
		t.Errorf("RemoveBlock: %q %v", f.last(), err)
	}

	if d, err := c.Devices(ctx); err != nil || len(d) != 1 || d[0].Name != "Sam's phone" {
		t.Errorf("Devices = %+v, %v", d, err)
	}
	if p, err := c.Policies(ctx); err != nil || len(p) != 1 || p[0].ID != 50 {
		t.Errorf("Policies = %+v, %v", p, err)
	}
	if p, err := c.SwitchPolicy(ctx, 50, false); err != nil || p.Enabled || f.last() != `PUT /api/v1/plugin/policies/50 {"enabled":false}` {
		t.Errorf("SwitchPolicy = %+v, %q %v", p, f.last(), err)
	}
	if o, err := c.LiveObjects(ctx); err != nil || len(o) != 1 || o[0].Members[0] != netip.MustParsePrefix("10.20.0.5/32") || o[0].Updated.IsZero() {
		t.Errorf("LiveObjects = %+v, %v", o, err)
	}
	if ch, err := c.SetLiveObject(ctx, "containers", nil); err != nil || !ch.Changed || f.last() != `PUT /api/v1/plugin/objects/containers {"members":[]}` {
		t.Errorf("SetLiveObject = %+v, %q %v", ch, f.last(), err)
	}
}

func TestAPIError(t *testing.T) {
	f, c := newFake(t)
	f.reply["GET /api/v1/devices"] = `!403 {"error":{"code":"scope_denied","message":"plugin hello may not call GET /api/v1/devices"}}`
	f.reply["PUT /api/v1/plugin/policies/{id}"] = `!409 {"error":{"code":"confirm_pending","message":"a commit is waiting"}}`
	_, err := c.Devices(context.Background())
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != "scope_denied" || !Denied(err) || StatusOf(err) != 403 {
		t.Fatalf("err = %#v", err)
	}
	if !strings.Contains(err.Error(), "scope_denied") {
		t.Errorf("Error() = %q", err.Error())
	}
	_, err = c.SwitchPolicy(context.Background(), 1, true)
	if Denied(err) || StatusOf(err) != 409 || !errors.As(err, &ae) || ae.Code != "confirm_pending" {
		t.Errorf("409: %v", err)
	}
	if Denied(errors.New("network down")) || StatusOf(errors.New("x")) != 0 {
		t.Error("a plain error is no API error")
	}
}

func TestWaitSelf(t *testing.T) {
	_, c := newFake(t)
	ctx := context.Background()
	// goblind not up: the dial fails every time, WaitSelf gives up after tries
	down := &Client{HTTP: &http.Client{Timeout: time.Second}, Base: "http://127.0.0.1:1"}
	start := time.Now()
	if _, err := down.WaitSelf(ctx, 3, 10*time.Millisecond); err == nil || StatusOf(err) != 0 {
		t.Fatalf("down: %v", err)
	}
	if time.Since(start) < 20*time.Millisecond {
		t.Error("WaitSelf did not wait between tries")
	}
	// goblind up: one call
	if s, err := c.WaitSelf(ctx, 3, time.Hour); err != nil || s.Capabilities != nil && len(s.Capabilities) != 0 {
		t.Fatalf("up: %+v %v", s, err)
	}
	// a cancelled context stops the wait
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := down.WaitSelf(cctx, 100, time.Hour); err == nil {
		t.Fatal("cancelled: no error")
	}
}

func TestNotifyCutsText(t *testing.T) {
	f, c := newFake(t)
	long := strings.Repeat("é", 150) // 300 octets
	if err := c.Notify(context.Background(), Notification{Title: long, Message: "m"}); err != nil {
		t.Fatal(err)
	}
	_, body, _ := strings.Cut(strings.TrimPrefix(f.last(), "POST /api/v1/plugin/notify"), " ")
	var n Notification
	if err := json.Unmarshal([]byte(body), &n); err != nil {
		t.Fatal(err)
	}
	if len(n.Title) != 200 || !strings.HasPrefix(long, n.Title) {
		t.Errorf("title cut to %d octets (want 200, whole characters)", len(n.Title))
	}
}

func TestRunOverTheSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "api.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("no unix sockets here: %v", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/plugin/self" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, `{"name":"hello","capabilities":["notify:send"],"settings":{"greeting":"hi"}}`)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	var got *Plugin
	err = run(context.Background(), Env{Socket: sock, StateDir: "/state"}, slog.New(slog.NewTextHandler(io.Discard, nil)),
		func(ctx context.Context, p *Plugin) error {
			got = p
			return errors.New("done")
		})
	if err == nil || err.Error() != "done" {
		t.Fatalf("run returned %v, want the function's error", err)
	}
	if got == nil || got.Env.Name != "hello" || got.Env.StateDir != "/state" || !got.Self.Has(CapNotifySend) ||
		got.Self.String("greeting", "") != "hi" || got.Client == nil || got.Logger == nil {
		t.Fatalf("plugin = %+v", got)
	}
}
