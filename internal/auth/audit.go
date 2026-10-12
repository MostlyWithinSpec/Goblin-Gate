package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AuditEntry is one line of the audit log.
type AuditEntry struct {
	Time   time.Time      `json:"time"`
	Event  string         `json:"event"` // login, login_failed, login_locked, logout, token_created, token_deleted, commit, rollback, setup, ...
	User   string         `json:"user,omitempty"`
	Addr   string         `json:"addr,omitempty"`
	Detail map[string]any `json:"detail,omitempty"`
}

// Audit appends JSON lines to a file (data_dir/audit.log). A nil *Audit
// discards entries.
type Audit struct {
	Path string
	Now  func() time.Time

	mu sync.Mutex
}

// Log writes one entry; errors are returned for the caller to log.
func (a *Audit) Log(event, user, addr string, detail map[string]any) error {
	if a == nil {
		return nil
	}
	now := time.Now()
	if a.Now != nil {
		now = a.Now()
	}
	b, err := json.Marshal(AuditEntry{Time: now.UTC(), Event: event, User: user, Addr: addr, Detail: detail})
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(a.Path), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(a.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(b, '\n'))
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}
