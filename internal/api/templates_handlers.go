package api

import (
	"errors"
	"net/http"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/templates"
)

// GET /api/v1/templates → {templates: [{id, version, title, description,
// inputs, unavailable?}]}, with the inputs' options taken from the
// candidate (task 110).
func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.Store.Candidate()
	if err != nil {
		s.internal(w, "read candidate config", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": templates.List(cfg)})
}

type templateRequest struct {
	Values map[string]string `json:"values"`
}

// templateResult is what preview answers and apply refuses with.
type templateResult struct {
	Changes          []config.Change          `json:"changes"`
	InputErrors      templates.InputErrors    `json:"input_errors"`
	ValidationErrors []config.ValidationError `json:"validation_errors"`
}

// runTemplate applies the template to a copy of the candidate. It reports
// only the validation errors the template adds: problems the candidate
// already had are not the template's to fix.
func (s *Server) runTemplate(w http.ResponseWriter, r *http.Request) (*config.Config, templateResult, bool) {
	res := templateResult{Changes: []config.Change{}, InputErrors: templates.InputErrors{}, ValidationErrors: []config.ValidationError{}}
	t, ok := templates.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such template", nil)
		return nil, res, false
	}
	var req templateRequest
	if !decodeJSON(w, r, &req) {
		return nil, res, false
	}
	cfg, err := s.Store.Candidate()
	if err != nil {
		s.internal(w, "read candidate config", err)
		return nil, res, false
	}
	out, err := t.Apply(cfg, req.Values)
	var ie templates.InputErrors
	switch {
	case errors.As(err, &ie):
		res.InputErrors = ie
		return nil, res, true
	case err != nil:
		s.internal(w, "apply template", err)
		return nil, res, false
	}
	had := map[config.ValidationError]bool{}
	for _, e := range config.ValidateCandidate(cfg) {
		had[e] = true
	}
	for _, e := range config.ValidateCandidate(out) {
		if !had[e] {
			res.ValidationErrors = append(res.ValidationErrors, e)
		}
	}
	res.Changes = config.Diff(cfg, out)
	if res.Changes == nil {
		res.Changes = []config.Change{}
	}
	return out, res, true
}

// POST /api/v1/templates/{id}/preview {values} → {changes, input_errors,
// validation_errors}; the candidate is not changed.
func (s *Server) previewTemplate(w http.ResponseWriter, r *http.Request) {
	if _, res, ok := s.runTemplate(w, r); ok {
		writeJSON(w, http.StatusOK, res)
	}
}

// POST /api/v1/templates/{id}/apply {values} saves the result to the
// candidate (never commits) → {status, changes}; with input or validation
// errors it answers 400 with the preview body under error.details.
func (s *Server) applyTemplate(w http.ResponseWriter, r *http.Request) {
	out, res, ok := s.runTemplate(w, r)
	if !ok {
		return
	}
	if len(res.InputErrors) > 0 || len(res.ValidationErrors) > 0 {
		writeError(w, http.StatusBadRequest, "template_invalid", "the template's answers need fixing", res)
		return
	}
	if err := s.Store.SetCandidate(out); err != nil {
		s.internal(w, "write candidate config", err)
		return
	}
	s.audit(r, "template.apply", "", map[string]any{"template": r.PathValue("id")})
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "changes": res.Changes})
}
