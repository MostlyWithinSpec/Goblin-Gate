package apply

import (
	"context"
	"fmt"
	"sync"
)

// Applier applies rendered files to the system.
type Applier interface {
	Apply(ctx context.Context, files map[string][]byte) error
}

// Fake implements Applier for tests and --dev mode. It records the last set
// of files it was given and can be told to fail.
type Fake struct {
	mu           sync.Mutex
	files        map[string][]byte
	calls        int
	failApply    bool
	failNext     int
	failApplyErr error
}

// NewFake creates a new fake applier.
func NewFake() *Fake {
	return &Fake{files: make(map[string][]byte)}
}

// Apply records the files it was given, or fails when configured to.
func (f *Fake) Apply(ctx context.Context, files map[string][]byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.failApply || f.failNext > 0 {
		if f.failNext > 0 {
			f.failNext--
		}
		err := f.failApplyErr
		if err == nil {
			err = fmt.Errorf("injected failure")
		}
		return fmt.Errorf("fake apply failed: %w", err)
	}
	f.files = make(map[string][]byte, len(files))
	for path, content := range files {
		f.files[path] = append([]byte(nil), content...)
	}
	return nil
}

// Files returns a copy of the files from the last successful Apply.
func (f *Fake) Files() map[string][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make(map[string][]byte, len(f.files))
	for path, content := range f.files {
		result[path] = append([]byte(nil), content...)
	}
	return result
}

// Calls returns how many times Apply was called.
func (f *Fake) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// SetFailApply makes every Apply fail with err until ClearFailApply.
func (f *Fake) SetFailApply(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failApply = true
	f.failApplyErr = err
}

// FailNext makes only the next n Apply calls fail with err.
func (f *Fake) FailNext(n int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failNext = n
	f.failApplyErr = err
}

// ClearFailApply clears all injected failures.
func (f *Fake) ClearFailApply() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failApply = false
	f.failNext = 0
	f.failApplyErr = nil
}
