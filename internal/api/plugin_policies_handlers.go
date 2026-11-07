package api

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/commit"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Policy switches for plugins (capability policies:toggle): a plugin may
// turn on and off only the policies the admin listed in its config entry
// (config.Plugin.Policies), each switch is a commit of just that change,
// audited, and limited per plugin.

// MaxPolicySwitchesPerHour limits how often one plugin may switch policies.
const MaxPolicySwitchesPerHour = 60

// switchLimiter counts a plugin's switches over the last hour.
type switchLimiter struct {
	mu   sync.Mutex
	seen map[string][]time.Time
}

func (l *switchLimiter) allow(name string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = map[string][]time.Time{}
	}
	recent := slices.DeleteFunc(l.seen[name], func(t time.Time) bool { return now.Sub(t) >= time.Hour })
	if len(recent) >= MaxPolicySwitchesPerHour {
		l.seen[name] = recent
		return false
	}
	l.seen[name] = append(recent, now)
	return true
}

// PluginPolicy is one policy a plugin may switch.
type PluginPolicy struct {
	ID      uint32 `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// switchable returns the running config's policies the plugin may switch.
func (s *Server) switchable(name string) ([]PluginPolicy, error) {
	cfg, err := s.Store.Running()
	if err != nil {
		return nil, err
	}
	var ids []uint32
	for _, p := range cfg.Plugins {
		if p.Name == name && p.Enabled {
			ids = p.Policies
		}
	}
	out := []PluginPolicy{}
	for _, id := range ids {
		if i := slices.IndexFunc(cfg.Policies, func(p config.Policy) bool { return p.ID == id }); i >= 0 {
			out = append(out, PluginPolicy{ID: id, Name: cfg.Policies[i].Name, Enabled: cfg.Policies[i].Enabled})
		}
	}
	return out, nil
}

// GET /api/v1/plugin/policies → {policies: [{id, name, enabled}]}: the
// policies the calling plugin may switch, as they are running now.
func (s *Server) pluginPolicies(w http.ResponseWriter, r *http.Request) {
	id, _ := pluginFrom(r)
	list, err := s.switchable(id.Name)
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policies": list})
}

// PUT /api/v1/plugin/policies/{id} {enabled} → {id, name, enabled, changed,
// revision?}: switches one of the plugin's policies and commits it.
func (s *Server) pluginSwitchPolicy(w http.ResponseWriter, r *http.Request) {
	id, _ := pluginFrom(r)
	n, err := strconv.ParseUint(r.PathValue("id"), 10, 32)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "the policy id must be a number", nil)
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Enabled == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "want {\"enabled\": true|false}", nil)
		return
	}
	list, err := s.switchable(id.Name)
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	i := slices.IndexFunc(list, func(p PluginPolicy) bool { return p.ID == uint32(n) })
	user := "plugin:" + id.Name
	detail := map[string]any{"plugin": id.Name, "policy": n, "enabled": *req.Enabled}
	if i < 0 {
		detail["result"] = "not_allowed"
		s.audit(r, "policy_switch", user, detail)
		writeError(w, http.StatusForbidden, "scope_denied", fmt.Sprintf("plugin %s may not switch policy %d", id.Name, n), nil)
		return
	}
	pol := list[i]
	if pol.Enabled == *req.Enabled {
		writeJSON(w, http.StatusOK, map[string]any{"id": pol.ID, "name": pol.Name, "enabled": pol.Enabled, "changed": false})
		return
	}
	if s.Commit == nil {
		unavailable(w, "commits")
		return
	}
	if !s.policySwitches.allow(id.Name, time.Now()) {
		detail["result"] = "rate_limited"
		s.audit(r, "policy_switch", user, detail)
		writeError(w, http.StatusTooManyRequests, "rate_limited", fmt.Sprintf("at most %d policy switches per hour", MaxPolicySwitchesPerHour), nil)
		return
	}
	state := "off"
	if *req.Enabled {
		state = "on"
	}
	rev, changed, err := s.Commit.SetPolicyEnabled(r.Context(), pol.ID, *req.Enabled, user, fmt.Sprintf("%s turned policy %q %s", id.Name, pol.Name, state))
	switch {
	case errors.Is(err, commit.ErrConfirmPending):
		detail["result"] = "confirm_pending"
		s.audit(r, "policy_switch", user, detail)
		writeError(w, http.StatusConflict, "confirm_pending", "a commit is waiting to be confirmed; try again after it", nil)
		return
	case errors.Is(err, commit.ErrNoPolicy):
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("no policy %d", n), nil)
		return
	case err != nil:
		detail["result"] = "failed"
		detail["error"] = err.Error()
		s.audit(r, "policy_switch", user, detail)
		writeError(w, http.StatusInternalServerError, "commit_failed", "the switch could not be applied", err.Error())
		return
	}
	detail["result"] = "switched"
	detail["revision"] = rev
	s.audit(r, "policy_switch", user, detail)
	out := map[string]any{"id": pol.ID, "name": pol.Name, "enabled": *req.Enabled, "changed": changed}
	if changed {
		out["revision"] = rev
	}
	writeJSON(w, http.StatusOK, out)
}
