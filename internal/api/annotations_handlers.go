package api

import (
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/devices"
)

// Device annotations (capability devices:annotate): a plugin attaches a few
// named fields to devices, such as the switch port a controller reports. The
// device list shows them; nothing matches on them.

// AnnotatingPlugins returns whether a plugin may have its annotations shown:
// enabled in the running config with devices:annotate approved.
func AnnotatingPlugins(cfg *config.Config) func(string) bool {
	ok := map[string]bool{}
	if cfg != nil {
		for _, p := range cfg.Plugins {
			if p.Enabled && slices.Contains(p.Capabilities, "devices:annotate") {
				ok[p.Name] = true
			}
		}
	}
	// RADIUS's own view (switch/AP and port of each device)
	if cfg != nil && cfg.RADIUSActive() {
		ok[RadiusAnnotations] = true
	}
	return func(name string) bool { return ok[name] }
}

// RadiusAnnotations is the annotation source of RADIUS accounting (no plugin
// can have the name: plugin names start with a letter).
const RadiusAnnotations = "@radius"

// GET /api/v1/plugin/devices/annotations → {devices: {mac: {field: value}},
// updated}: the calling plugin's annotations.
func (s *Server) pluginAnnotations(w http.ResponseWriter, r *http.Request) {
	id, _ := pluginFrom(r)
	if s.Annotations == nil {
		unavailable(w, "device annotations")
		return
	}
	writeJSON(w, http.StatusOK, s.Annotations.Get(id.Name))
}

// PUT /api/v1/plugin/devices/annotations {devices: {mac: {field: value}}} →
// {devices, changed}: replaces the plugin's annotations as a whole; an empty
// map clears them.
func (s *Server) pluginSetAnnotations(w http.ResponseWriter, r *http.Request) {
	id, _ := pluginFrom(r)
	if s.Annotations == nil {
		unavailable(w, "device annotations")
		return
	}
	var req struct {
		Devices map[string]devices.Fields `json:"devices"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Devices == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", `want {"devices": {...}} (an empty map clears them)`, nil)
		return
	}
	set, err := devices.CheckAnnotations(req.Devices)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_annotations", err.Error(), nil)
		return
	}
	// the same set again is answered without counting against the limit
	if cur := s.Annotations.Get(id.Name); equalAnnotations(cur.Devices, set) {
		writeJSON(w, http.StatusOK, map[string]any{"devices": len(set), "changed": false})
		return
	}
	if !s.annotationUpdates.allow(id.Name, time.Now()) {
		s.audit(r, "device_annotations_update", "plugin:"+id.Name, map[string]any{"plugin": id.Name, "result": "rate_limited"})
		writeError(w, http.StatusTooManyRequests, "rate_limited", fmt.Sprintf("at most %d annotation updates per hour", MaxLiveUpdatesPerHour), nil)
		return
	}
	changed, err := s.Annotations.Set(id.Name, set)
	if err != nil {
		s.internal(w, "save device annotations", err)
		return
	}
	if changed {
		s.audit(r, "device_annotations_update", "plugin:"+id.Name, map[string]any{"plugin": id.Name, "result": "updated", "devices": len(set)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": len(set), "changed": changed})
}

func equalAnnotations(a, b map[string]devices.Fields) bool {
	if len(a) != len(b) {
		return false
	}
	for mac, f := range a {
		g, ok := b[mac]
		if !ok || len(f) != len(g) {
			return false
		}
		for k, v := range f {
			if g[k] != v {
				return false
			}
		}
	}
	return true
}
