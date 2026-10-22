package api

import (
	"testing"
	"time"
)

func TestReboot(t *testing.T) {
	old := rebootDelay
	rebootDelay = 0
	t.Cleanup(func() { rebootDelay = old })
	a := newAuthAPI(t)
	done := make(chan struct{}, 1)
	a.server.Reboot = func() error { done <- struct{}{}; return nil }

	viewer := a.browser()
	viewer.login("viewer")
	viewer.expect("POST", "/api/v1/system/reboot", nil, 403)

	admin := a.browser()
	admin.login("admin")
	tok := admin.expect("POST", "/api/v1/auth/tokens", map[string]any{"name": "s", "scopes": []string{"read", "actions", "write:system", "commit"}}, 201)
	scoped := a.browser()
	scoped.header["Authorization"] = "Bearer " + tok["token"].(string)
	scoped.expect("POST", "/api/v1/system/reboot", nil, 403)
	select {
	case <-done:
		t.Fatal("restarted for someone who may not")
	default:
	}

	admin.expect("POST", "/api/v1/system/reboot", nil, 202)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("no restart")
	}
}

func TestRebootUnavailableInDev(t *testing.T) {
	newTestAPI(t).expect("POST", "/api/v1/system/reboot", nil, 503)
}
