package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/apply"
	"github.com/networkgoblinlabs/goblin-gate/internal/commit"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

type testAPI struct {
	t       *testing.T
	srv     *httptest.Server
	store   *config.Store
	applier *apply.Fake
	server  *Server
}

func newTestAPI(t *testing.T) *testAPI {
	t.Helper()
	return newTestAPIWith(t, nil)
}

// newTestAPIWith lets a test set Server fields before the routes are built.
func newTestAPIWith(t *testing.T, configure func(*Server)) *testAPI {
	t.Helper()
	store, err := config.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	applier := apply.NewFake()
	engine := commit.New(store, nil, applier)
	engine.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewServer(store, engine)
	s.Logger = engine.Logger
	if configure != nil {
		configure(s)
	}
	ts := httptest.NewServer(s.Routes())
	t.Cleanup(ts.Close)
	return &testAPI{t: t, srv: ts, store: store, applier: applier, server: s}
}

// do sends a request; body may be nil, a string (sent as is) or a value
// (JSON-encoded). It returns the status and decoded JSON body.
func (a *testAPI) do(method, path string, body any) (int, map[string]any) {
	a.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		data, err := json.Marshal(b)
		if err != nil {
			a.t.Fatal(err)
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, a.srv.URL+path, rd)
	if err != nil {
		a.t.Fatal(err)
	}
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		a.t.Fatalf("%s %s: content-type %q", method, path, ct)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		a.t.Fatalf("%s %s: decode: %v", method, path, err)
	}
	return resp.StatusCode, out
}

func (a *testAPI) expect(method, path string, body any, want int) map[string]any {
	a.t.Helper()
	got, out := a.do(method, path, body)
	if got != want {
		a.t.Fatalf("%s %s: status %d, want %d, body %v", method, path, got, want, out)
	}
	return out
}

func errCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

func TestRunningAndCandidate(t *testing.T) {
	a := newTestAPI(t)
	run := a.expect("GET", "/api/v1/config/running", nil, 200)
	if _, ok := run["system"]; !ok {
		t.Fatalf("running config missing system: %v", run)
	}
	cand := a.expect("GET", "/api/v1/config/candidate", nil, 200)
	if _, ok := cand["policies"]; !ok {
		t.Fatalf("candidate missing policies: %v", cand)
	}
}

func TestPutCandidate(t *testing.T) {
	a := newTestAPI(t)
	cfg := config.Default()
	cfg.System.Hostname = "put-test"
	out := a.expect("PUT", "/api/v1/config/candidate", cfg, 200)
	if errs, _ := out["validation_errors"].([]any); len(errs) != 0 {
		t.Fatalf("unexpected validation errors: %v", errs)
	}
	got, _ := a.store.Candidate()
	if got.System.Hostname != "put-test" {
		t.Fatalf("candidate hostname = %q", got.System.Hostname)
	}

	// invalid but well-formed config is stored with its problems listed
	cfg.System.Hostname = ""
	out = a.expect("PUT", "/api/v1/config/candidate", cfg, 200)
	if errs, _ := out["validation_errors"].([]any); len(errs) == 0 {
		t.Fatal("expected validation errors for empty hostname")
	}

	// errors
	if out := a.expect("PUT", "/api/v1/config/candidate", `{"nope": 1}`, 400); errCode(out) != "invalid_config" {
		t.Fatalf("unknown field: %v", out)
	}
	if out := a.expect("PUT", "/api/v1/config/candidate", `{not json`, 400); errCode(out) != "invalid_json" {
		t.Fatalf("bad json: %v", out)
	}
}

