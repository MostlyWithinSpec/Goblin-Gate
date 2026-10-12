package auth

import (
	"bufio"
	"crypto/x509"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func newClock() *fakeClock { return &fakeClock{t: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)} }

func TestSessions(t *testing.T) {
	clk := newClock()
	s := &Sessions{Now: clk.now}
	cookie, id := s.Create("admin", "admin")
	if len(cookie) < 40 || id.CSRF == "" || id.CSRF == cookie {
		t.Fatalf("cookie %q csrf %q", cookie, id.CSRF)
	}
	got, ok := s.Lookup(cookie)
	if !ok || got.User != "admin" || !got.CheckCSRF(id.CSRF) || got.CheckCSRF("") || got.CheckCSRF("x") {
		t.Fatalf("lookup %+v %v", got, ok)
	}
	if _, ok := s.Lookup(cookie + "x"); ok {
		t.Fatal("wrong cookie accepted")
	}

	// idle timeout, refreshed by use
	clk.t = clk.t.Add(IdleTimeout - time.Minute)
	if _, ok := s.Lookup(cookie); !ok {
		t.Fatal("expired too early")
	}
	clk.t = clk.t.Add(IdleTimeout + time.Second)
	if _, ok := s.Lookup(cookie); ok {
		t.Fatal("idle session still valid")
	}

	// absolute timeout even when active
	cookie, _ = s.Create("admin", "admin")
	for i := 0; i < int(AbsoluteTimeout/(20*time.Minute))+1; i++ {
		clk.t = clk.t.Add(20 * time.Minute)
		s.Lookup(cookie)
	}
	if _, ok := s.Lookup(cookie); ok {
		t.Fatal("session outlived the absolute timeout")
	}

	// logout and per-user revocation
	c1, id1 := s.Create("a", "admin")
	c2, _ := s.Create("b", "readonly")
	c3, _ := s.Create("b", "readonly")
	s.Delete(id1)
	s.DeleteUser("b")
	for _, c := range []string{c1, c2, c3} {
		if _, ok := s.Lookup(c); ok {
			t.Fatal("deleted session still valid")
		}
	}
}

func TestLimiter(t *testing.T) {
	clk := newClock()
	l := &Limiter{Now: clk.now}
	for i := 1; i < MaxFailures; i++ {
		if l.Fail("1.2.3.4", "admin") {
			t.Fatalf("locked after %d failures", i)
		}
	}
	if ok, _ := l.Allowed("1.2.3.4", "admin"); !ok {
		t.Fatal("blocked before the limit")
	}
	if !l.Fail("1.2.3.4", "admin") {
		t.Fatal("not locked at the limit")
	}
	if ok, wait := l.Allowed("1.2.3.4", "someone"); ok || wait != Lockout {
		t.Fatalf("address not locked: %v %v", ok, wait)
	}
	if ok, _ := l.Allowed("5.6.7.8", "admin"); !ok {
		t.Fatal("another address is locked by one address's failures")
	}
	clk.t = clk.t.Add(Lockout + time.Second)
	if ok, _ := l.Allowed("1.2.3.4", "admin"); !ok {
		t.Fatal("lockout did not end")
	}

	// failures spread over time do not add up
	for i := 0; i < 10; i++ {
		clk.t = clk.t.Add(Window / 3)
		if l.Fail("9.9.9.9", "x") && i < 2 {
			t.Fatal("old failures counted")
		}
	}

	// many addresses against one user lock the user
	locked := false
	for i := 0; i < MaxUserFailures; i++ {
		locked = l.Fail("10.0.0."+string(rune('a'+i)), "victim")
	}
	if !locked {
		t.Fatal("user not locked")
	}
	if ok, _ := l.Allowed("10.9.9.9", "victim"); ok {
		t.Fatal("locked user allowed from a new address")
	}
	l.Succeed("10.9.9.9")
	if ok, _ := l.Allowed("10.9.9.9", "victim"); ok {
		t.Fatal("success elsewhere unlocked the user")
	}
}

func TestTokens(t *testing.T) {
	dir := t.TempDir()
	clk := newClock()
	ts := &Tokens{Path: filepath.Join(dir, "tokens.json"), Now: clk.now}
	tok, info, err := ts.Create("backup script", "admin", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok, "gg_"+info.ID+"_") || info.Hash != "" {
		t.Fatalf("token %q info %+v", tok, info)
	}
	raw, _ := os.ReadFile(ts.Path)
	if strings.Contains(string(raw), strings.TrimPrefix(tok, "gg_"+info.ID+"_")) {
		t.Fatal("secret stored in clear text")
	}
	if fi, err := os.Stat(ts.Path); err == nil && fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Fatalf("tokens file mode %v", fi.Mode())
	}

	// a fresh instance (restart) verifies from the file
	ts2 := &Tokens{Path: ts.Path, Now: clk.now}
	got, ok := ts2.Verify(tok)
	if !ok || got.User != "admin" || got.Hash != "" {
		t.Fatalf("verify: %+v %v", got, ok)
	}
	for _, bad := range []string{"", "gg_", "gg_" + info.ID + "_wrong", "gg_nope_" + strings.TrimPrefix(tok, "gg_"+info.ID+"_"), tok + "x"} {
		if _, ok := ts2.Verify(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
	list, _ := ts2.List()
	if len(list) != 1 || list[0].LastUsed.IsZero() || list[0].Hash != "" {
		t.Fatalf("list %+v", list)
	}
	if err := ts2.Delete(info.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := ts2.Verify(tok); ok {
		t.Fatal("deleted token still works")
	}
	if err := ts2.Delete(info.ID); err != ErrNoToken {
		t.Fatalf("delete twice: %v", err)
	}
	if _, _, err := ts2.Create("bad\nname", "admin", "admin"); err == nil {
		t.Fatal("bad name accepted")
	}
}

func TestEnsureCert(t *testing.T) {
	dir := t.TempDir()
	c1, err := EnsureCert(dir, []string{"goblin-gate", "192.168.1.1"})
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(c1.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if leaf.Subject.CommonName != "goblin-gate" || len(leaf.IPAddresses) != 1 || leaf.DNSNames[0] != "goblin-gate" {
		t.Fatalf("cert %v %v %v", leaf.Subject, leaf.IPAddresses, leaf.DNSNames)
	}
	c2, err := EnsureCert(dir, []string{"other"})
	if err != nil || string(c2.Certificate[0]) != string(c1.Certificate[0]) {
		t.Fatal("existing certificate was not reused")
	}
}

func TestAudit(t *testing.T) {
	a := &Audit{Path: filepath.Join(t.TempDir(), "audit.log")}
	if err := a.Log("login", "admin", "192.168.1.5", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.Log("commit", "admin", "", map[string]any{"revision": 3}); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(a.Path)
	defer f.Close()
	var n int
	for sc := bufio.NewScanner(f); sc.Scan(); n++ {
		var e AuditEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil || e.Event == "" {
			t.Fatalf("line %d: %v %+v", n, err, e)
		}
	}
	if n != 2 {
		t.Fatalf("%d lines", n)
	}
	var nilAudit *Audit
	if err := nilAudit.Log("x", "", "", nil); err != nil {
		t.Fatal(err)
	}
}
