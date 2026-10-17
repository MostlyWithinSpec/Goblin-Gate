package webfilter

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/logs"
)

// query builds a DNS query for name/qtype.
func query(id uint16, name string, qtype uint16) []byte {
	b := []byte{byte(id >> 8), byte(id), 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0}
	for _, l := range strings.Split(name, ".") {
		b = append(b, byte(len(l)))
		b = append(b, l...)
	}
	b = append(b, 0, byte(qtype>>8), byte(qtype), 0, 1)
	return b
}

func testDB(t *testing.T) *DB {
	b := NewBuilder()
	b.Add("gambling", "casino.example")
	b.Add("social_networks", "social.example")
	b.Add("adult", "nsfw.example")
	b.Add("news", "news.example")
	var buf bytes.Buffer
	b.WriteTo(&buf)
	db, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// testFilter filters 127.0.0.0/8 (box address 127.0.0.1) with profile
// "kids" and forwards to a fake upstream that marks its answers.
type env struct {
	f      *Filter
	events []logs.Event
	mu     sync.Mutex
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{}
	f := &Filter{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	f.Event = func(ev logs.Event) { e.mu.Lock(); e.events = append(e.events, ev); e.mu.Unlock() }
	f.SetDB(testDB(t))

	cfg := config.Default()
	lo := netip.MustParsePrefix("127.0.0.1/8")
	cfg.Interfaces[1].Address = &lo
	cfg.Profiles.WebFilter = []config.WebFilterProfile{{
		Name:         "kids",
		Categories:   map[string]string{"gambling": "block", "social_networks": "warn", "news": "monitor", "adult": "block"},
		BlockDomains: []string{"games.example"},
		AllowDomains: []string{"ok.casino.example"},
	}}
	cfg.Policies[0].WebFilterProfile = "kids"
	f.Resync(cfg)

	// fake Unbound: answers every query with the same message, QR set and
	// one byte appended so tests can tell it answered
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			resp := append(append([]byte(nil), buf[:n]...), 0x7f)
			resp[2] |= 0x80
			pc.WriteTo(resp, addr)
		}
	}()
	f.UpstreamPort = pc.LocalAddr().(*net.UDPAddr).Port
	e.f = f
	return e
}

var client = netip.MustParseAddr("127.0.0.9")

func (e *env) ask(t *testing.T, name string, qtype uint16) []byte {
	t.Helper()
	resp, err := e.f.Answer(context.Background(), client, query(7, name, qtype), false)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return resp
}

func forwarded(resp []byte) bool { return len(resp) > 0 && resp[len(resp)-1] == 0x7f }

func answerA(resp []byte) (netip.Addr, bool) {
	if binary.BigEndian.Uint16(resp[6:8]) != 1 {
		return netip.Addr{}, false
	}
	a, _ := netip.AddrFromSlice(resp[len(resp)-4:])
	return a, true
}

func TestParseQuestion(t *testing.T) {
	q, err := parseQuestion(query(1, "WWW.Example.COM", typeAAAA))
	if err != nil || q.name != "www.example.com" || q.qtype != typeAAAA {
		t.Fatalf("%+v %v", q, err)
	}
	for _, bad := range [][]byte{nil, make([]byte, 11), query(1, "a.b", 1)[:14], func() []byte { r := query(1, "a.b", 1); r[2] |= 0x80; return r }()} {
		if _, err := parseQuestion(bad); err == nil {
			t.Errorf("accepted %x", bad)
		}
	}
}

func TestDecide(t *testing.T) {
	p := Compile(config.WebFilterProfile{Name: "p", Categories: map[string]string{"gambling": "block", "news": "monitor"},
		BlockDomains: []string{"*.games.example"}, AllowDomains: []string{"ok.casino.example"}})
	db := testDB(t)
	for name, want := range map[string]Decision{
		"www.casino.example":  {ActionBlock, "gambling", "casino.example"},
		"x.ok.casino.example": {ActionAllow, "custom", "ok.casino.example"},
		"level.games.example": {ActionBlock, "custom", "games.example"},
		"news.example":        {ActionMonitor, "news", "news.example"},
		"social.example":      {ActionAllow, "", ""}, // not in the profile: allow
		"unknown.example":     {ActionAllow, "", ""},
	} {
		if got := p.Decide(db, name); got != want {
			t.Errorf("%s: %+v, want %+v", name, got, want)
		}
	}
}

