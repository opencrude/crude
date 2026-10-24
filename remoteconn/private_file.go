// Package remoteconn owns client-machine remote connections and their secrets.
// It has no dependency on the selected core, workspace, or presentation layer.
package remoteconn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"crude/fileio"
	"github.com/gofrs/flock"
)

const maxDocumentBytes = 4 << 20

// PrivateFile serializes private, current-format JSON documents across handles
// and processes. The destination directory must be owned by the current user.
// Existing symlinks, non-regular files, and broad POSIX modes fail closed.
type PrivateFile struct {
	path string
	mu   sync.Mutex
}

func OpenPrivateFile(path string) (*PrivateFile, error) {
	if path == "" {
		return nil, errors.New("remoteconn: private path required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return nil, err
	}
	if err = privateMode(filepath.Dir(abs), true); err != nil {
		return nil, err
	}
	return &PrivateFile{path: abs}, nil
}

func privateMode(path string, dir bool) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.IsDir() != dir || (!dir && !fi.Mode().IsRegular()) || fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("remoteconn: private storage must not be a symlink or special file")
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0077 != 0 {
		return errors.New("remoteconn: private storage permissions must be owner-only")
	}
	return nil
}

// Update invokes fn under the file lock with a detached document (nil if absent).
// A nil replacement means read-only. Returned errors never include document data.
// fileio.WriteCommitted distinguishes a committed directory-sync warning.
func (f *PrivateFile) Update(ctx context.Context, fn func([]byte) ([]byte, error)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := privateMode(filepath.Dir(f.path), true); err != nil {
		return err
	}
	lockPath := f.path + ".lock"
	h, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err == nil {
		err = h.Close()
	} else if errors.Is(err, os.ErrExist) {
		err = nil
	}
	if err != nil {
		return err
	}
	if err = privateMode(lockPath, false); err != nil {
		return err
	}
	lock := flock.New(lockPath)
	locked, err := lock.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil {
		return err
	}
	if !locked {
		return ctx.Err()
	}
	defer func() { _ = lock.Unlock() }()
	var data []byte
	if err = privateMode(f.path, false); err == nil {
		h, e := os.Open(f.path)
		if e != nil {
			return e
		}
		data, err = io.ReadAll(io.LimitReader(h, maxDocumentBytes+1))
		closeErr := h.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		if len(data) > maxDocumentBytes {
			return errors.New("remoteconn: private document too large")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	next, err := fn(data)
	if err != nil || next == nil {
		return err
	}
	if len(next) > maxDocumentBytes {
		return errors.New("remoteconn: private document too large")
	}
	return fileio.WriteAtomicChecked(f.path, next, 0600, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := privateMode(f.path, false)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	})
}

// DecodeDocument refuses unknown fields, trailing bytes, and corrupt documents
// without returning JSON parser errors that could quote a credential.
func DecodeDocument(data []byte, out any) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("remoteconn: invalid current-format document")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(out) != nil {
		return errors.New("remoteconn: invalid current-format document")
	}
	var trailing any
	if dec.Decode(&trailing) != io.EOF {
		return errors.New("remoteconn: invalid current-format document")
	}
	return nil
}

func documentVersion(version int) error {
	if version != 1 {
		return fmt.Errorf("remoteconn: unsupported document version %d", version)
	}
	return nil
}
