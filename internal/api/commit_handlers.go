package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/commit"
	"github.com/networkgoblinlabs/goblin-gate/internal/notify"
)

type commitRequest struct {
	Comment        string `json:"comment,omitempty"`
	ConfirmMinutes int    `json:"confirm_minutes,omitempty"`
}

func (s *Server) commit(w http.ResponseWriter, r *http.Request) {
	var req commitRequest
	if r.ContentLength != 0 && !decodeJSON(w, r, &req) {
		return
	}
	if req.ConfirmMinutes < 0 || req.ConfirmMinutes > 24*60 {
		writeError(w, http.StatusBadRequest, "invalid_request", "confirm_minutes must be between 1 and 1440", nil)
		return
	}
	var (
		rev int
		err error
	)
	if req.ConfirmMinutes > 0 {
		rev, err = s.Commit.CommitConfirm(context.WithoutCancel(r.Context()), s.user(r), req.Comment, time.Duration(req.ConfirmMinutes)*time.Minute)
	} else {
		rev, err = s.Commit.Commit(context.WithoutCancel(r.Context()), s.user(r), req.Comment)
	}
	if err != nil {
		s.commitError(w, err)
		return
	}
	s.audit(r, "commit", "", map[string]any{"revision": rev, "comment": req.Comment, "confirm_minutes": req.ConfirmMinutes})
	msg := "Revision " + strconv.Itoa(rev) + " by " + s.user(r)
	if req.Comment != "" {
		msg += ": " + req.Comment
	}
	s.Notify.Publish(notify.Event{Kind: "commit", Title: "Configuration committed", Message: msg,
		Fields: map[string]string{"revision": strconv.Itoa(rev), "user": s.user(r)}})
	resp := map[string]any{"revision": rev}
	if deadline, ok := s.Commit.PendingConfirm(); ok {
		resp["confirm_deadline"] = deadline.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) commitError(w http.ResponseWriter, err error) {
	var ve *commit.ValidationError
	if errors.As(err, &ve) {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "the config has errors; nothing was applied", ve.Errors)
		return
	}
	s.Logger.Error("api: commit failed", "error", err)
	writeError(w, http.StatusInternalServerError, "commit_failed", "commit failed; the previous config is still running", err.Error())
}

func (s *Server) confirm(w http.ResponseWriter, r *http.Request) {
	if err := s.Commit.Confirm(); err != nil {
		if errors.Is(err, commit.ErrNoPendingConfirm) {
			writeError(w, http.StatusConflict, "nothing_to_confirm", "no commit is waiting for confirmation", nil)
			return
		}
		s.internal(w, "confirm commit", err)
		return
	}
	s.audit(r, "commit_confirmed", "", nil)
	writeJSON(w, http.StatusOK, map[string]any{"status": "confirmed"})
}

func (s *Server) discard(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DiscardCandidate(); err != nil {
		s.internal(w, "discard candidate", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "discarded"})
}

func (s *Server) revisions(w http.ResponseWriter, r *http.Request) {
	revs, err := s.Store.ListRevisions()
	if err != nil {
		s.internal(w, "list revisions", err)
		return
	}
	// newest first, which is what the GUI and CLI show
	out := make([]any, 0, len(revs))
	for i := len(revs) - 1; i >= 0; i-- {
		out = append(out, revs[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"revisions": out})
}

func (s *Server) rollback(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("rev"))
	if err != nil || n < 1 {
		writeError(w, http.StatusBadRequest, "invalid_revision", "revision must be a positive number", nil)
		return
	}
	if _, err := s.Store.Revision(n); err != nil {
		writeError(w, http.StatusNotFound, "revision_not_found", "no such revision", nil)
		return
	}
	rev, err := s.Commit.Rollback(context.WithoutCancel(r.Context()), n, s.user(r))
	if err != nil {
		s.commitError(w, err)
		return
	}
	s.audit(r, "rollback", "", map[string]any{"revision": rev, "rolled_back_to": n})
	s.Notify.Publish(notify.Event{Kind: "rollback", Title: "Configuration rolled back",
		Message: fmt.Sprintf("%s rolled back to revision %d (now revision %d).", s.user(r), n, rev),
		Fields:  map[string]string{"revision": strconv.Itoa(rev), "rolled_back_to": strconv.Itoa(n), "user": s.user(r)}})
	writeJSON(w, http.StatusOK, map[string]any{"revision": rev, "rolled_back_to": n})
}
