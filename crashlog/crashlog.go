// Package crashlog owns process-local, synchronous disaster reports. It is
// separate from normal logs and never relies on the UI or core event loop.
package crashlog

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"crude/buildinfo"
)

var active atomic.Pointer[Report]

type Report struct {
	mu          sync.Mutex
	file        *os.File
	path        string
	headerBytes int64
	failed      bool
}

// Start prepares an exclusive private file before either process starts work.
// Runtime fatal output goes directly to its O_SYNC descriptor, including a
// panic in any goroutine. No environment, arguments or draft text is collected.
func Start(home, role string) (*Report, error) {
	if role != "tui" && role != "core" {
		return nil, errors.New("invalid crash report process")
	}
	dir := filepath.Join(home, buildinfo.DataDir(), "crashes")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("crash report folder is not a directory")
	}
	now := time.Now().UTC()
	name := fmt.Sprintf("%s-%s-%d-%s.log", role, now.Format("20060102T150405.000000000Z"), os.Getpid(), rand.Text())
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_SYNC, 0600)
	if err != nil {
		return nil, err
	}
	r := &Report{file: f, path: path}
	header := fmt.Sprintf("Crude %s crash report\nStarted: %s\nPID: %d\nBuild: %s\nSource: %s\nRuntime: %s %s/%s\n\n",
		role, now.Format(time.RFC3339Nano), os.Getpid(), buildinfo.ExecutableID(), buildinfo.ProtocolBuild, runtime.Version(), runtime.GOOS, runtime.GOARCH)
	if _, err = f.WriteString(header); err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = debug.SetCrashOutput(f, debug.CrashOptions{})
	}
	if err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, err
	}
	r.headerBytes = int64(len(header))
	debug.SetTraceback("all")
	active.Store(r)
	return r, nil
}

// Recovered records the original panic stack before a framework attempts
// cleanup. The disk report is complete even if terminal teardown then stalls.
func Recovered(value any, stack []byte) {
	if r := active.Load(); r != nil {
		r.record(value, stack)
	}
}

func (r *Report) record(value any, stack []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return
	}
	r.failed = true
	if _, err := fmt.Fprintf(r.file, "Panic at %s: %v\n%s\n", time.Now().UTC().Format(time.RFC3339Nano), value, stack); err != nil {
		fmt.Fprintf(os.Stderr, "Could not write crash report %s: %v\n", r.path, err)
	}
	if err := r.file.Sync(); err != nil {
		fmt.Fprintf(os.Stderr, "Could not save crash report %s: %v\n", r.path, err)
	}
}

// Finish must be deferred at the process entry point. Do not remove or disable
// fatal output while unwinding a main-goroutine panic. Clean runs leave no
// crash log; a recovered panic or already-written runtime report is retained.
func (r *Report) Finish() {
	if value := recover(); value != nil {
		r.record(value, debug.Stack())
		panic(value)
	}
	active.CompareAndSwap(r, nil)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return
	}
	info, err := r.file.Stat()
	_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
	_ = r.file.Close()
	r.file = nil
	if err == nil && !r.failed && info.Size() == r.headerBytes {
		_ = os.Remove(r.path)
	}
}
