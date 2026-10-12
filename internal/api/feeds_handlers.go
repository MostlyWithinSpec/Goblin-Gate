package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/feeds"
)

func (s *Server) feedsReady(w http.ResponseWriter) bool {
	if s.Feeds == nil {
		writeError(w, http.StatusServiceUnavailable, "feeds_unavailable", "the feed manager is not running", nil)
		return false
	}
	return true
}

// GET /api/v1/feeds → {feeds: [feeds.Status]}
func (s *Server) feedStatus(w http.ResponseWriter, r *http.Request) {
	if !s.feedsReady(w) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"feeds": s.Feeds.Status()})
}

// POST /api/v1/feeds/{name}/refresh downloads the feed now and returns its
// status. A failed download answers 502 with the status (last good copy
// kept) in details.
func (s *Server) feedRefresh(w http.ResponseWriter, r *http.Request) {
	if !s.feedsReady(w) {
		return
	}
	st, err := s.Feeds.Refresh(r.Context(), r.PathValue("name"))
	switch {
	case errors.Is(err, feeds.ErrUnknownFeed):
		writeError(w, http.StatusNotFound, "feed_not_found", "no such feed in the running config", nil)
	case err != nil:
		writeError(w, http.StatusBadGateway, "refresh_failed", err.Error(), st)
	default:
		writeJSON(w, http.StatusOK, st)
	}
}

// GET /api/v1/feeds/lookup?q=<ip|domain|url> → {query, feeds: [names]}
func (s *Server) feedLookup(w http.ResponseWriter, r *http.Request) {
	if !s.feedsReady(w) {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" || len(q) > 2048 {
		writeError(w, http.StatusBadRequest, "invalid_query", "q must be an IP address, domain or URL", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"query": q, "feeds": s.Feeds.Lookup(q)})
}
