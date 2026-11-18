package toolstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
)

// AcquireExecutable pins a recognized managed executable until release is
// called. The caller must retain the lease through process startup and release
// only after the exact process Stop joins successfully. release is idempotent.
// Paths outside the store are unmanaged. Paths inside it fail closed unless
// their exact confirmed, indexed tool identity and executable still exist.
// Never acquire or release while holding a language-server manager lock.
func (s *Store) AcquireExecutable(ctx context.Context, executable string) (release func(), managed bool, err error) {
	if !s.containsExecutablePath(executable) {
		return nil, false, ctx.Err()
	}
	if err := s.mu.LockContext(ctx); err != nil {
		return nil, true, err
	}
	defer s.mu.Unlock()
	tool, err := s.executableLocked(ctx, executable)
	if err != nil {
		return nil, true, err
	}
	s.executableLeases[tool.ID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.executableLeases[tool.ID]--
			if s.executableLeases[tool.ID] == 0 {
				delete(s.executableLeases, tool.ID)
			}
		})
	}, true, nil
}

// MarkExecutableUsedContext records a managed launch without exposing the
// store's on-disk identity layout to the caller. The same short admission and
// cancellation contract as MarkUsedContext applies. A lease must remain held
// for the executable while its process is running.
func (s *Store) MarkExecutableUsedContext(ctx context.Context, executable, workspace string, admit func(context.Context) error) error {
	if strings.TrimSpace(workspace) == "" {
		return errors.New("workspace is empty")
	}
	if err := s.mu.LockContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	tool, err := s.executableLocked(ctx, executable)
	if err != nil {
		return err
	}
	return s.markUsedLocked(ctx, tool, workspace, admit)
}

func (s *Store) containsExecutablePath(executable string) bool {
	if !filepath.IsAbs(executable) {
		return false
	}
	rel, err := filepath.Rel(s.root, filepath.Clean(executable))
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}

func sameExecutablePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// executableLocked verifies current disk metadata under the same owner as
// removal. It does not adopt a new identity or accept another executable in a
// tool's directory. External filesystem mutation is not a security sandbox.
func (s *Store) executableLocked(ctx context.Context, executable string) (Tool, error) {
	for id, tool := range s.index {
		if !sameExecutablePath(tool.Executable, executable) {
			continue
		}
		if s.unconfirmed[id] {
			break
		}
		current, err := s.readToolContext(ctx, tool.Package, filepath.Base(tool.Dir))
		if err != nil {
			return Tool{}, fmt.Errorf("verify managed executable: %w", err)
		}
		if !reflect.DeepEqual(current, tool) {
			return Tool{}, errors.New("the managed tool identity changed; check the installed tool before starting it")
		}
		info, err := os.Lstat(tool.Executable)
		if err != nil {
			return Tool{}, fmt.Errorf("verify managed executable: %w", err)
		}
		if !info.Mode().IsRegular() {
			return Tool{}, errors.New("the managed executable is not a regular file")
		}
		if err := ctx.Err(); err != nil {
			return Tool{}, err
		}
		return tool, nil
	}
	return Tool{}, fmt.Errorf("the managed executable is missing or its publication needs inspection: %s", executable)
}
