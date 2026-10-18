package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/cli"
)

// recordingHandler captures the requests it receives so tests can assert on
// method, path and body.
type recordingHandler struct {
	requests []recordedRequest
	handler  http.HandlerFunc
}

type recordedRequest struct {
	Method string
	Path   string
	Body   []byte
}

func (h *recordingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	h.requests = append(h.requests, recordedRequest{Method: r.Method, Path: r.URL.Path, Body: body})
	if h.handler != nil {
		h.handler(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/v1/config/candidate":
		if r.Method == http.MethodPatch {
			io.WriteString(w, `{"status":"saved"}`)
		} else {
			io.WriteString(w, `{"interfaces":[{"name":"lan","address":"192.168.1.1/24"}]}`)
		}
	case "/api/v1/commit":
		io.WriteString(w, `{"revision":1}`)
	default:
		// Handle rollback endpoints: /api/v1/revisions/{rev}/rollback
		if strings.HasPrefix(r.URL.Path, "/api/v1/revisions/") && strings.HasSuffix(r.URL.Path, "/rollback") {
			io.WriteString(w, `{"status":"ok"}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"error":{"code":"not_found","message":"no such endpoint"}}`)
	}
}

func newTestShell(t *testing.T) (*interactiveShell, *recordingHandler) {
	t.Helper()
	h := &recordingHandler{}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	client := newGoblinClient(srv.URL, "test-token")
	return &interactiveShell{client: client}, h
}

func TestExecuteLineConfigEditCommit(t *testing.T) {
	shell, h := newTestShell(t)

	// Edit the LAN address.
	err := shell.executeLine("config system interface edit lan set address 192.168.1.1/24 next end")
	if err != nil {
		t.Fatalf("executeLine: %v", err)
	}

	// Commit.
	err = shell.executeLine("commit")
	if err != nil {
		t.Fatalf("commit: %v", err)
	}

	if len(h.requests) != 2 {
		t.Fatalf("expected 2 requests, got %d: %+v", len(h.requests), h.requests)
	}

	// First request: PATCH candidate.
	patch := h.requests[0]
	if patch.Method != http.MethodPatch || patch.Path != "/api/v1/config/candidate" {
		t.Fatalf("request 0 = %s %s, want PATCH /api/v1/config/candidate", patch.Method, patch.Path)
	}
	var patchBody struct {
		Path  string `json:"path"`
		Value any    `json:"value"`
	}
	if err := json.Unmarshal(patch.Body, &patchBody); err != nil {
		t.Fatalf("decode PATCH body: %v", err)
	}
	if patchBody.Path != "interfaces[name=lan].address" {
		t.Errorf("PATCH path = %q, want interfaces[name=lan].address", patchBody.Path)
	}
	if patchBody.Value != "192.168.1.1/24" {
		t.Errorf("PATCH value = %v, want 192.168.1.1/24", patchBody.Value)
	}

	// Second request: POST commit.
	commit := h.requests[1]
	if commit.Method != http.MethodPost || commit.Path != "/api/v1/commit" {
		t.Fatalf("request 1 = %s %s, want POST /api/v1/commit", commit.Method, commit.Path)
	}
}

func TestExecuteLineShow(t *testing.T) {
	shell, h := newTestShell(t)

	err := shell.executeLine("show system interface")
	if err != nil {
		t.Fatalf("executeLine: %v", err)
	}

	if len(h.requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(h.requests))
	}
	req := h.requests[0]
	if req.Method != http.MethodGet || req.Path != "/api/v1/config/candidate" {
		t.Fatalf("request = %s %s, want GET /api/v1/config/candidate", req.Method, req.Path)
	}
}

func TestExecuteLineRollback(t *testing.T) {
	shell, h := newTestShell(t)

	err := shell.executeLine("rollback 3")
	if err != nil {
		t.Fatalf("executeLine: %v", err)
	}

	if len(h.requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(h.requests))
	}
	req := h.requests[0]
	if req.Method != http.MethodPost || req.Path != "/api/v1/revisions/3/rollback" {
		t.Fatalf("request = %s %s, want POST /api/v1/revisions/3/rollback", req.Method, req.Path)
	}
}

