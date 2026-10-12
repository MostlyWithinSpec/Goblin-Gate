package notify

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/logs"
)

// recorder is an HTTP endpoint that records requests and answers with the
// next status from codes (then 200).
type recorder struct {
	mu    sync.Mutex
	reqs  []recorded
	codes []int
	srv   *httptest.Server
}

type recorded struct {
	path, body string
	header     http.Header
}

func newRecorder(t *testing.T, codes ...int) *recorder {
	r := &recorder{codes: codes}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.reqs = append(r.reqs, recorded{req.URL.Path, string(b), req.Header.Clone()})
		code := 200
		if len(r.codes) > 0 {
			code, r.codes = r.codes[0], r.codes[1:]
		}
		r.mu.Unlock()
		w.WriteHeader(code)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *recorder) all() []recorded {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recorded(nil), r.reqs...)
}

func dispatcher(n config.Notifications) *Dispatcher {
	return &Dispatcher{
		Config:   func() (config.Notifications, error) { return n, nil },
		Hostname: func() string { return "gate" },
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Backoff:  time.Millisecond,
	}
}

var ev = Event{Kind: "commit", Title: "Config committed", Message: "revision 7 by admin", Time: time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)}

func TestSenders(t *testing.T) {
	hook, disc, ntfy, tg := newRecorder(t), newRecorder(t), newRecorder(t), newRecorder(t)
	n := config.Notifications{
		Channels: []config.NotifyChannel{
			{Name: "hook", Type: "webhook", Enabled: true, URL: hook.srv.URL + "/in", Token: "s3cret"},
			{Name: "disc", Type: "discord", Enabled: true, URL: disc.srv.URL + "/api/webhooks/1/x"},
			{Name: "ntfy", Type: "ntfy", Enabled: true, URL: ntfy.srv.URL + "/my-gate"},
			{Name: "tg", Type: "telegram", Enabled: true, BotToken: "123:ABC", ChatID: "42"},
		},
		Rules: []config.NotifyRule{{Event: "commit", Channels: []string{"hook", "disc", "ntfy", "tg"}}},
	}
	d := dispatcher(n)
	d.TelegramAPI = tg.srv.URL
	var delivered []string
	d.Delivered = func(_ Event, ch []string) { delivered = ch }
	d.deliver(context.Background(), ev)

	if r := hook.all(); len(r) != 1 || r[0].header.Get("Authorization") != "Bearer s3cret" || !strings.Contains(r[0].body, `"event":"commit"`) || !strings.Contains(r[0].body, `"title":"gate: Config committed"`) {
		t.Errorf("webhook: %+v", r)
	}
	if r := disc.all(); len(r) != 1 || !strings.Contains(r[0].body, `"content":"**gate: Config committed**\nrevision 7 by admin"`) {
		t.Errorf("discord: %+v", r)
	}
	if r := ntfy.all(); len(r) != 1 || r[0].path != "/my-gate" || r[0].body != "revision 7 by admin" || r[0].header.Get("Title") != "gate: Config committed" {
		t.Errorf("ntfy: %+v", r)
	}
	if r := tg.all(); len(r) != 1 || r[0].path != "/bot123:ABC/sendMessage" || !strings.Contains(r[0].body, `"chat_id":"42"`) {
		t.Errorf("telegram: %+v", r)
	}
	if strings.Join(delivered, ",") != "hook,disc,ntfy,tg" {
		t.Errorf("delivered to %v", delivered)
	}
}

func TestRulesAndDisabledChannels(t *testing.T) {
	a, b := newRecorder(t), newRecorder(t)
	d := dispatcher(config.Notifications{
		Channels: []config.NotifyChannel{
			{Name: "a", Type: "webhook", Enabled: true, URL: a.srv.URL},
			{Name: "b", Type: "webhook", Enabled: false, URL: b.srv.URL},
		},
		Rules: []config.NotifyRule{{Event: "commit", Channels: []string{"a", "b"}}},
	})
	d.deliver(context.Background(), ev)
	d.deliver(context.Background(), Event{Kind: "feed_error", Key: "x", Title: "t"}) // no rule
	if len(a.all()) != 1 || len(b.all()) != 0 {
		t.Fatalf("a=%d b=%d", len(a.all()), len(b.all()))
	}
}

func TestRateLimitPerKey(t *testing.T) {
	r := newRecorder(t)
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	d := dispatcher(config.Notifications{
		Channels: []config.NotifyChannel{{Name: "a", Type: "webhook", Enabled: true, URL: r.srv.URL}},
		Rules:    []config.NotifyRule{{Event: "feed_error", Channels: []string{"a"}}},
	})
	d.Now = func() time.Time { return now }
	send := func(key string) {
		d.deliver(context.Background(), Event{Kind: "feed_error", Key: key, Title: "feed failed"})
	}
	send("spamhaus")
	send("spamhaus") // same key within 10 min: dropped
	send("ads")      // another feed: sent
	now = now.Add(11 * time.Minute)
	send("spamhaus") // later: sent again
	if got := len(r.all()); got != 3 {
		t.Fatalf("%d messages, want 3", got)
	}
}

