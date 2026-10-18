package api

import "net/http"

func (s *Server) gitBackupReady(w http.ResponseWriter) bool {
	if s.GitBackup == nil {
		writeError(w, http.StatusServiceUnavailable, "git_backup_unavailable", "the git history is not available on this box", nil)
		return false
	}
	return true
}

// GET /api/v1/git-backup → gitbackup.Status for the running config (the
// deploy key is created on the first call, so it can be added to the
// remote before the first commit).
func (s *Server) gitBackupStatus(w http.ResponseWriter, r *http.Request) {
	if !s.gitBackupReady(w) {
		return
	}
	live, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	writeJSON(w, http.StatusOK, s.GitBackup.Status(live))
}

// POST /api/v1/git-backup/push pushes now ("Test push"); 502 push_failed
// with the status in details when the remote refuses.
func (s *Server) gitBackupPush(w http.ResponseWriter, r *http.Request) {
	if !s.gitBackupReady(w) {
		return
	}
	live, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	s.GitBackup.Sync(r.Context(), live)
	if err := s.GitBackup.Push(r.Context()); err != nil {
		writeError(w, http.StatusBadGateway, "push_failed", err.Error(), s.GitBackup.Status(live))
		return
	}
	s.audit(r, "git_backup_push", s.user(r), nil)
	writeJSON(w, http.StatusOK, s.GitBackup.Status(live))
}

// POST /api/v1/git-backup/forget-host-key drops the remote's pinned SSH
// host key; the next push pins the one the server presents then.
func (s *Server) gitBackupForgetHostKey(w http.ResponseWriter, r *http.Request) {
	if !s.gitBackupReady(w) {
		return
	}
	if err := s.GitBackup.ForgetHostKey(); err != nil {
		s.internal(w, "forget host key", err)
		return
	}
	s.audit(r, "git_backup_forget_host_key", s.user(r), nil)
	live, _ := s.Store.Running()
	writeJSON(w, http.StatusOK, s.GitBackup.Status(live))
}
