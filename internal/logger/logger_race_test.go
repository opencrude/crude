package logger

import (
	"sync"
	"testing"
)

// TestLoggerCloseConcurrentWithWrite is a permanent race regression for the
// session-close data race: Session.Close() → Logger.Close() ran concurrently
// with a background orchestrator goroutine calling Logger.Info(). write()
// read the closed flag WITHOUT the logger mutex while Close() wrote it WITH
// the mutex (crude/session TestNewWithSessionDirEmptyIDUsesLoggerSessionID
// caught it intermittently under -race). Run with `go test -race`.
func TestLoggerCloseConcurrentWithWrite(t *testing.T) {
	l, err := New(Options{HomeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					l.Info("RACE", "tick", "i", 1)
				}
			}
		}()
	}

	// Close while the writers are mid-flight; this is the racing window.
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	close(stop)
	wg.Wait()

	// Second close must be a no-op (idempotent), and any straggler write
	// must not panic or deadlock.
	if err := l.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

// TestLoggerWriteAfterCloseIsSafe verifies writes after Close are dropped
// without panicking (the orchestrator may log after the session ended).
func TestLoggerWriteAfterCloseIsSafe(t *testing.T) {
	l, err := New(Options{HomeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	l.Info("SESSION", "late", "after", "close")
	l.Error("SESSION", "late", "after", "close")
	l.Warn("SESSION", "late", "after", "close")
	l.Debug("SESSION", "late", "after", "close")
}
