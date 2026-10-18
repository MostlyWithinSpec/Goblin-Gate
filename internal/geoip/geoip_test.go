package geoip

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

const sample = `1.0.0.0,1.0.0.255,AU
"1.0.1.0","1.0.3.255","CN"
10.0.0.0,10.255.255.255,ZZ
203.0.113.0,203.0.113.255,NZ
2001:db8::,2001:db8:ffff:ffff:ffff:ffff:ffff:ffff,NZ
2001:db9::,2001:db9::ffff,AU
`

func gz(s string) []byte {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	w.Write([]byte(s))
	w.Close()
	return b.Bytes()
}

func TestParseAndLookup(t *testing.T) {
	db, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if db.Rows() != 5 { // ZZ (reserved) is skipped
		t.Fatalf("rows %d", db.Rows())
	}
	for addr, want := range map[string]string{
		"1.0.0.7": "AU", "1.0.2.1": "CN", "1.0.4.0": "", "10.1.1.1": "", "203.0.113.200": "NZ",
		"2001:db8::1": "NZ", "2001:db9::fffe": "AU", "2001:dba::": "", "::ffff:203.0.113.1": "NZ",
	} {
		if got := db.Lookup(netip.MustParseAddr(addr)); got != want {
			t.Errorf("%s: %q, want %q", addr, got, want)
		}
	}
	r := db.Ranges("AU")
	if len(r) != 2 || r[0].Lo.String() != "1.0.0.0" || r[1].Hi.String() != "2001:db9::ffff" {
		t.Fatalf("AU ranges %v", r)
	}
	if db.Ranges("FR") != nil || db.Count()["NZ"] != 2 {
		t.Fatal("ranges/count")
	}
	for _, bad := range []string{"1.0.0.0,AU\n", "1.0.0.9,1.0.0.1,AU\n", "1.0.0.0,::1,AU\n", "1.0.0.0,1.0.0.9,AU\n1.0.0.5,1.0.0.20,NZ\n"} {
		if _, err := Parse(strings.NewReader(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func testManager(t *testing.T, srv *httptest.Server, now time.Time) (*Manager, *[][]byte) {
	var scripts [][]byte
	m := &Manager{Dir: t.TempDir(), Client: srv.Client(), Now: func() time.Time { return now },
		RunNFT: func(_ context.Context, s []byte) error { scripts = append(scripts, s); return nil }}
	return m, &scripts
}

func geoCfg(source string) *config.Config {
	cfg := config.Default()
	cfg.GeoIP.SourceURL = source
	cfg.Addresses = []config.Address{{Name: "nz", Type: "country", Value: "NZ"}}
	cfg.Policies = []config.Policy{{ID: 1, Name: "no-nz", Enabled: true, SrcIntf: []string{"lan"}, DstIntf: []string{"wan1"}, SrcAddr: []string{"all"}, DstAddr: []string{"nz"}, Service: []string{"any"}, Action: "deny"}}
	return cfg
}

func TestRefreshFallsBackAndFills(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/db-2026-09.csv.gz": // October's is not out yet
			w.Write(gz(sample))
		case "/broken-2026-10.csv.gz":
			w.Write([]byte("not gzip"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	m, scripts := testManager(t, srv, time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC))
	cfg := geoCfg(srv.URL + "/db-{month}.csv.gz?token=secret")

	m.Resync(cfg) // no database yet: nothing to load
	if len(*scripts) != 0 {
		t.Fatal("loaded sets without a database")
	}
	if err := m.Ensure(context.Background(), cfg.GeoIP.SourceURL); err != nil {
		t.Fatal(err)
	}
	st := m.Status(cfg)
	if !st.Loaded || st.Month != "2026-09" || st.Rows != 5 || strings.Contains(st.Source, "secret") || len(st.Used) != 1 || st.Used[0] != "NZ" {
		t.Fatalf("status %+v", st)
	}
	if len(*scripts) != 1 || !strings.Contains(string((*scripts)[0]), "add element inet goblin addr_nz { 203.0.113.0/24 }") || !strings.Contains(string((*scripts)[0]), "addr6_nz { 2001:db8::/32 }") {
		t.Fatalf("scripts %q", *scripts)
	}
	// later in the day: September is still the newest, nothing is fetched again
	before := hits.Load()
	if err := m.Refresh(context.Background(), cfg.GeoIP.SourceURL); err != nil || hits.Load() != before+1 {
		t.Fatalf("refresh: %v, %d new requests", err, hits.Load()-before)
	}
	// it survives a restart
	m2, _ := testManager(t, srv, time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC))
	m2.Dir = m.Dir
	if err := m2.Load(); err != nil || m2.Lookup(netip.MustParseAddr("203.0.113.5")) != "NZ" {
		t.Fatalf("load: %v", err)
	}
	// a broken file keeps the old database and reports the error
	if err := m.Refresh(context.Background(), srv.URL+"/broken-{month}.csv.gz"); err == nil || !m.Loaded() || m.Status(nil).LastError == "" {
		t.Fatalf("broken: %v", err)
	}
}

func TestRunOnlyWhenUsed(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write(gz(sample))
	}))
	defer srv.Close()
	m, _ := testManager(t, srv, time.Date(2026, 10, 18, 8, 0, 0, 0, time.UTC))
	unused := config.Default()
	unused.GeoIP.SourceURL = srv.URL + "/db.csv.gz"
	m.check(context.Background(), unused)
	if hits.Load() != 0 {
		t.Fatal("downloaded without any country in use")
	}
	m.check(context.Background(), geoCfg(srv.URL+"/db.csv.gz"))
	if hits.Load() != 1 || !m.Loaded() {
		t.Fatalf("not downloaded when used: %d", hits.Load())
	}
	m.check(context.Background(), geoCfg(srv.URL+"/db.csv.gz"))
	if hits.Load() != 1 {
		t.Fatal("downloaded again within the month")
	}
}

func TestDefaultSourceNeedsRealSize(t *testing.T) {
	m := &Manager{Now: func() time.Time { return time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) }}
	u := m.sourceURLs("")
	if len(u) != 2 || u[0].url != "https://download.db-ip.com/free/dbip-country-lite-2026-10.csv.gz" || u[1].month != "2026-09" {
		t.Fatalf("urls %+v", u)
	}
	if got := redactURL("https://user:pw@mirror.example/x.csv.gz?key=abc"); strings.Contains(got, "pw") || strings.Contains(got, "abc") {
		t.Fatalf("redact %q", got)
	}
}
