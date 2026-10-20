package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/fileio"
	"crude/worktree"
)

// Re-exec the native test binary so Linux PTY and Windows ConPTY exercise the
// same real, stdin-blocked process, without shell startup files or timed sleeps.
func TestExecutionTerminalCleanupPTYChild(t *testing.T) {
	if len(os.Args) == 0 || os.Args[len(os.Args)-1] != "execution-cleanup-pty-child" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func executionTerminalCleanupOptions(t *testing.T, root string) TerminalCreateOpts {
	t.Helper()
	command, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return TerminalCreateOpts{Command: command, Args: []string{"-test.run=^TestExecutionTerminalCleanupPTYChild$", "--", "execution-cleanup-pty-child"}, CWD: root, Cols: 40, Rows: 10}
}

func awaitExecutionTerminal[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(10 * time.Second):
		t.Fatal("PTY admission/cleanup did not reach its channel boundary")
		var zero T
		return zero
	}
}

func executionTerminalRelease(t *testing.T) (<-chan struct{}, func()) {
	t.Helper()
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	return release, unblock
}

func assertExecutionTerminalLeaseHeld(t *testing.T) {
	t.Helper()
	if unlock, available := fileio.TryLockMutation(); available {
		unlock()
		t.Fatal("PTY admission/cleanup did not hold the shared mutation lease")
	}
}

func executionTerminalRetention(manager *TerminalManager, root string) error {
	for _, info := range manager.List() {
		if info.Alive && info.CWD == root {
			return fmt.Errorf("live PTY %s retains checkout", info.ID)
		}
	}
	return nil
}

func TestExecutionTerminalAdmissionVsCleanup(t *testing.T) {
	t.Run("terminal_wins", func(t *testing.T) {
		service, _, root := executionFixture(t)
		c := executionCreate(t, service)
		if err := service.Unpin(c.ID, c.Owner); err != nil {
			t.Fatal(err)
		}
		manager := NewTerminalManager(root)
		t.Cleanup(manager.CloseAll)
		prepared := make(chan TerminalInfo, 1)
		release, unblock := executionTerminalRelease(t)
		type terminalResult struct {
			info TerminalInfo
			err  error
		}
		admitted := make(chan terminalResult, 1)
		opts := executionTerminalCleanupOptions(t, c.Root)
		go func() {
			info, err := manager.CreatePrepared(opts, func(info TerminalInfo) error {
				prepared <- info
				<-release
				return nil
			})
			admitted <- terminalResult{info, err}
		}()
		info := awaitExecutionTerminal(t, prepared)
		if info.PID <= 0 || !info.Alive || info.CWD != c.Root {
			t.Fatalf("fixture did not admit a real PTY: %+v", info)
		}
		assertExecutionTerminalLeaseHeld(t)
		started, cleaned := make(chan struct{}), make(chan error, 1)
		go func() {
			close(started)
			cleaned <- service.Cleanup(t.Context(), c.ID, c.Owner, worktree.CleanupOptions{Check: func(context.Context) error {
				return executionTerminalRetention(manager, c.Root)
			}})
		}()
		awaitExecutionTerminal(t, started)
		unblock()
		result := awaitExecutionTerminal(t, admitted)
		if result.err != nil || result.info.ID != info.ID {
			t.Fatalf("real PTY admission failed: %+v", result)
		}
		if err := awaitExecutionTerminal(t, cleaned); err == nil || !strings.Contains(err.Error(), "live PTY") {
			t.Fatalf("cleanup ignored admitted PTY: %v", err)
		}
		retained, err := service.Inspect(c.ID)
		if err != nil || retained.Removed {
			t.Fatalf("live terminal checkout removed: %+v %v", retained, err)
		}
		if got, ok := manager.Get(info.ID); !ok || !got.Alive || got.PID != info.PID {
			t.Fatalf("cleanup altered live terminal: %+v found=%t", got, ok)
		}
		if text, err := os.ReadFile(filepath.Join(c.Root, "disk.txt")); err != nil || string(text) != "original\n" {
			t.Fatalf("cleanup changed retained checkout content: %q %v", text, err)
		}
		manager.Close(info.ID)
		if err := service.Cleanup(t.Context(), c.ID, c.Owner, worktree.CleanupOptions{Check: func(context.Context) error {
			return executionTerminalRetention(manager, c.Root)
		}}); err != nil {
			t.Fatalf("explicit terminal close did not release cleanup: %v", err)
		}
		if _, err := os.Stat(c.Root); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("settled terminal checkout remains: %v", err)
		}
	})

	t.Run("cleanup_wins", func(t *testing.T) {
		service, _, root := executionFixture(t)
		c := executionCreate(t, service)
		if err := service.Unpin(c.ID, c.Owner); err != nil {
			t.Fatal(err)
		}
		manager := NewTerminalManager(root)
		t.Cleanup(manager.CloseAll)
		checked := make(chan struct{})
		release, unblock := executionTerminalRelease(t)
		cleaned := make(chan error, 1)
		go func() {
			cleaned <- service.Cleanup(t.Context(), c.ID, c.Owner, worktree.CleanupOptions{Check: func(context.Context) error {
				if err := executionTerminalRetention(manager, c.Root); err != nil {
					return err
				}
				close(checked)
				<-release
				return nil
			}})
		}()
		awaitExecutionTerminal(t, checked)
		assertExecutionTerminalLeaseHeld(t)
		var prepared atomic.Bool
		started, admitted := make(chan struct{}), make(chan error, 1)
		opts := executionTerminalCleanupOptions(t, c.Root)
		go func() {
			close(started)
			_, err := manager.CreatePrepared(opts, func(TerminalInfo) error { prepared.Store(true); return nil })
			admitted <- err
		}()
		awaitExecutionTerminal(t, started)
		unblock()
		if err := awaitExecutionTerminal(t, cleaned); err != nil {
			t.Fatal(err)
		}
		if err := awaitExecutionTerminal(t, admitted); err == nil {
			t.Fatal("PTY started successfully in a removed checkout")
		}
		if prepared.Load() || len(manager.List()) != 0 {
			t.Fatal("rejected PTY leaked a process/resource acknowledgement")
		}
		removed, err := service.Inspect(c.ID)
		if err != nil || !removed.Removed {
			t.Fatalf("cleanup did not finish: %+v %v", removed, err)
		}
		if _, err := os.Stat(c.Root); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("removed checkout remains: %v", err)
		}
	})
}
