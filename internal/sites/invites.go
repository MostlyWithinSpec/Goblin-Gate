package sites

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// Pending is an invitation this gate made and has not seen answered yet.
// It holds the one-time secret, so the file is root's alone.
type Pending struct {
	ID      string    `json:"id"`
	Site    string    `json:"site"` // what this gate will call the other one
	Port    int       `json:"port"` // this gate's listen port for the tunnel
	Secret  []byte    `json:"secret"`
	Expires time.Time `json:"expires"`
	Created time.Time `json:"created"`
}

// MaxPending bounds the open invitations.
const MaxPending = 16

// Invites keeps the pending invitations in one file.
type Invites struct {
	Path string
	Now  func() time.Time
	mu   sync.Mutex
}

func (s *Invites) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// load returns the pending invitations that have not expired.
func (s *Invites) load() ([]Pending, error) {
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var all []Pending
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, err
	}
	now := s.now()
	return slices.DeleteFunc(all, func(p Pending) bool { return !now.Before(p.Expires) }), nil
}

func (s *Invites) save(all []Pending) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o750); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(all, "", "  ")
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

// List returns the open invitations (expired ones are dropped).
func (s *Invites) List() ([]Pending, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// ErrTooMany: MaxPending invitations are open.
var ErrTooMany = errors.New("too many open invitations: cancel one or let it expire")

// Add keeps a new invitation.
func (s *Invites) Add(p Pending) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.load()
	if err != nil {
		return err
	}
	if len(all) >= MaxPending {
		return ErrTooMany
	}
	p.Created = s.now().UTC().Truncate(time.Second)
	return s.save(append(all, p))
}

// Take removes and returns the invitation with id; ok is false when there
// is none (never made, answered already, cancelled or expired).
func (s *Invites) Take(id string, use func(Pending) error) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.load()
	if err != nil {
		return false, err
	}
	i := slices.IndexFunc(all, func(p Pending) bool { return p.ID == id })
	if i < 0 {
		return false, nil
	}
	if err := use(all[i]); err != nil {
		return true, err // kept: a reply that did not fit may be fixed and pasted again
	}
	return true, s.save(slices.Delete(all, i, i+1))
}

// Delete cancels an invitation; false when there is none.
func (s *Invites) Delete(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.load()
	if err != nil {
		return false, err
	}
	n := len(all)
	all = slices.DeleteFunc(all, func(p Pending) bool { return p.ID == id })
	if len(all) == n {
		return false, nil
	}
	return true, s.save(all)
}

// Reserved are the names and ports the open invitations hold.
func (s *Invites) Reserved() (names map[string]bool, ports []int) {
	all, _ := s.List()
	names = map[string]bool{}
	for _, p := range all {
		names[p.Site] = true
		ports = append(ports, p.Port)
	}
	return names, ports
}
