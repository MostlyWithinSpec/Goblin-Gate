package api

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func (s *Server) getRunning(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	if !s.seesSecrets(r) {
		redactSecrets(cfg)
	}
	writeJSON(w, http.StatusOK, cfg)
}

// seesSecrets reports whether the caller may read secrets in a config:
// admins yes; read-only users and plugins no (a password hash can be
// cracked offline into an admin password).
func (s *Server) seesSecrets(r *http.Request) bool {
	if _, ok := pluginFrom(r); ok {
		return false
	}
	if s.Authn == nil {
		return true
	}
	id, _ := IdentityFrom(r)
	return id.CanWrite()
}

// redactSecrets blanks what a plugin or read-only user must not see
// (config.RedactSecrets).
func redactSecrets(cfg *config.Config) { config.RedactSecrets(cfg) }

func (s *Server) getCandidate(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.Store.Candidate()
	if err != nil {
		s.internal(w, "read candidate config", err)
		return
	}
	if !s.seesSecrets(r) {
		redactSecrets(cfg)
	}
	writeJSON(w, http.StatusOK, cfg)
}

// candidateResponse is returned by PUT and PATCH: the change is stored even
// when the config is not valid yet (a candidate may be half-edited); the
// problems are listed so the GUI can show them. Commit refuses invalid configs.
type candidateResponse struct {
	Status           string                   `json:"status"`
	ValidationErrors []config.ValidationError `json:"validation_errors"`
}

func (s *Server) putCandidate(w http.ResponseWriter, r *http.Request) {
	var raw json.RawMessage
	if !decodeJSON(w, r, &raw) {
		return
	}
	cfg, err := config.Load(bytes.NewReader(raw))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_config", "body is not a valid config document", err.Error())
		return
	}
	s.storeCandidate(w, cfg)
}

type patchRequest struct {
	Path   string          `json:"path"`
	Value  json.RawMessage `json:"value,omitempty"`
	Delete bool            `json:"delete,omitempty"`
}

func (s *Server) patchCandidate(w http.ResponseWriter, r *http.Request) {
	var req patchRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "invalid_patch", "path is required", nil)
		return
	}
	if req.Delete == (len(req.Value) > 0) {
		writeError(w, http.StatusBadRequest, "invalid_patch", "give exactly one of value or delete:true", nil)
		return
	}
	cfg, err := s.Store.Candidate()
	if err != nil {
		s.internal(w, "read candidate config", err)
		return
	}
	var out *config.Config
	if req.Delete {
		out, err = config.DeletePath(cfg, req.Path)
	} else {
		var value any
		if err = json.Unmarshal(req.Value, &value); err == nil {
			out, err = config.SetPath(cfg, req.Path, value)
		}
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_patch", "could not apply the change", err.Error())
		return
	}
	s.storeCandidate(w, out)
}

func (s *Server) storeCandidate(w http.ResponseWriter, cfg *config.Config) {
	if err := s.Store.SetCandidate(cfg); err != nil {
		s.internal(w, "write candidate config", err)
		return
	}
	errs := config.ValidateCandidate(cfg)
	if errs == nil {
		errs = []config.ValidationError{}
	}
	writeJSON(w, http.StatusOK, candidateResponse{Status: "ok", ValidationErrors: errs})
}

// validate checks the posted config, or the candidate when the body is empty.
func (s *Server) validate(w http.ResponseWriter, r *http.Request) {
	var cfg *config.Config
	if r.ContentLength == 0 {
		c, err := s.Store.Candidate()
		if err != nil {
			s.internal(w, "read candidate config", err)
			return
		}
		cfg = c
	} else {
		var raw json.RawMessage
		if !decodeJSON(w, r, &raw) {
			return
		}
		c, err := config.Load(bytes.NewReader(raw))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_config", "body is not a valid config document", err.Error())
			return
		}
		cfg = c
	}
	errs := config.ValidateCandidate(cfg)
	if errs == nil {
		errs = []config.ValidationError{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": len(errs) == 0, "errors": errs})
}

func (s *Server) diff(w http.ResponseWriter, r *http.Request) {
	running, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	candidate, err := s.Store.Candidate()
	if err != nil {
		s.internal(w, "read candidate config", err)
		return
	}
	if !s.seesSecrets(r) {
		redactSecrets(running)
		redactSecrets(candidate)
	}
	changes := config.Diff(running, candidate)
	if changes == nil {
		changes = []config.Change{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(changes), "changes": changes})
}

func (s *Server) internal(w http.ResponseWriter, what string, err error) {
	s.Logger.Error("api: "+what, "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "could not "+what, nil)
}
