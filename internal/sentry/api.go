package sentry

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

// Handler is Sentry's API for the admin UI, served on the plugin's API
// socket; goblind proxies /api/v1/plugins/sentry/api/<path> to it after
// authenticating the admin (X-Goblin-User). Read-only admins only reach
// GET. All bodies are JSON.
//
//	GET  /status                       Status
//	GET  /incidents?status=&limit=     {incidents: [Incident]} newest first
//	GET  /incidents/{id}               {incident, decisions: [Decision]}
//	POST /incidents/{id}/answer        {answer: block|ignore|approve|always, minutes?} → {incident, decision}
//	GET  /decisions?limit=             {decisions: [Decision]} newest first
//	GET  /rules                        {rules: [Rule]}
//	PUT  /rules                        {rules: [Rule]} → {rules} | 400 {error, problems}
func Handler(e *Engine) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, e.Status())
	})
	mux.HandleFunc("GET /incidents", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, map[string]any{"incidents": e.Incidents(r.URL.Query().Get("status"), limit(r, 200))})
	})
	mux.HandleFunc("GET /incidents/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		inc, ds, ok := e.Incident(id)
		if err != nil || !ok {
			fail(w, http.StatusNotFound, "no such incident")
			return
		}
		reply(w, http.StatusOK, map[string]any{"incident": inc, "decisions": ds})
	})
	mux.HandleFunc("POST /incidents/{id}/answer", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			fail(w, http.StatusNotFound, "no such incident")
			return
		}
		var req struct {
			Answer  string `json:"answer"`
			Minutes int    `json:"minutes"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
			fail(w, http.StatusBadRequest, "body: "+err.Error())
			return
		}
		user := r.Header.Get("X-Goblin-User")
		if user == "" {
			user = "admin"
		}
		inc, d, err := e.Answer(r.Context(), id, req.Answer, user, req.Minutes)
		switch {
		case errors.Is(err, ErrNotFound):
			fail(w, http.StatusNotFound, err.Error())
		case err != nil:
			fail(w, http.StatusBadRequest, err.Error())
		default:
			reply(w, http.StatusOK, map[string]any{"incident": inc, "decision": d})
		}
	})
	mux.HandleFunc("GET /decisions", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, map[string]any{"decisions": e.Decisions(limit(r, 200))})
	})
	mux.HandleFunc("GET /rules", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, map[string]any{"rules": e.Rules()})
	})
	mux.HandleFunc("PUT /rules", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Rules []Rule `json:"rules"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			fail(w, http.StatusBadRequest, "body: "+err.Error())
			return
		}
		if problems := e.SetRules(req.Rules); problems != nil {
			reply(w, http.StatusBadRequest, map[string]any{"error": "the rules are not valid", "problems": problems})
			return
		}
		reply(w, http.StatusOK, map[string]any{"rules": e.Rules()})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { fail(w, http.StatusNotFound, "no such endpoint") })
	return mux
}

func limit(r *http.Request, def int) int {
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 5000 {
		return n
	}
	return def
}

func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	reply(w, code, map[string]string{"error": msg})
}
