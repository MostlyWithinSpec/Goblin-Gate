package config

import (
	"net/url"
	"strings"
)

// GeoIP configures the country database behind "country" address objects.
type GeoIP struct {
	// SourceURL replaces the DB-IP download (a mirror, an air-gapped
	// network, tests): an http(s) URL of a gzipped "start,end,country" CSV;
	// "{month}" in it becomes the month (2026-10). "" = DB-IP Lite.
	SourceURL string `json:"source_url,omitempty"`
}

func (v *validator) checkGeoIP() {
	if s := v.cfg.GeoIP.SourceURL; s != "" {
		u, err := url.Parse(s)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || strings.ContainsAny(s, " \t\n") {
			v.add("geoip.source_url", "want an http:// or https:// URL")
		}
	}
}