func TestRunCommandExitCode(t *testing.T) {
	h := &recordingHandler{}
	srv := httptest.NewServer(h)
	defer srv.Close()

	// Success: commit returns 200.
	code := runCommand(srv.URL, "tok", "commit")
	if code != 0 {
		t.Errorf("runCommand success: exit = %d, want 0", code)
	}

	// Failure: unknown command.
	code = runCommand(srv.URL, "tok", "bogus-command")
	if code != 1 {
		t.Errorf("runCommand error: exit = %d, want 1", code)
	}
}

func TestRunScriptStopsOnFirstError(t *testing.T) {
	var patchCount int
	h := &recordingHandler{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/config/candidate" && r.Method == http.MethodPatch {
			patchCount++
			// First PATCH succeeds, second fails.
			if patchCount < 2 {
				io.WriteString(w, `{"status":"saved"}`)
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":{"code":"invalid","message":"bad value"}}`)
			return
		}
		if r.URL.Path == "/api/v1/commit" {
			io.WriteString(w, `{"revision":1}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})}
	srv := httptest.NewServer(h)
	defer srv.Close()

	script := filepath.Join(t.TempDir(), "test.gob")
	content := "config system interface edit lan set address 10.0.0.1/24 next end\n" +
		"config system interface edit wan set address 999.999.999.999/24 next end\n" +
		"commit\n"
	if err := os.WriteFile(script, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	code := runScript(srv.URL, "tok", script)
	if code != 1 {
		t.Errorf("runScript: exit = %d, want 1 (should stop on error)", code)
	}

	// Should have made exactly 2 PATCH requests (the second one failed), not 3 total.
	if patchCount != 2 {
		t.Errorf("PATCH count = %d, want 2 (stopped at first error)", patchCount)
	}
}

func TestRunScriptSuccess(t *testing.T) {
	h := &recordingHandler{}
	srv := httptest.NewServer(h)
	defer srv.Close()

	script := filepath.Join(t.TempDir(), "ok.gob")
	content := "# comment line\n" +
		"config system interface edit lan set address 10.0.0.1/24 next end\n" +
		"commit\n"
	if err := os.WriteFile(script, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	code := runScript(srv.URL, "tok", script)
	if code != 0 {
		t.Errorf("runScript success: exit = %d, want 0", code)
	}

	if len(h.requests) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(h.requests))
	}
}

func TestCoerceValue(t *testing.T) {
	tests := []struct {
		in   string
		want any
	}{
		{"true", true},
		{"false", false},
		{"42", 42},
		{"3.14", 3.14},
		{"hello", "hello"},
		{"192.168.1.1/24", "192.168.1.1/24"},
	}
	for _, tt := range tests {
		got := coerceValue(tt.in)
		if got != tt.want {
			t.Errorf("coerceValue(%q) = %v (%T), want %v (%T)", tt.in, got, got, tt.want, tt.want)
		}
	}
}

func TestOpToPatchPath(t *testing.T) {
	tests := []struct {
		path string
		want string
		ok   bool
	}{
		{"interfaces.lan.address", "interfaces[name=lan].address", true},
		{"policies.10.action", "policies[id=10].action", true},
		{"addresses.srv.value", "addresses[name=srv].value", true},
		{"interfaces.lan", "", false}, // too few parts
	}
	for _, tt := range tests {
		op := cli.Operation{Path: tt.path}
		got, err := opToPatchPath(op)
		if !tt.ok {
			if err == nil {
				t.Errorf("opToPatchPath(%q): expected error, got %q", tt.path, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("opToPatchPath(%q): %v", tt.path, err)
			continue
		}
		if got != tt.want {
			t.Errorf("opToPatchPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestExecuteLineUnknownCommand(t *testing.T) {
	shell, _ := newTestShell(t)
	err := shell.executeLine("frobnicate")
	if err == nil {
		t.Fatal("expected error for unknown command")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("error = %q, want contains 'unknown command'", err.Error())
	}
}

func TestDiagnosePolicy(t *testing.T) {
	shell, h := newTestShell(t)
	h.handler = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"verdict":"deny","reason":"denied by policy 2 \"iot-no-lan\"","chain":"forward","src_intf":"iot","dst_intf":"lan","policy":{"id":2,"name":"iot-no-lan"},"steps":[{"id":1,"name":"iot-to-ha","matched":false,"reason":"tcp port 22 is not in service ha-web"},{"id":2,"name":"iot-no-lan","matched":true,"reason":"matches"}]}`)
	}
	if err := shell.executeLine("diagnose policy 192.168.20.5 192.168.1.23 tcp 22"); err != nil {
		t.Fatal(err)
	}
	req := h.requests[0]
	var body map[string]any
	json.Unmarshal(req.Body, &body)
	if req.Method != http.MethodPost || req.Path != "/api/v1/policy/simulate" || body["src_ip"] != "192.168.20.5" || body["protocol"] != "tcp" || body["dst_port"] != float64(22) {
		t.Fatalf("request %s %s %v", req.Method, req.Path, body)
	}
	var out strings.Builder
	var res simResult
	json.Unmarshal([]byte(`{"verdict":"deny","reason":"r","chain":"forward","src_intf":"iot","dst_intf":"lan","steps":[{"id":1,"name":"a","matched":false,"reason":"why"}]}`), &res)
	printSimulation(&out, &res)
	if !strings.Contains(out.String(), "DENY: r") || !strings.Contains(out.String(), "[x] policy 1 a: why") {
		t.Fatalf("output:\n%s", out.String())
	}
	if err := shell.executeLine("diagnose policy 1.2.3.4"); err == nil {
		t.Error("missing destination accepted")
	}
}

func TestExecuteLineSnapshotSave(t *testing.T) {
	shell, h := newTestShell(t)
	h.handler = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/snapshots" {
			io.WriteString(w, `{"name":"pre-upgrade"}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}
	if err := shell.executeLine("snapshot save pre-upgrade before the upgrade"); err != nil {
		t.Fatalf("snapshot save: %v", err)
	}
	if len(h.requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(h.requests))
	}
	req := h.requests[0]
	if req.Method != http.MethodPost || req.Path != "/api/v1/snapshots" {
		t.Fatalf("request = %s %s, want POST /api/v1/snapshots", req.Method, req.Path)
	}
	var body map[string]any
	json.Unmarshal(req.Body, &body)
	if body["name"] != "pre-upgrade" || body["comment"] != "before the upgrade" || body["from"] != "running" {
		t.Errorf("body = %v", body)
	}
}

func TestExecuteLineSnapshotRestore(t *testing.T) {
	shell, h := newTestShell(t)
	h.handler = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/snapshots/pre-upgrade/restore" {
			io.WriteString(w, `{"status":"ok","changes":3}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}
	if err := shell.executeLine("snapshot restore pre-upgrade"); err != nil {
		t.Fatalf("snapshot restore: %v", err)
	}
	if len(h.requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(h.requests))
	}
	req := h.requests[0]
	if req.Method != http.MethodPost || req.Path != "/api/v1/snapshots/pre-upgrade/restore" {
		t.Fatalf("request = %s %s, want POST /api/v1/snapshots/pre-upgrade/restore", req.Method, req.Path)
	}
}

