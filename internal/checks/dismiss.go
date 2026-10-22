package checks

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
)

// keyRe limits what a dismissal may store: check ids, interface and admin
// names, policy numbers and versions.
var keyRe = regexp.MustCompile(`^[a-z0-9-]{1,32}:[A-Za-z0-9_.:~+-]{1,96}$`)

// ValidKey reports whether key can be a finding key.
func ValidKey(key string) bool { return keyRe.MatchString(key) }

// maxDismissed caps the stored list so the file cannot grow without bound.
const maxDismissed = 500

// Dismissals keeps the keys of dismissed findings in a JSON file. They are
// box state, not config: dismissing does not create a change to commit.
type Dismissals struct {
	Path string

	mu sync.Mutex
}

// Load returns the dismissed keys; a missing file is an empty set.
func (d *Dismissals) Load() (map[string]bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	keys, err := d.read()
	out := make(map[string]bool, len(keys))
	for _, k := range keys {
		out[k] = true
	}
	return out, err
}

// Set adds (on) or removes (off) key.
func (d *Dismissals) Set(key string, on bool) error {
	if !ValidKey(key) {
		return errors.New("invalid finding key")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	keys, err := d.read()
	if err != nil {
		return err
	}
	has := slices.Contains(keys, key)
	switch {
	case on && !has:
		if len(keys) >= maxDismissed {
			return errors.New("too many dismissed findings")
		}
		keys = append(keys, key)
	case !on && has:
		keys = slices.DeleteFunc(keys, func(k string) bool { return k == key })
	default:
		return nil
	}
	slices.Sort(keys)
	b, _ := json.Marshal(map[string][]string{"dismissed": keys})
	if err := os.MkdirAll(filepath.Dir(d.Path), 0o750); err != nil {
		return err
	}
	tmp := d.Path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, d.Path)
}

func (d *Dismissals) read() ([]string, error) {
	b, err := os.ReadFile(d.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc struct {
		Dismissed []string `json:"dismissed"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	return slices.DeleteFunc(doc.Dismissed, func(k string) bool { return !ValidKey(k) }), nil
}
