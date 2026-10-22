package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// nutSecret returns the password of the box's own upsmon account (UPS on
// USB, task 112), creating it on first use. It is box state, not config:
// it stays out of backups, config history and the API.
func nutSecret(path string) func() (string, error) {
	var mu sync.Mutex
	return func() (string, error) {
		mu.Lock()
		defer mu.Unlock()
		b, err := os.ReadFile(path)
		if err == nil && len(strings.TrimSpace(string(b))) >= 32 {
			return strings.TrimSpace(string(b)), nil
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		raw := make([]byte, 24)
		if _, err := rand.Read(raw); err != nil {
			return "", err
		}
		secret := hex.EncodeToString(raw)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(secret+"\n"), 0o600); err != nil {
			return "", err
		}
		return secret, nil
	}
}
