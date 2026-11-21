package sites

import (
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

func offer(name, addr string, nets ...string) Offer {
	o := Offer{Name: name, PublicKey: "YAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=", Address: netip.MustParseAddr(addr)}
	for _, n := range nets {
		o.Networks = append(o.Networks, netip.MustParsePrefix(n))
	}
	return o
}

func TestRoundTrip(t *testing.T) {
	inv, err := NewInvitation(offer("home", "10.254.1.1", "192.168.1.0/24"), now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// pasted with line breaks and spaces
	code := inv.Code()
	pasted := code[:30] + "\n  " + code[30:]
	got, err := ParseInvitation(pasted, now.Add(59*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != inv.ID || got.Networks[0] != inv.Networks[0] || got.Name != "home" {
		t.Fatalf("%+v", got)
	}
	reply := got.Answer(offer("cabin", "10.254.2.2", "192.168.50.0/24"))
	r, err := ParseReply(reply.Code())
	if err != nil {
		t.Fatal(err)
	}
	if !r.Verify(inv.Secret) || r.ID != inv.ID {
		t.Fatal("reply does not verify")
	}
	r.Networks = append(r.Networks, netip.MustParsePrefix("10.0.0.0/8"))
	if r.Verify(inv.Secret) {
		t.Fatal("tampered reply verifies")
	}
	other, _ := NewInvitation(offer("home", "10.254.1.1"), now, time.Hour)
	if reply.Verify(other.Secret) {
		t.Fatal("reply verifies against another invitation")
	}
	a, _ := PresharedKey(inv.Secret, inv.ID)
	b, _ := PresharedKey(got.Secret, got.ID)
	c, _ := PresharedKey(other.Secret, other.ID)
	if a != b || a == c || len(a) != 44 {
		t.Fatalf("psk %s %s %s", a, b, c)
	}
}

func TestParseErrors(t *testing.T) {
	inv, _ := NewInvitation(offer("home", "10.254.1.1"), now, time.Hour)
	reply := inv.Answer(offer("cabin", "10.254.2.2"))
	for name, tc := range map[string]struct {
		code string
		at   time.Time
		want string
	}{
		"expired":    {inv.Code(), now.Add(time.Hour), "expired"},
		"reply":      {reply.Code(), now, "reply code"},
		"garbage":    {"hello", now, "not a Goblin Gate invitation"},
		"damaged":    {inv.Code()[:40], now, "damaged"},
		"too long":   {"GGSITE1-" + strings.Repeat("A", 20000), now, "too long"},
		"bad offer":  {Invitation{V: 1, ID: "0123456789abcdef", Secret: make([]byte, 32), Expires: now.Add(time.Hour), Offer: Offer{PublicKey: "x"}}.Code(), now, "public key"},
		"bad net":    {Invitation{V: 1, ID: "0123456789abcdef", Secret: make([]byte, 32), Expires: now.Add(time.Hour), Offer: offer("x", "10.254.1.1", "0.0.0.0/0")}.Code(), now, "invalid network"},
		"newer code": {Invitation{V: 2, ID: "0123456789abcdef", Secret: make([]byte, 32), Expires: now.Add(time.Hour), Offer: offer("x", "10.254.1.1")}.Code(), now, "version 2"},
	} {
		if _, err := ParseInvitation(tc.code, tc.at); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
	if _, err := ParseReply(inv.Code()); err == nil || !strings.Contains(err.Error(), "invitation code") {
		t.Errorf("invitation as reply: %v", err)
	}
}

func TestSiteName(t *testing.T) {
	for in, want := range map[string]string{
		"home-gw.example.net":       "home-gw",
		"Cabin":                     "cabin",
		"9th_floor office":          "th-floor-office",
		"averyveryverylonghostname": "averyveryverylo",
		"---":                       "",
	} {
		if got := SiteName(in); got != want {
			t.Errorf("SiteName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInvites(t *testing.T) {
	clock := now
	s := &Invites{Path: filepath.Join(t.TempDir(), "x", "inv.json"), Now: func() time.Time { return clock }}
	for i := range MaxPending {
		if err := s.Add(Pending{ID: string(rune('a' + i)), Site: "s" + string(rune('a'+i)), Port: 51830 + i, Expires: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Add(Pending{ID: "z", Expires: now.Add(time.Hour)}); err != ErrTooMany {
		t.Fatalf("over the limit: %v", err)
	}
	names, ports := s.Reserved()
	if !names["sa"] || len(ports) != MaxPending {
		t.Fatalf("reserved %v %v", names, ports)
	}
	ok, err := s.Take("a", func(Pending) error { return ErrTooMany })
	if !ok || err != ErrTooMany {
		t.Fatal("take with a failing use")
	}
	if all, _ := s.List(); len(all) != MaxPending {
		t.Fatal("a failed use removed the invitation")
	}
	if ok, _ := s.Take("a", func(Pending) error { return nil }); !ok {
		t.Fatal("take")
	}
	if ok, _ := s.Take("a", func(Pending) error { return nil }); ok {
		t.Fatal("taken twice")
	}
	clock = now.Add(2 * time.Hour)
	if all, _ := s.List(); len(all) != 0 {
		t.Fatalf("expired kept: %d", len(all))
	}
}
