package ips

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Rule sets.
const (
	RulesetOpen = "ET Open"
	RulesetPro  = "ET Pro"
)

// UpdateStatus is what the IPS page shows about the rule download.
type UpdateStatus struct {
	Ruleset    string    `json:"ruleset"`
	LastUpdate time.Time `json:"last_update,omitzero"`
	Rules      int       `json:"rules,omitempty"`
	LastError  string    `json:"last_error,omitempty"`
}

// updaterState is the Updater's mutable part.
type updaterState struct {
	mu     sync.Mutex
	status UpdateStatus
	once   sync.Once
	kick   chan struct{}
}

func (u *Updater) kicks() chan struct{} {
	u.state.once.Do(func() { u.state.kick = make(chan struct{}, 1) })
	return u.state.kick
}

// Kick asks Run to update now (the ET Pro code changed).
func (u *Updater) Kick() {
	select {
	case u.kicks() <- struct{}{}:
	default:
	}
}

// Status reports the last update.
func (u *Updater) Status() UpdateStatus {
	u.state.mu.Lock()
	defer u.state.mu.Unlock()
	st := u.state.status
	if st.Ruleset == "" {
		st.Ruleset = u.wantRuleset()
	}
	return st
}

func (u *Updater) wantRuleset() string {
	if u.ETProCode != nil && u.ETProCode() != "" {
		return RulesetPro
	}
	return RulesetOpen
}

func (u *Updater) setStatus(f func(*UpdateStatus)) {
	u.state.mu.Lock()
	defer u.state.mu.Unlock()
	f(&u.state.status)
}

// etProSource is suricata-update's own record of an enabled ET Pro source
// (what `suricata-update enable-source et/pro secret-code=...` writes),
// written here instead so the code never appears on a command line and the
// file is root's alone (suricata-update makes it world-readable).
func etProSource(code string) []byte {
	return []byte("params:\n  secret-code: " + code + "\nsource: et/pro\n")
}

// syncSource enables ET Pro in suricata-update when a code is set and
// removes it otherwise (suricata-update then falls back to ET Open). It
// returns the code, so errors can be scrubbed of it.
func (u *Updater) syncSource(ctx context.Context) (string, error) {
	if u.ETProCode == nil || u.Paths.UpdateDir == "" {
		return "", nil
	}
	code := u.ETProCode()
	path := filepath.Join(u.Paths.UpdateDir, "sources", "et-pro.yaml")
	if code == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		return "", nil
	}
	if !config.ValidETProCode(code) {
		return "", errors.New("invalid ET Pro code")
	}
	// suricata-update finds et/pro's URL in its source index
	if _, err := os.Stat(filepath.Join(u.Paths.UpdateDir, "cache", "index.yaml")); err != nil {
		if err := u.exec(ctx, "suricata-update", "update-sources"); err != nil {
			return code, fmt.Errorf("fetch the rule source index: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return code, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, etProSource(code), 0o600); err != nil {
		return code, err
	}
	if err := os.Chmod(tmp, 0o600); err != nil { // an older file may have been 0644
		return code, err
	}
	return code, os.Rename(tmp, path)
}

// scrub removes the ET Pro code from an error: suricata-update's messages
// carry the download URL, and the code is part of it.
func scrub(err error, code string) error {
	if err == nil || code == "" || !strings.Contains(err.Error(), code) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), code, "<ET Pro code>"))
}
