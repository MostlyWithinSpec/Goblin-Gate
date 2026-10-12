package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

// MaxUIBlob limits a stored UI document (dashboards layout).
const MaxUIBlob = 256 << 10

// UIStore keeps small per-user JSON documents for the web UI (dashboard
// layouts) in Dir, outside the firewall config.
type UIStore struct {
	Dir string // data_dir/ui

	mu sync.Mutex
}

var uiUserRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,63}$`)

func (u *UIStore) path(user, doc string) (string, bool) {
	if !uiUserRe.MatchString(user) {
		return "", false
	}
	return filepath.Join(u.Dir, doc+"-"+user+".json"), true
}

// GET /api/v1/ui/dashboards → the user's stored JSON document, or
// {"dashboards": []} when none is stored yet.
func (s *Server) getDashboards(w http.ResponseWriter, r *http.Request) {
	if s.UI == nil {
		unavailable(w, "UI storage")
		return
	}
	p, ok := s.UI.path(s.user(r), "dashboards")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_user", "cannot store UI data for this user name", nil)
		return
	}
	s.UI.mu.Lock()
	b, err := os.ReadFile(p)
	s.UI.mu.Unlock()
	switch {
	case errors.Is(err, fs.ErrNotExist):
		writeJSON(w, http.StatusOK, map[string]any{"dashboards": []any{}})
	case err != nil:
		s.internal(w, "read dashboards", err)
	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(b)
	}
}

// PUT /api/v1/ui/dashboards stores any JSON object (max 256 KB) for the
// user. The web client owns its shape (web/src/api/stats.ts Dashboards).
func (s *Server) putDashboards(w http.ResponseWriter, r *http.Request) {
	if s.UI == nil {
		unavailable(w, "UI storage")
		return
	}
	p, ok := s.UI.path(s.user(r), "dashboards")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_user", "cannot store UI data for this user name", nil)
		return
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, MaxUIBlob+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "could not read the body", nil)
		return
	}
	if len(b) > MaxUIBlob {
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "dashboards are limited to 256 KB", nil)
		return
	}
	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "body must be a JSON object", err.Error())
		return
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, b); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "body must be a JSON object", err.Error())
		return
	}
	s.UI.mu.Lock()
	defer s.UI.mu.Unlock()
	if err := os.MkdirAll(s.UI.Dir, 0o750); err != nil {
		s.internal(w, "store dashboards", err)
		return
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, compact.Bytes(), 0o640); err != nil {
		s.internal(w, "store dashboards", err)
		return
	}
	if err := os.Rename(tmp, p); err != nil {
		s.internal(w, "store dashboards", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "saved"})
}