func TestRetries(t *testing.T) {
	flaky := newRecorder(t, 502, 503) // two failures, then ok
	refused := newRecorder(t, 401)    // permanent: no retry
	d := dispatcher(config.Notifications{
		Channels: []config.NotifyChannel{
			{Name: "flaky", Type: "webhook", Enabled: true, URL: flaky.srv.URL},
			{Name: "refused", Type: "webhook", Enabled: true, URL: refused.srv.URL},
		},
		Rules: []config.NotifyRule{{Event: "commit", Channels: []string{"flaky", "refused"}}},
	})
	var delivered []string
	d.Delivered = func(_ Event, ch []string) { delivered = ch }
	d.deliver(context.Background(), ev)
	if len(flaky.all()) != 3 || len(refused.all()) != 1 {
		t.Fatalf("flaky %d tries (want 3), refused %d (want 1)", len(flaky.all()), len(refused.all()))
	}
	if strings.Join(delivered, ",") != "flaky" {
		t.Fatalf("delivered %v", delivered)
	}
}

func TestTelegramErrorHidesToken(t *testing.T) {
	d := dispatcher(config.Notifications{})
	d.TelegramAPI = "http://127.0.0.1:1" // nothing listens
	err := d.sendTelegram(context.Background(), config.NotifyChannel{BotToken: "123:SECRET", ChatID: "1"}, ev)
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("error %v", err)
	}
}

func TestPublishAndRun(t *testing.T) {
	r := newRecorder(t)
	d := dispatcher(config.Notifications{
		Channels: []config.NotifyChannel{{Name: "a", Type: "webhook", Enabled: true, URL: r.srv.URL}},
		Rules:    []config.NotifyRule{{Event: "commit", Channels: []string{"a"}}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)
	d.Publish(ev)
	deadline := time.Now().Add(5 * time.Second)
	for len(r.all()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(r.all()) != 1 {
		t.Fatal("published event not delivered")
	}
	var nilD *Dispatcher
	nilD.Publish(ev) // a nil dispatcher is a no-op
}

func TestTestMessage(t *testing.T) {
	r := newRecorder(t)
	d := dispatcher(config.Notifications{Channels: []config.NotifyChannel{{Name: "a", Type: "webhook", Enabled: false, URL: r.srv.URL}}})
	if err := d.Test(context.Background(), "a"); err != nil { // works even when disabled and without rules
		t.Fatal(err)
	}
	if !strings.Contains(r.all()[0].body, `"event":"test"`) {
		t.Fatalf("%+v", r.all())
	}
	if err := d.Test(context.Background(), "nope"); !errors.Is(err, ErrUnknownChannel) {
		t.Fatalf("%v", err)
	}
}

// fakeSMTP speaks just enough SMTP for net/smtp (no STARTTLS, no AUTH) and
// returns the DATA it received.
func fakeSMTP(t *testing.T) (port int, got chan string) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got = make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		rd := bufio.NewReader(c)
		say := func(s string) { io.WriteString(c, s+"\r\n") }
		say("220 fake ESMTP")
		var data strings.Builder
		inData := false
		for {
			line, err := rd.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					say("250 queued")
					got <- data.String()
					continue
				}
				data.WriteString(line)
				continue
			}
			switch cmd := strings.ToUpper(strings.Fields(line + " x")[0]); cmd {
			case "EHLO", "HELO":
				say("250 fake")
			case "DATA":
				inData = true
				say("354 go on")
			case "QUIT":
				say("221 bye")
				return
			default:
				say("250 ok")
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, got
}

func TestEmail(t *testing.T) {
	port, got := fakeSMTP(t)
	c := config.NotifyChannel{Name: "mail", Type: "email", Enabled: true, SMTPHost: "127.0.0.1", SMTPPort: port,
		From: "gate@example.com", To: []string{"me@example.com"}}
	e := ev
	e.Title = "gate: subject\r\nBcc: evil@example.com" // header injection attempt
	if err := sendEmail(context.Background(), c, e); err != nil {
		t.Fatal(err)
	}
	select {
	case data := <-got:
		if !strings.Contains(data, "Subject: gate: subject  Bcc: evil@example.com\r\n") || !strings.Contains(data, "revision 7 by admin") {
			t.Fatalf("message:\n%s", data)
		}
		if strings.Contains(data, "\r\nBcc:") {
			t.Fatal("a header was injected")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no mail received")
	}
}

func TestIPSBatcher(t *testing.T) {
	b := &IPSBatcher{}
	b.Observe(&logs.Event{Kind: "traffic", Action: "deny"}) // not IPS
	b.Observe(&logs.Event{Kind: "ips", Action: "alert"})    // detect only: no block
	for i := 0; i < 3; i++ {
		b.Observe(&logs.Event{Kind: "ips", Action: "drop", Signature: "2010935: ET SCAN MSSQL", SrcIP: "203.0.113.9"})
	}
	b.Observe(&logs.Event{Kind: "ips", Action: "drop", Signature: "2100498: GPL id check", SrcIP: "203.0.113.10"})
	ev, ok := b.flush()
	if !ok || ev.Kind != "ips_block" || ev.Title != "IPS blocked 4 connection(s)" {
		t.Fatalf("%+v", ev)
	}
	if !strings.Contains(ev.Message, "From 2 source address(es)") || !strings.HasPrefix(strings.Split(ev.Message, "\n")[1], "3 × 2010935") {
		t.Fatalf("message:\n%s", ev.Message)
	}
	if _, ok := b.flush(); ok {
		t.Fatal("counts not reset")
	}
}
