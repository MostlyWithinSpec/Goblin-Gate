package api

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/logs"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/nft"
)

func unavailable(w http.ResponseWriter, what string) {
	writeError(w, http.StatusServiceUnavailable, "unavailable", what+" is not available", nil)
}

// POST /api/v1/stats/query: body logs.Query → logs.Result.
func (s *Server) statsQuery(w http.ResponseWriter, r *http.Request) {
	if s.Logs == nil {
		unavailable(w, "the log store")
		return
	}
	var q logs.Query
	if !decodeJSON(w, r, &q) {
		return
	}
	res, err := s.Logs.Query(r.Context(), q, time.Now())
	var qe *logs.QueryError
	switch {
	case errors.As(err, &qe):
		writeError(w, http.StatusBadRequest, "invalid_query", qe.Error(), nil)
	case err != nil:
		s.internal(w, "run stats query", err)
	default:
		writeJSON(w, http.StatusOK, res)
	}
}

// GET /api/v1/logs?kind=&q=&from=&to=&limit=&cursor=&<field>= → {events, next_cursor}
// (newest first; <field> is any stats event field except kind, matched
// exactly, e.g. src_ip=192.168.1.5; pass next_cursor back as cursor for the next page, 0 =
// no more).
func (s *Server) listLogs(w http.ResponseWriter, r *http.Request) {
	if s.Logs == nil {
		unavailable(w, "the log store")
		return
	}
	v := r.URL.Query()
	now := time.Now()
	f := logs.EventFilter{Kind: v.Get("kind"), Text: v.Get("q"), Fields: map[string]string{}}
	// any other event field is an exact filter, e.g. ?src_ip=192.168.1.5
	for _, k := range logs.GroupFields("events") {
		if x := v.Get(k); x != "" && k != "kind" {
			f.Fields[k] = x
		}
	}
	var err error
	if x := v.Get("from"); x != "" {
		if f.From, err = logs.ParseTime(x, now); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_query", "from: "+err.Error(), nil)
			return
		}
	}
	if x := v.Get("to"); x != "" {
		if f.To, err = logs.ParseTime(x, now); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_query", "to: "+err.Error(), nil)
			return
		}
	}
	if x := v.Get("cursor"); x != "" {
		if f.Before, err = strconv.ParseInt(x, 10, 64); err != nil || f.Before < 0 {
			writeError(w, http.StatusBadRequest, "invalid_query", "cursor must be a non-negative number", nil)
			return
		}
	}
	if x := v.Get("after"); x != "" {
		if f.After, err = strconv.ParseInt(x, 10, 64); err != nil || f.After < 0 {
			writeError(w, http.StatusBadRequest, "invalid_query", "after must be a non-negative number", nil)
			return
		}
	}
	if x := v.Get("limit"); x != "" {
		if f.Limit, err = strconv.Atoi(x); err != nil || f.Limit < 1 || f.Limit > logs.MaxQueryRows {
			writeError(w, http.StatusBadRequest, "invalid_query", "limit must be between 1 and 10000", nil)
			return
		}
	}
	events, next, err := s.Logs.ListEvents(r.Context(), f)
	if err != nil {
		s.internal(w, "list events", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "next_cursor": next})
}

// GET /api/v1/stats/policies → {policies: [{id, packets, bytes, last_hit}]}
func (s *Server) policyStats(w http.ResponseWriter, r *http.Request) {
	if s.Policies == nil {
		unavailable(w, "policy counters")
		return
	}
	ps, err := s.Policies.Read(r.Context())
	if err != nil {
		s.Logger.Warn("api: read policy counters", "error", err)
		writeError(w, http.StatusServiceUnavailable, "unavailable", "could not read policy counters", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policies": ps})
}

// GET /api/v1/stats/system → stats.System
func (s *Server) systemStats(w http.ResponseWriter, r *http.Request) {
	if s.System == nil {
		unavailable(w, "system stats")
		return
	}
	writeJSON(w, http.StatusOK, s.System.Sample())
}

// FeedHit is one row of GET /api/v1/stats/feeds.
type FeedHit struct {
	Name    string `json:"name"`
	Packets uint64 `json:"packets"`
	Bytes   uint64 `json:"bytes"`
}

// CustomHit is one row of GET /api/v1/stats/custom-rules.
type CustomHit struct {
	Name    string `json:"name"`
	Packets uint64 `json:"packets"`
	Bytes   uint64 `json:"bytes"`
}

// GET /api/v1/stats/custom-rules → {rules: [{name, packets, bytes}]}:
// packets matched per enabled custom rule since the last ruleset load.
func (s *Server) customRuleStats(w http.ResponseWriter, r *http.Request) {
	if s.CustomHits == nil {
		unavailable(w, "custom rule counters")
		return
	}
	cfg, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	counters, err := s.CustomHits.CustomCounters(r.Context())
	if err != nil {
		s.Logger.Warn("api: read custom rule counters", "error", err)
		writeError(w, http.StatusServiceUnavailable, "unavailable", "could not read custom rule counters", err.Error())
		return
	}
	out := []CustomHit{}
	for name, counter := range nft.CustomCounterNames(cfg) {
		c := counters[counter]
		out = append(out, CustomHit{Name: name, Packets: c.Packets, Bytes: c.Bytes})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"rules": out})
}

// GET /api/v1/stats/feeds → {feeds: [{name, packets, bytes}]}: packets
// dropped per enabled IP threat feed since the last ruleset load.
func (s *Server) feedStats(w http.ResponseWriter, r *http.Request) {
	if s.FeedHits == nil {
		unavailable(w, "feed counters")
		return
	}
	cfg, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	counters, err := s.FeedHits.FeedCounters(r.Context())
	if err != nil {
		s.Logger.Warn("api: read feed counters", "error", err)
		writeError(w, http.StatusServiceUnavailable, "unavailable", "could not read feed counters", err.Error())
		return
	}
	_, names := nft.FeedNames(cfg)
	out := []FeedHit{}
	for feed, counter := range names {
		c := counters[counter]
		out = append(out, FeedHit{Name: feed, Packets: c.Packets, Bytes: c.Bytes})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"feeds": out})
}
