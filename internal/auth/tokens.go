package auth

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// TokenInfo is an API token as stored and listed (never with its secret).
type TokenInfo struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	User     string    `json:"user"`
	Role     string    `json:"role"`
	Hash     string    `json:"hash,omitempty"` // SHA-256 of the secret; never sent by the API
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"last_used,omitzero"`
	// Scopes limit what the token may do (task 102); empty = everything
	// its role allows. A scoped token never sees secrets and never reaches
	// admin-only endpoints (tokens, admins, 2FA, restore, plugins, ...).
	Scopes []string `json:"scopes,omitempty"`
	// Expires, if set, ends the token.
	Expires time.Time `json:"expires,omitzero"`
	// AllowedFrom, if set, lists the networks the token may be used from.
	AllowedFrom []netip.Prefix `json:"allowed_from,omitempty"`
}

// Token scopes. ScopeRead covers every GET and the read-only POSTs; the
// write scopes cover config sections and actions by area.
const (
	ScopeRead          = "read"
	ScopeWriteFirewall = "write:firewall"
	ScopeWriteNetwork  = "write:network"
	ScopeWriteSystem   = "write:system"
	ScopeActions       = "actions"
	ScopeCommit        = "commit"
)

// Scopes lists every token scope.
var Scopes = []string{ScopeRead, ScopeWriteFirewall, ScopeWriteNetwork, ScopeWriteSystem, ScopeActions, ScopeCommit}

// TokenOptions are the optional limits of a new token.
type TokenOptions struct {
	Scopes      []string
	Expires     time.Time
	AllowedFrom []netip.Prefix
}

// AllowsAddr reports whether the token may be used from addr.
func (ti TokenInfo) AllowsAddr(addr netip.Addr) bool {
	if len(ti.AllowedFrom) == 0 {
		return true
	}
	addr = addr.Unmap()
	for _, p := range ti.AllowedFrom {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// ErrNoToken is returned for an unknown token id.
var ErrNoToken = errors.New("auth: no such token")

// Tokens stores API tokens hashed in a JSON file. A token reads
// "gg_<id>_<secret>"; the id finds the entry, the secret is compared by
// hash. Secrets are 256-bit random, so a fast hash is enough at rest.
type Tokens struct {
	Path string // data_dir/tokens.json
	Now  func() time.Time

	mu     sync.Mutex
	loaded bool
	byID   map[string]*TokenInfo
}

var tokenNameRe = regexp.MustCompile(`^[A-Za-z0-9 _.-]{1,64}$`)

func (t *Tokens) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

func (t *Tokens) loadLocked() error {
	if t.loaded {
		return nil
	}
	t.byID = map[string]*TokenInfo{}
	b, err := os.ReadFile(t.Path)
	if errors.Is(err, fs.ErrNotExist) {
		t.loaded = true
		return nil
	}
	if err != nil {
		return fmt.Errorf("auth: read tokens: %w", err)
	}
	var list []*TokenInfo
	if err := json.Unmarshal(b, &list); err != nil {
		return fmt.Errorf("auth: parse tokens: %w", err)
	}
	for _, ti := range list {
		t.byID[ti.ID] = ti
	}
	t.loaded = true
	return nil
}

func (t *Tokens) saveLocked() error {
	list := make([]*TokenInfo, 0, len(t.byID))
	for _, ti := range t.byID {
		list = append(list, ti)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Created.Before(list[j].Created) })
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(t.Path), 0o750); err != nil {
		return err
	}
	tmp := t.Path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, t.Path)
}

// Create makes a token for user/role and returns the full token string
// (shown once) and its info.
func (t *Tokens) Create(name, user, role string) (string, TokenInfo, error) {
	return t.CreateWith(name, user, role, TokenOptions{})
}

// CreateWith is Create with scopes, an expiry and allowed networks.
func (t *Tokens) CreateWith(name, user, role string, o TokenOptions) (string, TokenInfo, error) {
	if !tokenNameRe.MatchString(name) {
		return "", TokenInfo{}, errors.New("token name: 1-64 letters, digits, space, _ . -")
	}
	var scopes []string
	for _, sc := range o.Scopes {
		if !slices.Contains(Scopes, sc) {
			return "", TokenInfo{}, fmt.Errorf("unknown scope %q: want %s", sc, strings.Join(Scopes, ", "))
		}
		if role == "readonly" && sc != ScopeRead {
			return "", TokenInfo{}, fmt.Errorf("a read-only token cannot have scope %q", sc)
		}
		if !slices.Contains(scopes, sc) {
			scopes = append(scopes, sc)
		}
	}
	if !o.Expires.IsZero() && !o.Expires.After(t.now()) {
		return "", TokenInfo{}, errors.New("expires: must be in the future")
	}
	if len(o.AllowedFrom) > 16 {
		return "", TokenInfo{}, errors.New("allowed_from: at most 16 networks")
	}
	for i, p := range o.AllowedFrom {
		o.AllowedFrom[i] = p.Masked()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.loadLocked(); err != nil {
		return "", TokenInfo{}, err
	}
	id := strings.ToLower(RandomToken(6))
	id = strings.NewReplacer("-", "x", "_", "y").Replace(id) // ids never contain the separator
	secret := RandomToken(32)
	ti := &TokenInfo{ID: id, Name: name, User: user, Role: role, Hash: hashID(secret), Created: t.now(),
		Scopes: scopes, Expires: o.Expires, AllowedFrom: o.AllowedFrom}
	t.byID[id] = ti
	if err := t.saveLocked(); err != nil {
		delete(t.byID, id)
		return "", TokenInfo{}, fmt.Errorf("auth: save tokens: %w", err)
	}
	out := *ti
	out.Hash = ""
	return "gg_" + id + "_" + secret, out, nil
}

// Verify checks a full token string and records its use.
func (t *Tokens) Verify(token string) (TokenInfo, bool) {
	rest, ok := strings.CutPrefix(token, "gg_")
	if !ok {
		return TokenInfo{}, false
	}
	id, secret, ok := strings.Cut(rest, "_")
	if !ok || id == "" || secret == "" {
		return TokenInfo{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.loadLocked() != nil {
		return TokenInfo{}, false
	}
	ti, ok := t.byID[id]
	if !ok || subtle.ConstantTimeCompare([]byte(hashID(secret)), []byte(ti.Hash)) != 1 {
		return TokenInfo{}, false
	}
	now := t.now()
	if !ti.Expires.IsZero() && !now.Before(ti.Expires) {
		return TokenInfo{}, false
	}
	if now.Sub(ti.LastUsed) > time.Minute { // don't rewrite the file on every request
		ti.LastUsed = now
		_ = t.saveLocked()
	}
	out := *ti
	out.Hash = ""
	return out, true
}

// List returns all tokens without hashes, oldest first.
func (t *Tokens) List() ([]TokenInfo, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.loadLocked(); err != nil {
		return nil, err
	}
	out := make([]TokenInfo, 0, len(t.byID))
	for _, ti := range t.byID {
		c := *ti
		c.Hash = ""
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out, nil
}

// Delete revokes a token.
func (t *Tokens) Delete(id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.loadLocked(); err != nil {
		return err
	}
	if _, ok := t.byID[id]; !ok {
		return ErrNoToken
	}
	old := t.byID[id]
	delete(t.byID, id)
	if err := t.saveLocked(); err != nil {
		t.byID[id] = old
		return fmt.Errorf("auth: save tokens: %w", err)
	}
	return nil
}
