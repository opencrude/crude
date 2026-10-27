package app

import (
	"context"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"crude/coreclient"
	"crude/history"
	"crude/shellport/remote"
	"crude/tui"
	"crude/tui/viewmodel"
)

// Only the caller drives screenshots, through scripts/tuishot.sh (never
// concurrent captures). The controller still submits via the production framed
// core protocol; the real TUI renders the same retained session and event log.
func startLiveWaitVisual(t *testing.T, ctx context.Context, project, parent string, dial func(context.Context, string) (*coreclient.Client, error)) func() {
	t.Helper()
	if os.Getenv("CRUDE_VISUAL_WAIT") != "1" {
		return func() {}
	}
	port, err := remote.Attach(ctx, remote.Options{Root: project, Dial: dial})
	if err != nil {
		t.Fatal("cannot connect visual shell to private wait core")
	}
	t.Cleanup(func() { _ = port.Close() })
	terminal, err := tui.Start(port, providerPresets(), history.New(), &appLogger{}, func(a *viewmodel.App) error {
		a.AddOrchestrator(parent)
		return nil
	})
	if err != nil {
		t.Fatal("cannot start private wait visual shell")
	}
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			terminal.Stop()
			done := make(chan struct{})
			go func() { terminal.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("private wait visual shell did not shut down cleanly")
				terminal.Program.Kill()
				_ = port.Close()
				<-done
			}
		})
	}
	t.Cleanup(stop)
	port.StartReplica()
	return func() {
		// Stop and flush the shell while the framed transport is still alive.
		// The caller cancels the journey context after this deferred callback.
		defer stop()
		if t.Failed() {
			return
		}
		seconds, err := strconv.Atoi(os.Getenv("CRUDE_WAIT_VISUAL_HOLD_SECONDS"))
		if err != nil || seconds <= 0 {
			return
		}
		if seconds > 120 {
			seconds = 120
		}
		timer := time.NewTimer(time.Duration(seconds) * time.Second)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			t.Error("private wait visual hold exceeded journey deadline")
		}
	}
}