func TestContentTypeRequired(t *testing.T) {
	a := newTestAPI(t)
	req, _ := http.NewRequest("PUT", a.srv.URL+"/api/v1/config/candidate", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestBodyTooLarge(t *testing.T) {
	a := newTestAPI(t)
	big := `{"path":"system.hostname","value":"` + strings.Repeat("a", MaxBodyBytes) + `"}`
	if out := a.expect("PATCH", "/api/v1/config/candidate", big, 413); errCode(out) != "body_too_large" {
		t.Fatalf("got %v", out)
	}
}

func TestPatchCandidate(t *testing.T) {
	a := newTestAPI(t)
	id := strconv.FormatUint(uint64(config.Default().Policies[0].ID), 10)
	a.expect("PATCH", "/api/v1/config/candidate", map[string]any{"path": "policies[id=" + id + "].action", "value": "deny"}, 200)
	a.expect("PATCH", "/api/v1/config/candidate", map[string]any{"path": "addresses[name=nas]", "value": map[string]any{"type": "host", "value": "192.168.1.5"}}, 200)
	got, _ := a.store.Candidate()
	if got.Policies[0].Action != "deny" {
		t.Fatalf("action = %q", got.Policies[0].Action)
	}
	if len(got.Addresses) == 0 || got.Addresses[len(got.Addresses)-1].Name != "nas" {
		t.Fatalf("addresses = %+v", got.Addresses)
	}
	a.expect("PATCH", "/api/v1/config/candidate", map[string]any{"path": "addresses[name=nas]", "delete": true}, 200)

	for _, bad := range []any{
		map[string]any{"path": "system.nope", "value": 1},
		map[string]any{"path": "policies[id=4242].action", "value": "deny"},
		map[string]any{"path": "system.hostname"},
		map[string]any{"path": "system.hostname", "value": "x", "delete": true},
		map[string]any{"value": "x"},
	} {
		if out := a.expect("PATCH", "/api/v1/config/candidate", bad, 400); errCode(out) != "invalid_patch" {
			t.Fatalf("%v: %v", bad, out)
		}
	}
}

func TestValidate(t *testing.T) {
	a := newTestAPI(t)
	out := a.expect("POST", "/api/v1/config/validate", nil, 200)
	if out["valid"] != true {
		t.Fatalf("default candidate should be valid: %v", out)
	}
	cfg := config.Default()
	cfg.System.Hostname = ""
	out = a.expect("POST", "/api/v1/config/validate", cfg, 200)
	errs, _ := out["errors"].([]any)
	if out["valid"] != false || len(errs) == 0 {
		t.Fatalf("expected errors: %v", out)
	}
	first, _ := errs[0].(map[string]any)
	if first["path"] == "" || first["path"] == nil {
		t.Fatalf("validation errors must carry a path: %v", first)
	}
	a.expect("POST", "/api/v1/config/validate", `[1,2]`, 400)
}

func TestDiffCommitAndRevisions(t *testing.T) {
	a := newTestAPI(t)
	if out := a.expect("GET", "/api/v1/config/diff", nil, 200); out["count"].(float64) != 0 {
		t.Fatalf("fresh diff should be empty: %v", out)
	}
	a.expect("PATCH", "/api/v1/config/candidate", map[string]any{"path": "system.hostname", "value": "gate1"}, 200)
	diff := a.expect("GET", "/api/v1/config/diff", nil, 200)
	if diff["count"].(float64) != 1 {
		t.Fatalf("diff = %v", diff)
	}
	out := a.expect("POST", "/api/v1/commit", map[string]any{"comment": "rename"}, 200)
	if out["revision"].(float64) != 1 {
		t.Fatalf("commit = %v", out)
	}
	run, _ := a.store.Running()
	if run.System.Hostname != "gate1" {
		t.Fatalf("running hostname = %q", run.System.Hostname)
	}
	if out := a.expect("GET", "/api/v1/config/diff", nil, 200); out["count"].(float64) != 0 {
		t.Fatalf("diff after commit: %v", out)
	}
	revs := a.expect("GET", "/api/v1/revisions", nil, 200)["revisions"].([]any)
	if len(revs) != 1 {
		t.Fatalf("revisions = %v", revs)
	}

	// commit with an invalid candidate → 422 with details
	a.expect("PATCH", "/api/v1/config/candidate", map[string]any{"path": "system.hostname", "value": ""}, 200)
	out = a.expect("POST", "/api/v1/commit", nil, 422)
	if errCode(out) != "validation_failed" {
		t.Fatalf("got %v", out)
	}
	if a.applier.Calls() != 1 {
		t.Fatalf("invalid commit must not apply, calls = %d", a.applier.Calls())
	}
}

func TestCommitConfirmAndDiscard(t *testing.T) {
	a := newTestAPI(t)
	a.expect("POST", "/api/v1/commit/confirm", nil, 409)
	a.expect("PATCH", "/api/v1/config/candidate", map[string]any{"path": "system.hostname", "value": "trial"}, 200)
	out := a.expect("POST", "/api/v1/commit", map[string]any{"confirm_minutes": 5}, 200)
	if _, ok := out["confirm_deadline"]; !ok {
		t.Fatalf("missing confirm_deadline: %v", out)
	}
	st := a.expect("GET", "/api/v1/system/status", nil, 200)
	if st["pending_confirm"] == nil {
		t.Fatalf("status should show pending confirm: %v", st)
	}
	a.expect("POST", "/api/v1/commit/confirm", nil, 200)
	a.expect("POST", "/api/v1/commit", map[string]any{"confirm_minutes": -1}, 400)

	a.expect("PATCH", "/api/v1/config/candidate", map[string]any{"path": "system.hostname", "value": "scrap"}, 200)
	a.expect("POST", "/api/v1/commit/discard", nil, 200)
	cand, _ := a.store.Candidate()
	if cand.System.Hostname != "trial" {
		t.Fatalf("candidate after discard = %q, want running value", cand.System.Hostname)
	}
}

func TestRollback(t *testing.T) {
	a := newTestAPI(t)
	for _, h := range []string{"one", "two"} {
		a.expect("PATCH", "/api/v1/config/candidate", map[string]any{"path": "system.hostname", "value": h}, 200)
		a.expect("POST", "/api/v1/commit", nil, 200)
	}
	out := a.expect("POST", "/api/v1/revisions/1/rollback", nil, 200)
	if out["revision"].(float64) != 3 {
		t.Fatalf("rollback = %v", out)
	}
	run, _ := a.store.Running()
	if run.System.Hostname != "one" {
		t.Fatalf("running = %q", run.System.Hostname)
	}
	a.expect("POST", "/api/v1/revisions/99/rollback", nil, 404)
	a.expect("POST", "/api/v1/revisions/abc/rollback", nil, 400)
}

func TestStatusAndNotFound(t *testing.T) {
	a := newTestAPI(t)
	st := a.expect("GET", "/api/v1/system/status", nil, 200)
	for _, k := range []string{"version", "hostname", "uptime_seconds"} {
		if _, ok := st[k]; !ok {
			t.Fatalf("status missing %s: %v", k, st)
		}
	}
	if out := a.expect("GET", "/api/v1/nope", nil, 404); errCode(out) != "not_found" {
		t.Fatalf("got %v", out)
	}
}

func TestAuthHook(t *testing.T) {
	store, _ := config.New(t.TempDir())
	s := NewServer(store, commit.New(store, nil, apply.NewFake()))
	s.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	s.Auth = func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "login required", nil)
		})
	}
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/config/running", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rec.Code)
	}
}

// HSTS is sent on HTTPS connections only.
func TestHSTSOverTLS(t *testing.T) {
	a := newTestAPI(t)
	ts := httptest.NewTLSServer(a.server.Routes())
	t.Cleanup(ts.Close)
	resp, err := ts.Client().Get(ts.URL + "/api/v1/system/status")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("Strict-Transport-Security") == "" || resp.Header.Get("Content-Security-Policy") == "" {
		t.Fatalf("headers %v", resp.Header)
	}
}