func TestAnswer(t *testing.T) {
	e := newEnv(t)
	resp := e.ask(t, "www.casino.example", typeA)
	if a, ok := answerA(resp); !ok || a != netip.MustParseAddr("127.0.0.1") || forwarded(resp) || resp[0] != 0 || resp[1] != 7 {
		t.Fatalf("blocked A: %x", resp)
	}
	if resp := e.ask(t, "www.casino.example", typeAAAA); binary.BigEndian.Uint16(resp[6:8]) != 0 || forwarded(resp) {
		t.Fatalf("blocked AAAA should be empty: %x", resp)
	}
	if !forwarded(e.ask(t, "ok.casino.example", typeA)) || !forwarded(e.ask(t, "plain.example", typeA)) {
		t.Fatal("allowed names must be forwarded")
	}
	if !forwarded(e.ask(t, "news.example", typeA)) {
		t.Fatal("monitored names must be forwarded")
	}
	// warn: blocked until granted
	if forwarded(e.ask(t, "social.example", typeA)) {
		t.Fatal("warn should answer with the block page")
	}
	e.f.Grant(client, "social.example")
	if !forwarded(e.ask(t, "m.social.example", typeA)) {
		t.Fatal("granted warn should be forwarded")
	}
	if _, err := e.f.Answer(context.Background(), netip.MustParseAddr("10.9.9.9"), query(1, "x.example", 1), false); err == nil {
		t.Fatal("client outside the filtered subnets accepted")
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	actions := []string{}
	for _, ev := range e.events {
		actions = append(actions, ev.Action+":"+ev.Category+":"+ev.Message)
	}
	want := "deny:gambling:www.casino.example deny:gambling:www.casino.example allow:news:news.example warn:social_networks:social.example allow:social_networks:m.social.example"
	if strings.Join(actions, " ") != want {
		t.Fatalf("events:\n%s\nwant\n%s", strings.Join(actions, " "), want)
	}
}

func TestServeTCPAndUDP(t *testing.T) {
	e := newEnv(t)
	// TCP upstream for the TCP path
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				q, err := readTCP(c)
				if err != nil {
					return
				}
				q = append(q, 0x7f)
				q[2] |= 0x80
				writeTCP(c, q)
			}()
		}
	}()
	e.f.UpstreamPort = ln.Addr().(*net.TCPAddr).Port
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pl, _ := net.Listen("tcp", "127.0.0.1:0")
	go e.f.ServeTCP(ctx, pl)
	c, err := net.Dial("tcp", pl.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, name := range []string{"plain.example", "casino.example"} {
		writeTCP(c, query(9, name, typeA))
		resp, err := readTCP(c)
		if err != nil {
			t.Fatal(err)
		}
		if blocked := !forwarded(resp); blocked != (name == "casino.example") {
			t.Fatalf("tcp %s: %x", name, resp)
		}
	}

	pc, _ := net.ListenPacket("udp", "127.0.0.1:0")
	go e.f.ServeUDP(ctx, pc)
	uc, _ := net.Dial("udp", pc.LocalAddr().String())
	defer uc.Close()
	uc.Write(query(3, "nsfw.example", typeA))
	buf := make([]byte, 512)
	n, err := uc.Read(buf)
	if err != nil || forwarded(buf[:n]) {
		t.Fatalf("udp blocked: %x %v", buf[:n], err)
	}
}

