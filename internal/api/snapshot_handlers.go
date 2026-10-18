package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// GET /api/v1/snapshots → {snapshots: [{name, comment, created, created_by, revision}]}
// newest first, no config bodies.
func (s *Server) listSnapshots(w http.ResponseWriter, r *http.Request) {
	infos, err := s.Store.ListSnapshots()
	if err != nil {
		s.internal(w, "list snapshots", err)
		return
	}
	if infos == nil {
		infos = []config.SnapshotInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": infos})
}

type createSnapshotRequest struct {
	Name    string `json:"name"`
	Comment string `json:"comment"`
	From    string `json:"from"` // "running" or "candidate"
}

// POST /api/v1/snapshots {name, comment, from} → 201 with the snapshot summary.
func (s *Server) createSnapshot(w http.ResponseWriter, r *http.Request) {
	var req createSnapshotRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !config.ValidName(req.Name) {
		writeError(w, http.StatusBadRequest, "invalid_name", "name must be 1-63 letters, digits, \".\", \"_\" or \"-\"", nil)
		return
	}
	var cfg *config.Config
	var revision int
	var err error
	switch req.From {
	case "running":
		cfg, err = s.Store.Running()
		if err == nil {
			revs, rerr := s.Store.ListRevisions()
			if rerr != nil {
				s.internal(w, "list revisions", rerr)
				return
			}
			if len(revs) > 0 {
				revision = revs[len(revs)-1].Revision
			}
		}
	case "candidate":
		cfg, err = s.Store.Candidate()
	default:
		writeError(w, http.StatusBadRequest, "invalid_request", "from must be \"running\" or \"candidate\"", nil)
		return
	}
	if err != nil {
		s.internal(w, "read "+req.From+" config", err)
		return
	}
	if err := s.Store.SaveSnapshot(req.Name, req.Comment, s.user(r), revision, cfg); err != nil {
		switch {
		case errors.Is(err, config.ErrSnapshotExists):
			writeError(w, http.StatusConflict, "exists", "a snapshot named "+req.Name+" already exists", nil)
		case errors.Is(err, config.ErrTooManySnapshots):
			writeError(w, http.StatusBadRequest, "too_many_snapshots", "the limit of 50 snapshots is reached; delete one first", nil)
		default:
			s.internal(w, "save snapshot", err)
		}
		return
	}
	s.audit(r, "snapshot_saved", req.Name, map[string]any{"from": req.From})
	info := config.SnapshotInfo{Name: req.Name, Comment: req.Comment, CreatedBy: s.user(r), Revision: revision,
		Created: time.Now().UTC().Format(time.RFC3339)}
	if snap, err := s.Store.GetSnapshot(req.Name); err == nil {
		info.Created = snap.Created // the stored time, so the list shows the same
	}
	writeJSON(w, http.StatusCreated, info)
}

// GET /api/v1/snapshots/{name} → the summary plus the full config.
func (s *Server) getSnapshot(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	snap, err := s.Store.GetSnapshot(name)
	if err != nil {
		if errors.Is(err, config.ErrSnapshotNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no snapshot named "+name, nil)
			return
		}
		s.internal(w, "read snapshot", err)
		return
	}
	if snap.Config != nil && !s.seesSecrets(r) {
		redactSecrets(snap.Config)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":       snap.Name,
		"comment":    snap.Comment,
		"created":    snap.Created,
		"created_by": snap.CreatedBy,
		"revision":   snap.Revision,
		"config":     snap.Config,
	})
}

// POST /api/v1/snapshots/{name}/restore → loads the snapshot into the
// candidate (never commits) and returns how many changes that is against the
// running config.
func (s *Server) restoreSnapshot(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	snap, err := s.Store.GetSnapshot(name)
	if err != nil {
		if errors.Is(err, config.ErrSnapshotNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no snapshot named "+name, nil)
			return
		}
		s.internal(w, "read snapshot", err)
		return
	}
	if snap.Config == nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_snapshot", "snapshot "+name+" has no config", nil)
		return
	}
	if err := s.Store.SetCandidate(snap.Config); err != nil {
		s.internal(w, "restore snapshot into candidate", err)
		return
	}
	running, _ := s.Store.Running()
	changes := config.Diff(running, snap.Config)
	s.audit(r, "snapshot_restored", name, map[string]any{"changes": len(changes)})
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "changes": len(changes)})
}

// DELETE /api/v1/snapshots/{name} → 204.
func (s *Server) deleteSnapshot(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.Store.DeleteSnapshot(name); err != nil {
		if errors.Is(err, config.ErrSnapshotNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no snapshot named "+name, nil)
			return
		}
		s.internal(w, "delete snapshot", err)
		return
	}
	s.audit(r, "snapshot_deleted", name, nil)
	w.WriteHeader(http.StatusNoContent)
}
