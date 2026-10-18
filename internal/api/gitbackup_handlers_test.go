package api

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/gitbackup"
)

func TestGitBackupAPI(t *testing.T) {
	a := newTestAPI(t)
	a.expect("GET", "/api/v1/git-backup", nil, 503)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	a = newTestAPIWith(t, func(s *Server) { s.GitBackup = &gitbackup.Manager{Dir: t.TempDir(), Store: s.Store} })
	st := a.expect("GET", "/api/v1/git-backup", nil, 200)
	if st["enabled"] != false || st["branch"] != "main" || !strings.HasPrefix(st["public_key"].(string), "ssh-ed25519 ") {
		t.Fatalf("status: %v", st)
	}
	if out := a.expect("POST", "/api/v1/git-backup/push", nil, 502); errCode(out) != "push_failed" {
		t.Fatalf("push without a remote: %v", out)
	}
	a.expect("POST", "/api/v1/git-backup/forget-host-key", nil, 200)
}

func TestGitBackupWritesAdminsOnly(t *testing.T) {
	a := newAuthAPI(t)
	a.server.GitBackup = &gitbackup.Manager{Dir: t.TempDir(), Store: a.server.Store}
	v := a.browser()
	v.login("viewer")
	v.expect("POST", "/api/v1/git-backup/push", nil, 403)
	v.expect("POST", "/api/v1/git-backup/forget-host-key", nil, 403)
}
