package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/notify"
)

func TestNotificationsTestAndCommitEvent(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
	}))
	defer hook.Close()
	got := func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), bodies...) }

	a := newAuthAPI(t)
	run, _ := a.store.Running()
	run.Notifications = config.Notifications{
		Channels: []config.NotifyChannel{{Name: "hook", Type: "webhook", Enabled: true, URL: hook.URL}},
		Rules:    []config.NotifyRule{{Event: "commit", Channels: []string{"hook"}}},
	}
	if _, err := a.store.SaveRevision(run, "test", "notifications"); err != nil {
		t.Fatal(err)
	}
	if err := a.store.SetCandidate(run); err != nil {
		t.Fatal(err)
	}
	d := &notify.Dispatcher{Config: func() (config.Notifications, error) {
		c, err := a.store.Running()
		return c.Notifications, err
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)
	a.server.Notify = d

	b := a.browser()
	b.login("admin")
	b.expect("POST", "/api/v1/notifications/test", map[string]string{"channel": "nope"}, 404)
	b.expect("POST", "/api/v1/notifications/test", map[string]string{"channel": "hook"}, 200)
	if g := got(); len(g) != 1 || !strings.Contains(g[0], `"event":"test"`) {
		t.Fatalf("test message: %v", g)
	}

	b.expect("POST", "/api/v1/commit", map[string]string{"comment": "hello"}, 200)
	deadline := time.Now().Add(5 * time.Second)
	for len(got()) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if g := got(); len(g) != 2 || !strings.Contains(g[1], `"event":"commit"`) || !strings.Contains(g[1], "by admin: hello") {
		t.Fatalf("commit message: %v", g)
	}
}
