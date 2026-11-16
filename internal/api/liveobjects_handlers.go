package api

import (
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/liveobjects"
)

// Live address objects (capability objects:write): a plugin replaces the
// members of the live objects the running config gives it; the members go
// straight into nftables (no commit) and are audited.

// MaxLiveUpdatesPerHour limits how often one plugin may update its objects.
const MaxLiveUpdatesPerHour = 360

// LiveObjectView is one live object for the admin UI and its plugin.
type LiveObjectView struct {
	Name    string         `json:"name"`
	Plugin  string         `json:"plugin"`
	Members []netip.Prefix `json:"members"`
	Updated time.Time      `json:"updated,omitzero"`
}

// liveLimiter counts a plugin's updates over the last hour.
type liveLimiter struct {
	mu   sync.Mutex
	seen map[string][]time.Time
}

func (l *liveLimiter) allow(name string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = map[string][]time.Time{}
	}
	recent := l.seen[name][:0]
	for _, t := range l.seen[name] {
		if now.Sub(t) < time.Hour {
			recent = append(recent, t)
		}
	}
	if len(recent) >= MaxLiveUpdatesPerHour {
		l.seen[name] = recent
		return false
	}
	l.seen[name] = append(recent, now)
	return true
}

// liveObjects lists the running config's live objects, those of plugin only
// when plugin is not "".
func (s *Server) liveObjects(plugin string) ([]LiveObjectView, error) {
	cfg, err := s.Store.Running()
	if err != nil {
		return nil, err
	}
	owned := liveobjects.Owned(cfg)
	if plugin != "" {
		for name, p := range owned {
			if p != plugin {
				delete(owned, name)
			}
		}
	}
	out := []LiveObjectView{}
	for _, o := range s.LiveObjects.List(owned) {
		out = append(out, LiveObjectView(o))
	}
	return out, nil
}

// GET /api/v1/live-objects → {objects: [LiveObjectView]}: every live object
// of the running config with its current members.
func (s *Server) listLiveObjects(w http.ResponseWriter, r *http.Request) {
	if s.LiveObjects == nil {
		unavailable(w, "live objects")
		return
	}
	list, err := s.liveObjects("")
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"objects": list})
}

// GET /api/v1/plugin/objects → {objects: [LiveObjectView]}: the calling
// plugin's live objects.
func (s *Server) pluginLiveObjects(w http.ResponseWriter, r *http.Request) {
	id, _ := pluginFrom(r)
	if s.LiveObjects == nil {
		unavailable(w, "live objects")
		return
	}
	list, err := s.liveObjects(id.Name)
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"objects": list})
}

// PUT /api/v1/plugin/objects/{name} {members: ["10.0.0.5", "fd00:1::/64"]}
// → {name, members, added, removed, changed}: replaces the members of one of
// the plugin's live objects and loads them into nftables.
func (s *Server) pluginSetLiveObject(w http.ResponseWriter, r *http.Request) {
	id, _ := pluginFrom(r)
	if s.LiveObjects == nil {
		unavailable(w, "live objects")
		return
	}
	var req struct {
		Members []string `json:"members"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Members == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", `want {"members": [...]} (an empty list clears the object)`, nil)
		return
	}
	name := r.PathValue("name")
	user := "plugin:" + id.Name
	detail := map[string]any{"plugin": id.Name, "object": name}
	cfg, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	if owner, ok := liveobjects.Owned(cfg)[name]; !ok || owner != id.Name {
		detail["result"] = "not_owned"
		s.audit(r, "live_object_update", user, detail)
		writeError(w, http.StatusForbidden, "scope_denied", fmt.Sprintf("plugin %s keeps no live object %q", id.Name, name), nil)
		return
	}
	members, err := liveobjects.ParseMembers(req.Members)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_members", err.Error(), nil)
		return
	}
	// The same members again change nothing: answered without counting
	// against the limit, so a plugin may send its full list on every poll.
	if cur, ok := s.LiveObjects.Get(name); ok && cur.Plugin == id.Name && slices.Equal(cur.Members, members) {
		writeJSON(w, http.StatusOK, map[string]any{"name": name, "members": len(members), "added": 0, "removed": 0, "changed": false})
		return
	}
	if !s.liveUpdates.allow(id.Name, time.Now()) {
		detail["result"] = "rate_limited"
		s.audit(r, "live_object_update", user, detail)
		writeError(w, http.StatusTooManyRequests, "rate_limited", fmt.Sprintf("at most %d live object updates per hour", MaxLiveUpdatesPerHour), nil)
		return
	}
	ch, err := s.LiveObjects.Set(name, id.Name, members)
	if err != nil {
		s.internal(w, "save live object", err)
		return
	}
	if ch.Changed {
		if s.RefillLive != nil {
			s.RefillLive(name)
		}
		detail["result"] = "updated"
		detail["members"], detail["added"], detail["removed"] = ch.Members, ch.Added, ch.Removed
		s.audit(r, "live_object_update", user, detail)
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "members": ch.Members, "added": ch.Added, "removed": ch.Removed, "changed": ch.Changed})
}
