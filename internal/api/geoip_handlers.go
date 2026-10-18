package api

import (
	"net/http"
	"net/netip"
	"slices"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func (s *Server) geoipReady(w http.ResponseWriter) bool {
	if s.GeoIP == nil {
		writeError(w, http.StatusServiceUnavailable, "geoip_unavailable", "the GeoIP database is not available on this box", nil)
		return false
	}
	return true
}

// GET /api/v1/geoip → geoip.Status plus the country list
// ({code, name}, for pickers).
func (s *Server) geoipStatus(w http.ResponseWriter, r *http.Request) {
	if !s.geoipReady(w) {
		return
	}
	live, _ := s.Store.Running()
	type country struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	countries := make([]country, 0, len(config.Countries))
	for code, name := range config.Countries {
		countries = append(countries, country{code, name})
	}
	slices.SortFunc(countries, func(a, b country) int { return strings.Compare(a.Name, b.Name) })
	writeJSON(w, http.StatusOK, map[string]any{"status": s.GeoIP.Status(live), "countries": countries})
}

// GET /api/v1/geoip/lookup?ip=203.0.113.9 → {ip, country, name}; country
// is "" for an address in no country (private space).
func (s *Server) geoipLookup(w http.ResponseWriter, r *http.Request) {
	if !s.geoipReady(w) {
		return
	}
	a, err := netip.ParseAddr(strings.TrimSpace(r.URL.Query().Get("ip")))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_ip", "ip must be an IPv4 or IPv6 address", nil)
		return
	}
	if !s.GeoIP.Loaded() {
		writeError(w, http.StatusConflict, "no_database", "the GeoIP database is not downloaded yet: use Refresh now, or add a country object", nil)
		return
	}
	c := s.GeoIP.Lookup(a)
	writeJSON(w, http.StatusOK, map[string]any{"ip": a.Unmap().String(), "country": c, "name": config.Countries[c]})
}

// POST /api/v1/geoip/refresh downloads the database now (from the running
// config's geoip.source_url) and returns the status; 502 when it fails.
func (s *Server) geoipRefresh(w http.ResponseWriter, r *http.Request) {
	if !s.geoipReady(w) {
		return
	}
	live, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	if err := s.GeoIP.Refresh(r.Context(), live.GeoIP.SourceURL); err != nil {
		writeError(w, http.StatusBadGateway, "refresh_failed", err.Error(), s.GeoIP.Status(live))
		return
	}
	s.audit(r, "geoip_refresh", s.user(r), nil)
	writeJSON(w, http.StatusOK, s.GeoIP.Status(live))
}