func TestExecuteLineSnapshotList(t *testing.T) {
	shell, h := newTestShell(t)
	h.handler = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/snapshots" {
			io.WriteString(w, `{"snapshots":[{"name":"a","comment":"c","created":"2026-01-01T00:00:00Z","created_by":"admin","revision":1}]}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}
	if err := shell.executeLine("snapshot list"); err != nil {
		t.Fatalf("snapshot list: %v", err)
	}
	if len(h.requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(h.requests))
	}
	req := h.requests[0]
	if req.Method != http.MethodGet || req.Path != "/api/v1/snapshots" {
		t.Fatalf("request = %s %s, want GET /api/v1/snapshots", req.Method, req.Path)
	}
}

func TestExecuteLineSnapshotErrors(t *testing.T) {
	shell, _ := newTestShell(t)
	if err := shell.executeLine("snapshot"); err == nil {
		t.Error("bare 'snapshot' should error")
	}
	if err := shell.executeLine("snapshot save"); err == nil {
		t.Error("snapshot save without name should error")
	}
	if err := shell.executeLine("snapshot restore"); err == nil {
		t.Error("snapshot restore without name should error")
	}
	if err := shell.executeLine("snapshot frobnicate"); err == nil {
		t.Error("unknown snapshot sub-command should error")
	}
}
