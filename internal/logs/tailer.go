package logs

import (
	"bufio"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"time"
)

// Tailer follows a file like `tail -F`: it waits for the file to appear,
// reads new complete lines, reopens it when it is rotated (renamed and
// recreated) and starts over when it is truncated. The read offset is kept
// in PosFile so a restart continues where it stopped.
type Tailer struct {
	Path    string
	PosFile string        // "" = always start at the end of the file
	Poll    time.Duration // default 500ms

	// SavePosEvery is how many lines may pass between position saves (the
	// position is also saved whenever the file is idle). Default 1000.
	SavePosEvery int
}

// Run calls handle for each new line (without the newline) until ctx ends.
func (t *Tailer) Run(ctx context.Context, handle func(line string)) error {
	poll := t.Poll
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	every := t.SavePosEvery
	if every <= 0 {
		every = 1000
	}
	start, havePos := t.loadPos()
	for {
		f, err := os.Open(t.Path)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			if !sleep(ctx, poll) {
				return nil
			}
			havePos, start = true, 0 // a file that appears later is read from the start
			continue
		}
		pos, err := t.initialPos(f, start, havePos)
		if err != nil {
			f.Close()
			return err
		}
		done, err := t.follow(ctx, f, pos, poll, every, handle)
		f.Close()
		if err != nil || done {
			return err
		}
		start, havePos = 0, true // rotated: the new file is read from the start
	}
}

func (t *Tailer) initialPos(f *os.File, saved int64, havePos bool) (int64, error) {
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	pos := saved
	switch {
	case !havePos:
		pos = st.Size()
	case pos > st.Size():
		pos = 0 // truncated while we were not running
	}
	_, err = f.Seek(pos, io.SeekStart)
	return pos, err
}

// follow reads f until ctx ends (done) or the path now names another file
// (rotation; the rest of f is read first).
func (t *Tailer) follow(ctx context.Context, f *os.File, pos int64, poll time.Duration, every int, handle func(string)) (done bool, err error) {
	r := bufio.NewReaderSize(f, 64<<10)
	var partial strings.Builder
	sinceSave := 0
	for {
		chunk, err := r.ReadString('\n')
		if err == nil {
			partial.WriteString(chunk)
			line := partial.String()
			partial.Reset()
			pos += int64(len(line))
			handle(strings.TrimRight(line, "\r\n"))
			if sinceSave++; sinceSave >= every {
				t.savePos(pos)
				sinceSave = 0
			}
			if ctx.Err() != nil {
				t.savePos(pos)
				return true, nil
			}
			continue
		}
		if !errors.Is(err, io.EOF) {
			return false, err
		}
		partial.WriteString(chunk) // an incomplete last line waits for its newline
		t.savePos(pos)
		sinceSave = 0

		// Idle: has the file been truncated or replaced?
		if cur, err := f.Stat(); err == nil && cur.Size() < pos+int64(partial.Len()) {
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return false, err
			}
			r.Reset(f)
			partial.Reset()
			pos = 0
			continue
		}
		if st, err := os.Stat(t.Path); err == nil {
			if cur, err := f.Stat(); err == nil && !os.SameFile(st, cur) {
				return false, nil // rotated, and the old file is fully read
			}
		}
		if !sleep(ctx, poll) {
			return true, nil
		}
	}
}

func (t *Tailer) loadPos() (int64, bool) {
	if t.PosFile == "" {
		return 0, false
	}
	b, err := os.ReadFile(t.PosFile)
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func (t *Tailer) savePos(pos int64) {
	if t.PosFile == "" {
		return
	}
	tmp := t.PosFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.FormatInt(pos, 10)+"\n"), 0o640); err == nil {
		_ = os.Rename(tmp, t.PosFile)
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
