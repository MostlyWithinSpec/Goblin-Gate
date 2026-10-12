package api

import (
	"net/http"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/appctrl"
	"github.com/networkgoblinlabs/goblin-gate/internal/webfilter"
)

// GET /api/v1/webfilter/categories → {categories: [names], entries: n}
// (empty until the category database has been downloaded).
func (s *Server) webfilterCategories(w http.ResponseWriter, r *http.Request) {
	if s.WebFilter == nil {
		unavailable(w, "the web filter")
		return
	}
	db := s.WebFilter.DB()
	cats := db.Categories()
	if cats == nil {
		cats = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": cats, "entries": db.Len()})
}

// GET /api/v1/webfilter/lookup?q=example.com → {domain, categories,
// profiles: {name: {action, category, match}}}: the name's categories and
// what each web filter profile of the running config does with it.
func (s *Server) webfilterLookup(w http.ResponseWriter, r *http.Request) {
	if s.WebFilter == nil {
		unavailable(w, "the web filter")
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" || len(q) > 253 {
		writeError(w, http.StatusBadRequest, "invalid_query", "q must be a domain name", nil)
		return
	}
	cfg, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	db := s.WebFilter.DB()
	cats := db.Lookup(q)
	if cats == nil {
		cats = []string{}
	}
	profiles := map[string]webfilter.Decision{}
	for _, p := range cfg.Profiles.WebFilter {
		profiles[p.Name] = webfilter.Compile(p).Decide(db, q)
	}
	writeJSON(w, http.StatusOK, map[string]any{"domain": strings.ToLower(q), "categories": cats, "profiles": profiles})
}

// GET /api/v1/appctrl/apps → {apps: [{id, name, group, domains}]}: the
// applications application control profiles can name (task 035).
func (s *Server) appctrlApps(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"apps": appctrl.Catalog()})
}
