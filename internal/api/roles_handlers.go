package api

import (
	"errors"
	"net/http"
	"slices"

	"github.com/networkgoblinlabs/goblin-gate/internal/auth"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Custom admin roles: a name and the scopes it grants, on top of reading
// everything. Like admins, roles change right away (no commit): they are the
// management plane. A user's role is looked up on every request, so a
// changed role takes effect at once.

// scopeInfo describes a scope for the web's role editor.
type scopeInfo struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

var scopeDescriptions = map[string]string{
	auth.ScopeRead:          "See everything except secrets",
	auth.ScopeWriteFirewall: "Edit objects, policies, NAT, security profiles, feeds and the reverse proxy",
	auth.ScopeWriteNetwork:  "Edit interfaces, VLANs, DHCP, DNS, gateways, routes and VPNs",
	auth.ScopeWriteSystem:   "Edit system settings, notifications, log and flow export, snapshots",
	auth.ScopeActions:       "Block addresses, manage devices, run diagnostics and refreshes",
	auth.ScopeCommit:        "Commit, confirm, discard and roll back changes",
}

type roleInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Scopes      []string `json:"scopes"`
	Builtin     bool     `json:"builtin"`
	Users       []string `json:"users"`
}

// GET /api/v1/roles → {roles: [...], scopes: [{id, description}]}: the
// built-in roles first, then the custom ones, each with the users that have it.
func (s *Server) listRoles(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	users := func(role string) []string {
		out := []string{}
		for _, a := range cfg.Admins {
			if a.Role == role {
				out = append(out, a.Username)
			}
		}
		return out
	}
	roles := []roleInfo{
		{Name: "admin", Description: "Everything", Scopes: slices.Clone(auth.Scopes), Builtin: true, Users: users("admin")},
		{Name: "readonly", Description: "See everything except secrets", Scopes: []string{auth.ScopeRead}, Builtin: true, Users: users("readonly")},
	}
	for _, ro := range cfg.Roles {
		sc, _ := cfg.RoleScopes(ro.Name)
		roles = append(roles, roleInfo{Name: ro.Name, Description: ro.Description, Scopes: sc, Users: users(ro.Name)})
	}
	scopes := []scopeInfo{}
	for _, sc := range auth.Scopes {
		scopes = append(scopes, scopeInfo{sc, scopeDescriptions[sc]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"roles": roles, "scopes": scopes})
}

type roleRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Scopes      []string `json:"scopes"`
}

// roleProblems answers 422 with the role's validation errors.
func roleProblems(w http.ResponseWriter, ro config.Role) bool {
	probs := config.CheckRole(ro)
	if len(probs) == 0 {
		return false
	}
	details := []map[string]string{}
	for _, p := range probs {
		details = append(details, map[string]string{"path": p[0], "message": p[1]})
	}
	writeError(w, http.StatusUnprocessableEntity, "validation_failed", probs[0][1], details)
	return true
}

func roleIndex(roles []config.Role, name string) int {
	for i, ro := range roles {
		if ro.Name == name {
			return i
		}
	}
	return -1
}

func (s *Server) roleError(w http.ResponseWriter, err error) bool {
	var conflict errConflict
	switch {
	case err == nil:
		return false
	case errors.Is(err, errNoRole):
		writeError(w, http.StatusNotFound, "role_not_found", err.Error(), nil)
	case errors.As(err, &conflict):
		writeError(w, http.StatusConflict, "role_conflict", err.Error(), nil)
	default:
		s.internal(w, "update roles", err)
	}
	return true
}

// POST /api/v1/roles {name, description, scopes} → 201 {role}.
func (s *Server) createRole(w http.ResponseWriter, r *http.Request) {
	var req roleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ro := config.Role{Name: req.Name, Description: req.Description, Scopes: req.Scopes}
	if ro.Scopes == nil {
		ro.Scopes = []string{}
	}
	if roleProblems(w, ro) {
		return
	}
	err := s.updateAccess(r, "role "+ro.Name+" added", func(cfg *config.Config) error {
		if roleIndex(cfg.Roles, ro.Name) >= 0 {
			return errConflict("a role named " + ro.Name + " already exists")
		}
		cfg.Roles = append(cfg.Roles, ro)
		return nil
	})
	if s.roleError(w, err) {
		return
	}
	s.audit(r, "role_created", "", map[string]any{"role": ro.Name, "scopes": ro.Scopes})
	writeJSON(w, http.StatusCreated, map[string]any{"role": ro})
}

type roleUpdateRequest struct {
	Description *string   `json:"description"`
	Scopes      *[]string `json:"scopes"`
}

// PATCH /api/v1/roles/{name} {description?, scopes?}: its users get the new
// scopes with their next request.
func (s *Server) updateRole(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req roleUpdateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Description == nil && req.Scopes == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "give a description, scopes or both", nil)
		return
	}
	if auth.BuiltinRole(name) {
		writeError(w, http.StatusConflict, "role_conflict", "the built-in roles can't be changed", nil)
		return
	}
	err := s.updateAccess(r, "role "+name+" changed", func(cfg *config.Config) error {
		i := roleIndex(cfg.Roles, name)
		if i < 0 {
			return errNoRole
		}
		ro := cfg.Roles[i]
		if req.Description != nil {
			ro.Description = *req.Description
		}
		if req.Scopes != nil {
			ro.Scopes = slices.Clone(*req.Scopes)
		}
		if probs := config.CheckRole(ro); len(probs) > 0 {
			return roleInvalid{ro}
		}
		cfg.Roles[i] = ro
		return nil
	})
	var bad roleInvalid
	if errors.As(err, &bad) {
		roleProblems(w, bad.role)
		return
	}
	if s.roleError(w, err) {
		return
	}
	s.audit(r, "role_changed", "", map[string]any{"role": name, "scopes": req.Scopes})
	writeJSON(w, http.StatusOK, map[string]any{"status": "changed"})
}

// DELETE /api/v1/roles/{name}: only a role no user has.
func (s *Server) deleteRole(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if auth.BuiltinRole(name) {
		writeError(w, http.StatusConflict, "role_conflict", "the built-in roles can't be deleted", nil)
		return
	}
	err := s.updateAccess(r, "role "+name+" deleted", func(cfg *config.Config) error {
		i := roleIndex(cfg.Roles, name)
		if i < 0 {
			return errNoRole
		}
		for _, a := range cfg.Admins {
			if a.Role == name {
				return errConflict("role " + name + " is still given to " + a.Username + "; give them another role first")
			}
		}
		if cfg.SSO.DefaultRole == name || slices.ContainsFunc(cfg.SSO.Roles, func(m config.SSORoleMap) bool { return m.Role == name }) {
			return errConflict("single sign-on still gives role " + name + "; change its group mapping first")
		}
		cfg.Roles = append(cfg.Roles[:i], cfg.Roles[i+1:]...)
		return nil
	})
	if s.roleError(w, err) {
		return
	}
	s.audit(r, "role_deleted", "", map[string]any{"role": name})
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted"})
}

var errNoRole = errors.New("no such role")

type roleInvalid struct{ role config.Role }

func (roleInvalid) Error() string { return "invalid role" }
