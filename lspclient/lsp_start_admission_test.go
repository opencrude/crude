package lspclient

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestLSPCancelledStartNeverAttemptsProgramConstruction(t *testing.T) {
	for _, waitingOnOwner := range []bool{false, true} {
		t.Run(map[bool]string{false: "already-cancelled", true: "cancelled-waiting-for-owner"}[waitingOnOwner], func(t *testing.T) {
			// This exact nonexistent operand makes any constructor attempt
			// independently observable as an exec/path error, without spawning a
			// process or relying on whether a killed script managed to write a marker.
			l := NewLSPIntegration(filepath.Join(t.TempDir(), "must-not-be-resolved"), nil, "file:///unused")
			ctx, cancel := context.WithCancel(t.Context())
			result := make(chan error, 1)
			if waitingOnOwner {
				l.mu.Lock()
				entered := make(chan struct{})
				go func() {
					close(entered)
					result <- l.Start(ctx)
				}()
				<-entered
				cancel()
				l.mu.Unlock()
			} else {
				cancel()
				result <- l.Start(ctx)
			}
			if err := <-result; !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled startup reached program construction: %v", err)
			}
			l.mu.Lock()
			if l.launch != nil || l.client != nil || l.pipe != nil {
				t.Error("cancelled startup installed a producer lifetime")
			}
			l.mu.Unlock()
		})
	}
}

func TestLSPDeniedLaunchEpochNeverAttemptsProgramConstruction(t *testing.T) {
	denied := errors.New("workspace analysis launch epoch was revoked")
	calls := 0
	l := NewLSPIntegration(filepath.Join(t.TempDir(), "must-not-be-resolved"), nil, "file:///unused",
		WithStartAdmission(func() error { calls++; return denied }))
	if err := l.Start(t.Context()); !errors.Is(err, denied) {
		t.Fatalf("revoked epoch reached program construction: %v", err)
	}
	if calls != 1 {
		t.Fatalf("launch admission observed %d times, expected exactly once", calls)
	}
	if err := l.Stop(); err != nil {
		t.Fatal(err)
	}
}