func TestBlockPage(t *testing.T) {
	e := newEnv(t)
	bp := NewBlockPage(e.f)
	bp.TemplatePath = "/nonexistent"
	get := func(host string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://"+host+"/some/path", nil)
		r.RemoteAddr = "127.0.0.9:5555"
		w := httptest.NewRecorder()
		bp.ServeHTTP(w, r)
		return w
	}
	w := get("www.casino.example")
	if w.Code != 403 || !strings.Contains(w.Body.String(), "This site is blocked") || !strings.Contains(w.Body.String(), "gambling") {
		t.Fatalf("block page %d:\n%s", w.Code, w.Body)
	}
	w = get("social.example")
	body := w.Body.String()
	if w.Code != 403 || !strings.Contains(body, "Continue anyway") {
		t.Fatalf("warn page %d:\n%s", w.Code, body)
	}
	field := func(name string) string {
		i := strings.Index(body, `name="`+name+`" value="`)
		rest := body[i+len(`name="`+name+`" value="`):]
		return rest[:strings.Index(rest, `"`)]
	}
	post := func(token string) *httptest.ResponseRecorder {
		form := url.Values{"domain": {"social.example"}, "match": {field("match")}, "expires": {field("expires")}, "token": {token}}
		r := httptest.NewRequest("POST", "http://social.example/__goblin/continue", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.RemoteAddr = "127.0.0.9:5555"
		w := httptest.NewRecorder()
		bp.ServeHTTP(w, r)
		return w
	}
	if w := post("forged"); w.Code != 403 {
		t.Fatalf("forged token: %d", w.Code)
	}
	w = post(field("token"))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "http://social.example/" {
		t.Fatalf("continue: %d %v", w.Code, w.Header())
	}
	if !forwarded(e.ask(t, "social.example", typeA)) {
		t.Fatal("continue did not grant access")
	}
	if w := get("plain.example"); w.Code != 200 {
		t.Fatalf("allowed host: %d", w.Code)
	}
}

func TestAppControl(t *testing.T) {
	e := newEnv(t)
	cfg := config.Default()
	lo := netip.MustParsePrefix("127.0.0.1/8")
	cfg.Interfaces[1].Address = &lo
	cfg.Profiles.AppCtrl = []config.AppCtrlProfile{{Name: "kids", Groups: map[string]string{"Gaming": "block", "Video streaming": "monitor"}, Apps: map[string]string{"minecraft": "allow"}}}
	cfg.Policies[0].AppCtrlProfile = "kids"
	e.f.Resync(cfg)

	if forwarded(e.ask(t, "store.steampowered.com", typeA)) {
		t.Fatal("blocked app (group Gaming) was forwarded")
	}
	if !forwarded(e.ask(t, "www.minecraft.net", typeA)) {
		t.Fatal("app allowed by override was blocked")
	}
	if !forwarded(e.ask(t, "www.netflix.com", typeA)) {
		t.Fatal("monitored app must be forwarded")
	}
	if !forwarded(e.ask(t, "www.casino.example", typeA)) {
		t.Fatal("no web filter profile on this subnet now")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.events) != 2 || e.events[0].Kind != "appctrl" || e.events[0].App != "steam" || e.events[0].Action != "deny" || e.events[0].Category != "Gaming" ||
		e.events[1].App != "netflix" || e.events[1].Action != "allow" {
		t.Fatalf("events %+v", e.events)
	}

	bp := NewBlockPage(e.f)
	bp.TemplatePath = "/nonexistent"
	r := httptest.NewRequest("GET", "http://store.steampowered.com/", nil)
	r.RemoteAddr = "127.0.0.9:1"
	w := httptest.NewRecorder()
	bp.ServeHTTP(w, r)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "Steam (Gaming)") {
		t.Fatalf("block page %d: %s", w.Code, w.Body)
	}
}

// The DoH canaries answer NXDOMAIN so browsers keep using this DNS.
func TestDoHCanaries(t *testing.T) {
	e := newEnv(t)
	for _, name := range []string{"use-application-dns.net", "mask.icloud.com", "mask-h2.icloud.com"} {
		resp := e.ask(t, name, typeA)
		if forwarded(resp) || resp[3]&0x0f != 3 || binary.BigEndian.Uint16(resp[6:8]) != 0 || resp[0] != 0 || resp[1] != 7 {
			t.Fatalf("%s: want NXDOMAIN, got %x", name, resp)
		}
	}
	if !forwarded(e.ask(t, "www.use-application-dns.net", typeA)) {
		t.Fatal("only the canary names themselves are answered")
	}
}
