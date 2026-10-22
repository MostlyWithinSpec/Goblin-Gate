package apply

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
)

// fileMode is the permission of a managed file unless its group sets one.
const fileMode = 0o644

// privateMode is the permission of everything else the applier writes: the
// staged copies for checks, the backup of the previous files and the
// manifest. They can hold a copy of a secret file and only root reads them.
const privateMode = 0o600

// modeFor returns the permission and owning group name ("" = leave it)
// of a file written at p.
func modeFor(p string) (os.FileMode, string) {
	gi, err := groupFor(p)
	if err != nil {
		return privateMode, ""
	}
	g := groups[gi]
	if g.mode == 0 {
		return fileMode, g.fileGroup
	}
	return g.mode, g.fileGroup
}

// read returns the content of p and whether it exists.
func (h *Host) read(p string) ([]byte, bool, error) {
	b, err := os.ReadFile(h.native(p))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", p, err)
	}
	return b, true, nil
}

// writeAtomic writes data to p via a temp file in the same directory, fsync
// and rename, so readers never see a partial file.
func (h *Host) writeAtomic(p string, data []byte) error {
	full := h.native(p)
	dir := filepath.Dir(full)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("write %s: %w", p, err)
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(full)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", p, err)
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", p, err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("write %s: %w", p, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write %s: %w", p, err)
	}
	mode, owner := modeFor(p)
	if err := os.Chmod(tmp, mode); err != nil {
		return fmt.Errorf("write %s: %w", p, err)
	}
	if owner != "" {
		// a missing group (the daemon's package is not installed) leaves the
		// file root's: the daemon can't read it, but nothing leaks
		if gid, ok := groupID(owner); ok {
			if err := os.Chown(tmp, -1, gid); err != nil {
				return fmt.Errorf("write %s: %w", p, err)
			}
		}
	}
	if err := os.Rename(tmp, full); err != nil {
		return fmt.Errorf("write %s: %w", p, err)
	}
	ok = true
	syncDir(dir)
	return nil
}

// manifest lists the files written by the last successful Apply, so files
// that are no longer rendered can be removed. Only these are ever deleted.
type manifest struct {
	Files []string `json:"files"`
}

func (h *Host) manifestPath() string { return path.Join(h.stateDir(), "manifest.json") }

func (h *Host) loadManifest() ([]string, error) {
	b, ok, err := h.read(h.manifestPath())
	if err != nil || !ok {
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("manifest %s: %w", h.manifestPath(), err)
	}
	return m.Files, nil
}

func (h *Host) saveManifest(paths []string) error {
	files := append([]string{}, paths...)
	sort.Strings(files)
	b, err := json.MarshalIndent(manifest{Files: files}, "", "  ")
	if err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	b = append(b, '\n')
	if old, ok, _ := h.read(h.manifestPath()); ok && string(old) == string(b) {
		return nil
	}
	return h.writeAtomic(h.manifestPath(), b)
}
