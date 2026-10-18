package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/geoip"
)

func TestGeoIPAPI(t *testing.T) {
	a := newTestAPI(t)
	a.expect("GET", "/api/v1/geoip", nil, 503)

	var gzb bytes.Buffer
	zw := gzip.NewWriter(&gzb)
	zw.Write([]byte("203.0.113.0,203.0.113.255,NZ\n2001:db8::,2001:db8::ffff,NZ\n"))
	zw.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(gzb.Bytes()) }))
	defer srv.Close()
	mgr := &geoip.Manager{Dir: t.TempDir(), Client: srv.Client(), RunNFT: func(context.Context, []byte) error { return nil }}
	a = newTestAPIWith(t, func(s *Server) { s.GeoIP = mgr })

	out := a.expect("GET", "/api/v1/geoip", nil, 200)
	st := out["status"].(map[string]any)
	if st["loaded"] != false || st["attribution"] == "" || len(out["countries"].([]any)) < 240 {
		t.Fatalf("status: %v", st)
	}
	if errCode(a.expect("GET", "/api/v1/geoip/lookup?ip=203.0.113.9", nil, 409)) != "no_database" {
		t.Fatal("lookup without a database")
	}
	a.expect("GET", "/api/v1/geoip/lookup?ip=nope", nil, 400)

	cfg, _ := a.store.Running()
	cfg.GeoIP.SourceURL = srv.URL + "/db.csv.gz"
	cfg.Addresses = append(cfg.Addresses, config.Address{Name: "nz", Type: "country", Value: "NZ"})
	if _, err := a.store.SaveRevision(cfg, "test", "geoip"); err != nil {
		t.Fatal(err)
	}
	if st := a.expect("POST", "/api/v1/geoip/refresh", nil, 200); st["loaded"] != true || st["rows"] != float64(2) {
		t.Fatalf("refresh: %v", st)
	}
	if l := a.expect("GET", "/api/v1/geoip/lookup?ip=2001:db8::5", nil, 200); l["country"] != "NZ" || l["name"] != "New Zealand" {
		t.Fatalf("lookup: %v", l)
	}
	if l := a.expect("GET", "/api/v1/geoip/lookup?ip=192.168.1.5", nil, 200); l["country"] != "" {
		t.Fatalf("private lookup: %v", l)
	}
}

func TestGeoIPRefreshAdminsOnly(t *testing.T) {
	a := newAuthAPI(t)
	a.server.GeoIP = &geoip.Manager{Dir: t.TempDir()}
	v := a.browser()
	v.login("viewer")
	v.expect("POST", "/api/v1/geoip/refresh", nil, 403)
}
