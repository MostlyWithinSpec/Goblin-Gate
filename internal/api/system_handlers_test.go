package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/auth"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func TestAdminsLifecycle(t *testing.T) {
	a := newAuthAPI(t)
	b := a.browser()
	b.login("admin")

	list := b.expect("GET", "/api/v1/admins", nil, 200)["admins"].([]any)
	if len(list) != 2 || strings.Contains(strings.ToLower(jsonString(list)), "hash") {
		t.Fatalf("list %v", list)
	}

	// create: validation, then success and a working login
	b.expect("POST", "/api/v1/admins", map[string]string{"username": "bad name", "password": testPassword}, 422)
	b.expect("POST", "/api/v1/admins", map[string]string{"username": "kim", "password": "short"}, 422)
	b.expect("POST", "/api/v1/admins", map[string]string{"username": "kim", "role": "readonly", "password": testPassword}, 201)
	b.expect("POST", "/api/v1/admins", map[string]string{"username": "kim", "password": testPassword}, 409)
	kim := a.browser()
	kim.login("kim")
	kim.expect("POST", "/api/v1/admins", map[string]string{"username": "eve", "password": testPassword}, 403) // readonly

	// promote kim and reset her password: her session ends
	b.expect("PATCH", "/api/v1/admins/kim", map[string]string{"role": "admin", "password": "new horse battery staple"}, 200)
	kim.expect("GET", "/api/v1/admins", nil, 401)
	run, _ := a.store.Running()
	k := run.Admins[adminIndex(run.Admins, "kim")]
	if k.Role != "admin" || auth.VerifyPassword("new horse battery staple", k.PasswordHash) != nil {
		t.Fatalf("kim %+v", k)
	}
	cand, _ := a.store.Candidate()
	if adminIndex(cand.Admins, "kim") < 0 {
		t.Fatal("the candidate does not have the new admin (a later commit would drop her)")
	}

	// delete: not yourself, not unknown; kim can go
	b.expect("DELETE", "/api/v1/admins/admin", nil, 409)
	b.expect("DELETE", "/api/v1/admins/nobody", nil, 404)
	b.expect("DELETE", "/api/v1/admins/kim", nil, 200)
}

func TestLastAdminIsKept(t *testing.T) {
	a := newAuthAPI(t)
	b := a.browser()
	b.login("admin")
	// admin is the only one with the admin role: no demotion
	out := b.expect("PATCH", "/api/v1/admins/admin", map[string]string{"role": "readonly"}, 409)
	if !strings.Contains(jsonString(out), "at least one admin") {
		t.Fatalf("%v", out)
	}
	// a second admin makes it possible
	b.expect("POST", "/api/v1/admins", map[string]string{"username": "kim", "password": testPassword}, 201)
	b.expect("PATCH", "/api/v1/admins/admin", map[string]string{"role": "readonly"}, 200)
}

func TestBackupRestoreRoundTrip(t *testing.T) {
	a := newAuthAPI(t)
	b := a.browser()
	b.login("admin")
	run, _ := a.store.Running()
	run.System.Hostname = "backed-up"
	if _, err := a.store.SaveRevision(run, "test", "rename"); err != nil {
		t.Fatal(err)
	}

	resp := b.raw("GET", "/api/v1/system/backup", "", nil)
	if resp.code != 200 || !strings.Contains(resp.header.Get("Content-Disposition"), "goblin-gate-backed-up-") {
		t.Fatalf("backup: %d %v", resp.code, resp.header)
	}
	zr, err := zip.NewReader(bytes.NewReader(resp.body), int64(len(resp.body)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, want := range []string{"manifest.json", "running.json", "history/index.json", "history/000001.json"} {
		if !names[want] {
			t.Errorf("backup lacks %s (has %v)", want, names)
		}
	}

	// change the box, then restore: the backup becomes the candidate, nothing is applied
	run.System.Hostname = "changed"
	if _, err := a.store.SaveRevision(run, "test", "rename again"); err != nil {
		t.Fatal(err)
	}
	calls := a.applier.Calls()
	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	fw, _ := mw.CreateFormFile("backup", "b.zip")
	fw.Write(resp.body)
	mw.Close()
	r2 := b.raw("POST", "/api/v1/system/restore", mw.FormDataContentType(), form.Bytes())
	if r2.code != 200 {
		t.Fatalf("restore: %d %s", r2.code, r2.body)
	}
	cand, _ := a.store.Candidate()
	if cand.System.Hostname != "backed-up" {
		t.Fatalf("candidate hostname %q", cand.System.Hostname)
	}
	if a.applier.Calls() != calls {
		t.Fatal("restore applied the config")
	}

	// a raw zip body works too; junk is refused
	if r3 := b.raw("POST", "/api/v1/system/restore", "application/zip", resp.body); r3.code != 200 {
		t.Fatalf("raw restore: %d %s", r3.code, r3.body)
	}
	if r4 := b.raw("POST", "/api/v1/system/restore", "application/zip", []byte("not a zip")); r4.code != 400 {
		t.Fatalf("junk restore: %d", r4.code)
	}
	// the readonly user can do neither
	v := a.browser()
	v.login("viewer")
	if r5 := v.raw("POST", "/api/v1/system/restore", "application/zip", resp.body); r5.code != 403 {
		t.Fatalf("readonly restore: %d", r5.code)
	}
}

func TestRestoreRejectsAnInvalidConfig(t *testing.T) {
	a := newAuthAPI(t)
	b := a.browser()
	b.login("admin")
	cfg := config.Default()
	cfg.System.Timezone = "Mars/Olympus"
	data, _ := json.Marshal(cfg)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create("running.json")
	f.Write(data)
	zw.Close()
	if r := b.raw("POST", "/api/v1/system/restore", "application/zip", buf.Bytes()); r.code != 422 || !strings.Contains(string(r.body), "system.timezone") {
		t.Fatalf("%d %s", r.code, r.body)
	}
}

func TestSystemInfo(t *testing.T) {
	a := newAuthAPI(t)
	b := a.browser()
	b.login("viewer")
	out := b.expect("GET", "/api/v1/system/info", nil, 200)
	if out["version"] == nil || out["time"] == nil {
		t.Fatalf("%v", out)
	}
}

type rawResponse struct {
	code   int
	header http.Header
	body   []byte
}

// raw sends a non-JSON request with the browser's cookies and CSRF header.
func (b *browser) raw(method, path, contentType string, body []byte) rawResponse {
	b.t.Helper()
	req, _ := http.NewRequest(method, b.base+path, bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if b.csrf != "" {
		req.Header.Set(CSRFHeader, b.csrf)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return rawResponse{resp.StatusCode, resp.Header, data}
}
