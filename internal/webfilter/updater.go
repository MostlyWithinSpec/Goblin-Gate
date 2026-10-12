package webfilter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// MaxDownload caps the category list download.
const MaxDownload = 400 << 20

// Updater keeps the category database current: it loads Dir/categories.db
// at start, and while Wanted reports true downloads the UT1 lists every
// Every (default 7 days), rebuilds the database and swaps it into Filter.
type Updater struct {
	URL    string // default UT1URL
	Dir    string // data_dir/webfilter
	Filter *Filter
	Every  time.Duration
	Wanted func() bool // a profile uses categories; nil = always
	Client *http.Client
	Logger *slog.Logger
}

func (u *Updater) dbPath() string  { return filepath.Join(u.Dir, "categories.db") }
func (u *Updater) tgzPath() string { return filepath.Join(u.Dir, "ut1.tar.gz") }

func (u *Updater) logger() *slog.Logger {
	if u.Logger != nil {
		return u.Logger
	}
	return slog.Default()
}

// Run loads the saved database and updates on schedule until ctx ends.
func (u *Updater) Run(ctx context.Context) {
	if db, err := Open(u.dbPath()); err == nil {
		u.Filter.SetDB(db)
		u.logger().Info("webfilter: category database loaded", "entries", db.Len())
	} else if !errors.Is(err, fs.ErrNotExist) {
		u.logger().Warn("webfilter: category database unreadable", "error", err)
	}
	every := u.Every
	if every <= 0 {
		every = 7 * 24 * time.Hour
	}
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		if u.Wanted == nil || u.Wanted() {
			st, err := os.Stat(u.dbPath())
			if err != nil || time.Since(st.ModTime()) > every {
				if err := u.Update(ctx); err != nil {
					u.logger().Warn("webfilter: category update failed", "error", err)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Update downloads the lists (unless unchanged), rebuilds and swaps the DB.
func (u *Updater) Update(ctx context.Context) error {
	url := u.URL
	if url == "" {
		url = UT1URL
	}
	if err := os.MkdirAll(u.Dir, 0o750); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if st, err := os.Stat(u.tgzPath()); err == nil {
		req.Header.Set("If-Modified-Since", st.ModTime().UTC().Format(http.TimeFormat))
	}
	client := u.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotModified:
		now := time.Now()
		return os.Chtimes(u.dbPath(), now, now) // checked; nothing new
	case http.StatusOK:
	default:
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	tmp := u.tgzPath() + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, MaxDownload+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > MaxDownload {
		err = errors.New("download too large")
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if lm, err := http.ParseTime(resp.Header.Get("Last-Modified")); err == nil {
		os.Chtimes(tmp, lm, lm)
	}

	in, err := os.Open(tmp)
	if err != nil {
		return err
	}
	b := NewBuilder()
	cats, err := BuildUT1(in, b)
	in.Close()
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if cats == 0 {
		os.Remove(tmp)
		return errors.New("the download has no categories")
	}
	dbTmp := u.dbPath() + ".tmp"
	out, err := os.Create(dbTmp)
	if err != nil {
		return err
	}
	if _, err := b.WriteTo(out); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	db, err := Open(dbTmp)
	if err != nil {
		return err
	}
	if err := os.Rename(dbTmp, u.dbPath()); err != nil {
		return err
	}
	if err := os.Rename(tmp, u.tgzPath()); err != nil {
		return err
	}
	u.Filter.SetDB(db)
	u.logger().Info("webfilter: category database updated", "categories", cats, "entries", db.Len())
	return nil
}
