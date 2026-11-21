// Package sites pairs Goblin Gates into a site-to-site VPN (task 155)
// without a central server: one gate makes a short-lived invitation code,
// the other pastes it and answers with a reply code, and the first one
// pastes that. The invitation carries a one-time secret: the reply is
// authenticated with it (HMAC-SHA256), and both gates derive the tunnel's
// WireGuard preshared key from it, so whoever reads only one of the codes
// later cannot pose as either gate.
package sites

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"time"

	"golang.org/x/crypto/hkdf"
)

// Version of the code format.
const Version = 1

const (
	invitePrefix = "GGSITE1-"
	replyPrefix  = "GGREPLY1-"
)

// Offer is one gate's half of a pairing.
type Offer struct {
	// Name is what the gate suggests the other one calls it (its hostname
	// made into a site name).
	Name      string         `json:"name"`
	PublicKey string         `json:"pub"`
	Endpoint  string         `json:"endpoint,omitempty"` // host:port, "" = dials out only
	Address   netip.Addr     `json:"addr"`               // its tunnel address
	Networks  []netip.Prefix `json:"nets,omitempty"`
}

// Invitation is the first code.
type Invitation struct {
	V       int       `json:"v"`
	ID      string    `json:"id"`
	Secret  []byte    `json:"secret"` // 32 bytes
	Expires time.Time `json:"exp"`
	Offer
}

// Reply is the second code.
type Reply struct {
	V  int    `json:"v"`
	ID string `json:"id"` // the invitation's
	Offer
	MAC []byte `json:"mac"` // HMAC-SHA256(secret, the reply without MAC)
}

// NewInvitation makes an invitation valid for ttl.
func NewInvitation(o Offer, now time.Time, ttl time.Duration) (Invitation, error) {
	inv := Invitation{V: Version, Expires: now.Add(ttl).UTC().Truncate(time.Second), Offer: o, Secret: make([]byte, 32)}
	id := make([]byte, 8)
	if _, err := rand.Read(id); err != nil {
		return inv, err
	}
	if _, err := rand.Read(inv.Secret); err != nil {
		return inv, err
	}
	inv.ID = hex.EncodeToString(id)
	return inv, nil
}

// Code is the invitation as text to paste.
func (inv Invitation) Code() string { return invitePrefix + encode(inv) }

// Code is the reply as text to paste.
func (r Reply) Code() string { return replyPrefix + encode(r) }

func encode(v any) string {
	b, _ := json.Marshal(v)
	return base64.RawURLEncoding.EncodeToString(b)
}

// maxCode bounds what is decoded (a code with 64 networks is ~4 KB).
const maxCode = 16 << 10

func decode(code, prefix string, v any) error {
	code = strings.Join(strings.Fields(code), "") // pasted across lines
	if len(code) > maxCode {
		return errors.New("the code is too long")
	}
	rest, ok := strings.CutPrefix(code, prefix)
	if !ok {
		return fmt.Errorf("not a Goblin Gate %s code", map[string]string{invitePrefix: "invitation", replyPrefix: "reply"}[prefix])
	}
	b, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil {
		return errors.New("the code is damaged (copy all of it)")
	}
	if err := json.Unmarshal(b, v); err != nil {
		return errors.New("the code is damaged (copy all of it)")
	}
	return nil
}

// ParseInvitation reads an invitation code and checks its shape and expiry.
func ParseInvitation(code string, now time.Time) (Invitation, error) {
	var inv Invitation
	if err := decode(code, invitePrefix, &inv); err != nil {
		if strings.HasPrefix(strings.TrimSpace(code), replyPrefix) {
			return inv, errors.New("this is a reply code: paste it on the gate that made the invitation")
		}
		return inv, err
	}
	if inv.V != Version {
		return inv, fmt.Errorf("invitation version %d: update both gates to the same release", inv.V)
	}
	if len(inv.Secret) != 32 || len(inv.ID) != 16 {
		return inv, errors.New("the code is damaged (copy all of it)")
	}
	if !now.Before(inv.Expires) {
		return inv, fmt.Errorf("the invitation expired at %s: make a new one", inv.Expires.Format(time.RFC3339))
	}
	return inv, inv.Offer.check()
}

// Answer makes the reply to inv.
func (inv Invitation) Answer(o Offer) Reply {
	r := Reply{V: Version, ID: inv.ID, Offer: o}
	r.MAC = r.mac(inv.Secret)
	return r
}

func (r Reply) mac(secret []byte) []byte {
	c := r
	c.MAC = nil
	b, _ := json.Marshal(c)
	m := hmac.New(sha256.New, secret)
	m.Write([]byte("goblin-gate site reply\x00"))
	m.Write(b)
	return m.Sum(nil)
}

// ParseReply reads a reply code; Verify checks it against the invitation.
func ParseReply(code string) (Reply, error) {
	var r Reply
	if err := decode(code, replyPrefix, &r); err != nil {
		if strings.HasPrefix(strings.TrimSpace(code), invitePrefix) {
			return r, errors.New("this is an invitation code: paste it on the other gate under Join a site")
		}
		return r, err
	}
	if r.V != Version {
		return r, fmt.Errorf("reply version %d: update both gates to the same release", r.V)
	}
	return r, r.Offer.check()
}

// Verify reports whether the reply was made from the invitation with secret.
func (r Reply) Verify(secret []byte) bool { return hmac.Equal(r.MAC, r.mac(secret)) }

// PresharedKey derives the tunnel's WireGuard preshared key (base64).
func PresharedKey(secret []byte, id string) (string, error) {
	k := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, secret, []byte(id), []byte("goblin-gate site psk")), k); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(k), nil
}

func (o Offer) check() error {
	if k, err := base64.StdEncoding.DecodeString(o.PublicKey); err != nil || len(k) != 32 {
		return errors.New("the code has no valid public key")
	}
	if !o.Address.Is4() {
		return errors.New("the code has no IPv4 tunnel address")
	}
	if len(o.Networks) > 64 {
		return errors.New("the code brings more than 64 networks")
	}
	for _, n := range o.Networks {
		if !n.IsValid() || n.Masked() != n || n.Bits() == 0 {
			return fmt.Errorf("the code brings an invalid network %s", n)
		}
	}
	return nil
}

// SiteName turns a hostname into a valid site name ("" when nothing is left).
func SiteName(host string) string {
	host, _, _ = strings.Cut(strings.ToLower(host), ".")
	var b strings.Builder
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == ' ':
			if b.Len() > 0 {
				b.WriteByte('-')
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	for len(s) > 0 && (s[0] < 'a' || s[0] > 'z') {
		s = s[1:]
	}
	if len(s) > 15 {
		s = strings.TrimRight(s[:15], "-")
	}
	return s
}
