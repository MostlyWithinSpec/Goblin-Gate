package webfilter

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestDBRoundTrip(t *testing.T) {
	b := NewBuilder()
	b.Add("gambling", "Casino.example.")
	b.Add("adult", "*.nsfw.example")
	b.Add("adult", "casino.example") // a name in two categories
	b.Add("social_networks", "social.example")
	b.Add("adult", "nsfw.example") // duplicate
	b.Add("adult", "bad entry/with/path")
	var buf bytes.Buffer
	if _, err := b.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	db, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if db.Len() != 4 {
		t.Fatalf("entries %d, want 4", db.Len())
	}
	for q, want := range map[string][]string{
		"casino.example":     {"adult", "gambling"},
		"www.casino.example": {"adult", "gambling"},
		"a.b.nsfw.example.":  {"adult"},
		"SOCIAL.example":     {"social_networks"},
		"example":            {},
		"other.example":      {},
		"notsocial.example":  {},
	} {
		if d := cmp.Diff(want, db.Lookup(q)); d != "" {
			t.Errorf("Lookup(%q): %s", q, d)
		}
	}
	if d := cmp.Diff([]string{"gambling", "adult", "social_networks"}, db.Categories()); d != "" {
		t.Error(d)
	}
	var nilDB *DB
	if nilDB.Lookup("x.example") != nil || nilDB.Len() != 0 {
		t.Error("nil DB")
	}
	if _, err := Read(bytes.NewReader([]byte("nope"))); err == nil {
		t.Error("garbage accepted")
	}
}

func TestBuildUT1(t *testing.T) {
	var tgz bytes.Buffer
	gz := gzip.NewWriter(&tgz)
	tw := tar.NewWriter(gz)
	add := func(name, body string, typ byte) {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: typ})
		tw.Write([]byte(body))
	}
	add("blacklists/gambling/domains", "casino.example\n# comment\nbet.example\n", tar.TypeReg)
	add("blacklists/gambling/urls", "casino.example/path\n", tar.TypeReg)
	add("blacklists/phishing/domains", "login-bank.example\n", tar.TypeReg)
	tw.WriteHeader(&tar.Header{Name: "blacklists/porn", Linkname: "adult", Typeflag: tar.TypeSymlink})
	tw.Close()
	gz.Close()

	b := NewBuilder()
	n, err := BuildUT1(&tgz, b)
	if err != nil || n != 2 {
		t.Fatalf("categories %d err %v", n, err)
	}
	var buf bytes.Buffer
	b.WriteTo(&buf)
	db, _ := Read(&buf)
	if got := db.Lookup("www.bet.example"); len(got) != 1 || got[0] != "gambling" {
		t.Fatalf("lookup %v", got)
	}
}

func TestUpdater(t *testing.T) {
	var tgz bytes.Buffer
	gz := gzip.NewWriter(&tgz)
	tw := tar.NewWriter(gz)
	body := "casino.example\n"
	tw.WriteHeader(&tar.Header{Name: "blacklists/gambling/domains", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
	tw.Write([]byte(body))
	tw.Close()
	gz.Close()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("If-Modified-Since") != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Last-Modified", "Sun, 11 Oct 2026 10:00:00 GMT")
		w.Write(tgz.Bytes())
	}))
	defer srv.Close()

	f := &Filter{}
	u := &Updater{URL: srv.URL, Dir: t.TempDir(), Filter: f, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := u.Update(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.DB().Lookup("www.casino.example"); len(got) != 1 {
		t.Fatalf("after update: %v", got)
	}
	if err := u.Update(context.Background()); err != nil || hits != 2 {
		t.Fatalf("second update (304): %v hits %d", err, hits)
	}
	// a restart loads the saved database without downloading
	f2 := &Filter{}
	ctx, cancel := context.WithCancel(context.Background())
	u2 := &Updater{URL: srv.URL, Dir: u.Dir, Filter: f2, Wanted: func() bool { return false }, Logger: u.Logger}
	done := make(chan struct{})
	go func() { u2.Run(ctx); close(done) }()
	for i := 0; i < 100 && f2.DB() == nil; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if f2.DB() == nil || hits != 2 {
		t.Fatalf("restart: db %v hits %d", f2.DB() != nil, hits)
	}
}
