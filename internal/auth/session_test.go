package auth

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

var hex16 = regexp.MustCompile(`^[0-9a-f]{16}$`)

func TestSessionListOrderAndFormat(t *testing.T) {
	clk := newClock()
	s := &Sessions{Now: clk.now}

	// Create three sessions with distinct timestamps.
	_, idA := s.CreateFrom("alice", "admin", "10.0.0.1", "UA-A")
	clk.t = clk.t.Add(time.Second)
	_, idB := s.CreateFrom("bob", "readonly", "10.0.0.2", "UA-B")
	clk.t = clk.t.Add(time.Second)
	_, idC := s.CreateFrom("carol", "admin", "10.0.0.3", "UA-C")

	list := s.List()
	if len(list) != 3 {
		t.Fatalf("len %d, want 3", len(list))
	}
	// Newest first: carol, bob, alice.
	wantUsers := []string{"carol", "bob", "alice"}
	for i, si := range list {
		if si.User != wantUsers[i] {
			t.Errorf("list[%d].user = %q, want %q", i, si.User, wantUsers[i])
		}
		if !hex16.MatchString(si.ID) {
			t.Errorf("list[%d].id %q is not 16 hex chars", i, si.ID)
		}
	}
	// Spot-check fields on the first entry.
	if list[0].Role != "admin" || list[0].Addr != "10.0.0.3" || list[0].UserAgent != "UA-C" {
		t.Errorf("list[0] = %+v", list[0])
	}

	// SessID matches the short ID in the list for each identity.
	if idA.SessID()[:16] != list[2].ID {
		t.Errorf("idA.SessID %q != list[2].ID %q", idA.SessID(), list[2].ID)
	}
	if idB.SessID()[:16] != list[1].ID {
		t.Errorf("idB.SessID %q != list[1].ID %q", idB.SessID(), list[1].ID)
	}
	if idC.SessID()[:16] != list[0].ID {
		t.Errorf("idC.SessID %q != list[0].ID %q", idC.SessID(), list[0].ID)
	}
}

func TestSessionListExcludesExpired(t *testing.T) {
	clk := newClock()
	s := &Sessions{Now: clk.now}

	cookieOld, _ := s.Create("old", "admin")
	clk.t = clk.t.Add(IdleTimeout + time.Minute) // old session now expired
	_, _ = s.Create("new", "readonly")

	list := s.List()
	if len(list) != 1 {
		t.Fatalf("len %d, want 1 (expired should be GC'd)", len(list))
	}
	if list[0].User != "new" {
		t.Errorf("list[0].user = %q, want new", list[0].User)
	}
	// The expired cookie must no longer work.
	if _, ok := s.Lookup(cookieOld); ok {
		t.Fatal("expired session still valid after List")
	}
}

func TestSessionDeleteByID(t *testing.T) {
	s := &Sessions{}
	cookie, _ := s.CreateFrom("admin", "admin", "1.2.3.4", "UA")
	list := s.List()
	if len(list) != 1 {
		t.Fatalf("len %d", len(list))
	}
	shortID := list[0].ID

	if !s.DeleteByID(shortID) {
		t.Fatal("DeleteByID returned false for existing session")
	}
	if _, ok := s.Lookup(cookie); ok {
		t.Fatal("session still valid after DeleteByID")
	}
	if s.List() != nil && len(s.List()) != 0 {
		t.Fatalf("list not empty after delete: %v", s.List())
	}
	if s.DeleteByID(shortID) {
		t.Fatal("DeleteByID returned true for already-deleted session")
	}
	if s.DeleteByID("ffffffffffffffff") {
		t.Fatal("DeleteByID returned true for unknown id")
	}
}

func TestSessionCreateFromTruncatesUserAgent(t *testing.T) {
	s := &Sessions{}
	longUA := strings.Repeat("x", 250)
	_, _ = s.CreateFrom("u", "admin", "1.1.1.1", longUA)
	list := s.List()
	if len(list) != 1 {
		t.Fatalf("len %d", len(list))
	}
	if len(list[0].UserAgent) != 200 {
		t.Errorf("user agent length = %d, want 200", len(list[0].UserAgent))
	}
	if list[0].UserAgent != longUA[:200] {
		t.Error("truncated user agent does not match prefix")
	}
}

func TestSessionSessID(t *testing.T) {
	s := &Sessions{}
	_, id := s.CreateFrom("u", "admin", "1.1.1.1", "")
	if id.SessID() == "" {
		t.Fatal("session identity has empty SessID")
	}
	if len(id.SessID()) != 64 {
		t.Errorf("SessID length = %d, want 64 (full sha256 hex)", len(id.SessID()))
	}

	// Non-session identities have empty SessID.
	token := Identity{User: "u", Role: "admin", Method: "token"}
	if token.SessID() != "" {
		t.Errorf("token identity SessID = %q, want empty", token.SessID())
	}
	socket := Identity{User: "root", Role: "admin", Method: "socket"}
	if socket.SessID() != "" {
		t.Errorf("socket identity SessID = %q, want empty", socket.SessID())
	}
}

func TestDeleteByIDExactOnly(t *testing.T) {
	var s Sessions
	_, a := s.Create("alice", "admin")
	s.Create("bob", "admin")
	for _, bad := range []string{"", a.SessID()[:1], a.SessID()[:15], a.SessID()} {
		if s.DeleteByID(bad) {
			t.Fatalf("DeleteByID(%q) ended a session", bad)
		}
	}
	if len(s.List()) != 2 || !s.DeleteByID(a.SessID()[:16]) || len(s.List()) != 1 {
		t.Fatal("exact short id did not end exactly one session")
	}
}
