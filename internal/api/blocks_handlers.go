package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/blocks"
	"github.com/networkgoblinlabs/goblin-gate/internal/notify"
)

// Temporary blocks (internal/blocks): admins and plugins with blocks:write
// add expiring blocks; protected targets are refused, each requester is
// rate limited and every change is audited.

// blockView is a block with its remaining time.
type blockView struct {
	blocks.Block
	ExpiresIn int `json:"expires_in"` // seconds
}

func viewBlock(b blocks.Block, now time.Time) blockView {
	return blockView{Block: b, ExpiresIn: int(b.Until.Sub(now).Round(time.Second) / time.Second)}
}

// GET /api/v1/blocks → {blocks: [blockView]}, soonest to expire first.
func (s *Server) listBlocks(w http.ResponseWriter, r *http.Request) {
	if s.Blocks == nil {
		unavailable(w, "temporary blocks")
		return
	}
	now := time.Now()
	out := []blockView{}
	for _, b := range s.Blocks.List() {
		out = append(out, viewBlock(b, now))
	}
	writeJSON(w, http.StatusOK, map[string]any{"blocks": out})
}

// POST /api/v1/blocks {ip, minutes, reason, allow_internal?} → 201 blockView.
// allow_internal (admins only) permits addresses inside the box's own
// networks.
func (s *Server) addBlock(w http.ResponseWriter, r *http.Request) {
	if s.Blocks == nil {
		unavailable(w, "temporary blocks")
		return
	}
	var req struct {
		IP            string `json:"ip"`
		Minutes       int    `json:"minutes"`
		Reason        string `json:"reason"`
		AllowInternal bool   `json:"allow_internal"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(req.IP))
	if err != nil || ip.Zone() != "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "ip must be a single IPv4 or IPv6 address", nil)
		return
	}
	ip = ip.Unmap()
	req.Reason = strings.TrimSpace(req.Reason)
	if req.Minutes < 1 || req.Minutes > blocks.MaxMinutes || req.Reason == "" || len(req.Reason) > 500 {
		writeError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("want minutes 1-%d and a reason (at most 500 characters)", blocks.MaxMinutes), nil)
		return
	}
	cfg, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	b := blocks.Block{IP: ip, Reason: req.Reason}
	check := blocks.Check{}
	limit := blocks.AdminRate
	actor := s.user(r)
	if plug, ok := pluginFrom(r); ok {
		actor = "plugin:" + plug.Name
		if req.AllowInternal {
			writeError(w, http.StatusForbidden, "scope_denied", "plugins may not block internal addresses", nil)
			return
		}
		b.Source, b.CreatedBy, limit = "plugin:"+plug.Name, plug.Name, blocks.PluginRate
	} else {
		b.Source, b.CreatedBy = "admin", s.user(r)
		check.AllowInternal = req.AllowInternal
		if a, err := netip.ParseAddr(clientAddr(r)); err == nil {
			check.Requester = a
		}
	}
	if s.LiveAddrs != nil {
		check.Live = s.LiveAddrs()
	}
	detail := map[string]any{"ip": ip.String(), "minutes": req.Minutes, "reason": req.Reason, "source": b.Source}
	if why := blocks.Protected(cfg, ip, check); why != "" {
		detail["result"] = "refused: " + why
		s.audit(r, "block_add", actor, detail)
		writeError(w, http.StatusForbidden, "protected_target", ip.String()+" cannot be blocked: "+why, nil)
		return
	}
	added, err := s.Blocks.Add(r.Context(), b, time.Duration(req.Minutes)*time.Minute, limit)
	switch {
	case errors.Is(err, blocks.ErrRateLimited), errors.Is(err, blocks.ErrTooMany):
		detail["result"] = "refused: " + err.Error()
		s.audit(r, "block_add", actor, detail)
		writeError(w, http.StatusTooManyRequests, "rate_limited", err.Error(), nil)
		return
	case err != nil:
		s.internal(w, "add the block", err)
		return
	}
	detail["result"] = "added"
	s.audit(r, "block_add", actor, detail)
	if s.Notify != nil {
		s.Notify.Publish(notify.Event{Kind: "block_added", Key: ip.String(), Time: time.Now(),
			Title:   fmt.Sprintf("Blocked %s for %d minutes", ip, req.Minutes),
			Message: fmt.Sprintf("%s (by %s)", req.Reason, b.Source),
			Fields:  map[string]string{"ip": ip.String(), "minutes": fmt.Sprint(req.Minutes), "reason": req.Reason, "source": b.Source}})
	}
	writeJSON(w, http.StatusCreated, viewBlock(added, time.Now()))
}

// DELETE /api/v1/blocks/{ip} lifts a block. A plugin may lift only its
// own blocks.
func (s *Server) removeBlock(w http.ResponseWriter, r *http.Request) {
	if s.Blocks == nil {
		unavailable(w, "temporary blocks")
		return
	}
	ip, err := netip.ParseAddr(r.PathValue("ip"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "not an IP address", nil)
		return
	}
	ip = ip.Unmap()
	who := s.user(r)
	if plug, ok := pluginFrom(r); ok {
		who = "plugin:" + plug.Name
		if b, found := s.Blocks.Get(ip); found && b.Source != "plugin:"+plug.Name {
			writeError(w, http.StatusForbidden, "scope_denied", "plugins may lift only their own blocks", nil)
			return
		}
	}
	removed, err := s.Blocks.Remove(r.Context(), ip)
	if err != nil {
		s.internal(w, "lift the block", err)
		return
	}
	if !removed {
		writeError(w, http.StatusNotFound, "not_found", ip.String()+" is not blocked", nil)
		return
	}
	s.audit(r, "block_remove", who, map[string]any{"ip": ip.String()})
	w.WriteHeader(http.StatusNoContent)
}
