package api

import (
	"net/http"
	"testing"
)

func TestSnapshotsLifecycle(t *testing.T) {
	a := newTestAPI(t)

	// empty list
	out := a.expect("GET", "/api/v1/snapshots", nil, 200)
	snaps, _ := out["snapshots"].([]any)
	if len(snaps) != 0 {
		t.Fatalf("expected empty list: %v", snaps)
	}

	// create from running
	a.expect("POST", "/api/v1/snapshots", map[string]string{"name": "pre-upgrade", "comment": "before change", "from": "running"}, 201)
	a.expect("POST", "/api/v1/snapshots", map[string]string{"name": "pre-upgrade", "from": "running"}, 409) // exists
	a.expect("POST", "/api/v1/snapshots", map[string]string{"name": "bad name!", "from": "running"}, 400)   // invalid name
	a.expect("POST", "/api/v1/snapshots", map[string]string{"name": "ok2", "from": "bogus"}, 400)

	// list now has one
	out = a.expect("GET", "/api/v1/snapshots", nil, 200)
	snaps, _ = out["snapshots"].([]any)
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot: %v", snaps)
	}

	// get full snapshot includes config
	full := a.expect("GET", "/api/v1/snapshots/pre-upgrade", nil, 200)
	if _, ok := full["config"]; !ok {
		t.Fatalf("missing config in get: %v", full)
	}
	a.expect("GET", "/api/v1/snapshots/nobody", nil, 404)

	// restore loads into candidate (not committed)
	a.expect("POST", "/api/v1/snapshots/pre-upgrade/restore", nil, 200)
	cand, _ := a.store.Candidate()
	run, _ := a.store.Running()
	if cand.System.Hostname != run.System.Hostname {
		t.Fatalf("restore should load running config into candidate")
	}

	// delete → 204 (no body, so use raw request)
	req, _ := http.NewRequest("DELETE", a.srv.URL+"/api/v1/snapshots/pre-upgrade", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatalf("delete status %d, want 204", resp.StatusCode)
	}

	// gone now
	a.expect("GET", "/api/v1/snapshots/pre-upgrade", nil, 404)
	out = a.expect("GET", "/api/v1/snapshots", nil, 200)
	snaps, _ = out["snapshots"].([]any)
	if len(snaps) != 0 {
		t.Fatalf("expected empty after delete: %v", snaps)
	}
}

func TestSnapshotRestoreUnknown(t *testing.T) {
	a := newTestAPI(t)
	a.expect("POST", "/api/v1/snapshots/ghost/restore", nil, 404)
}
