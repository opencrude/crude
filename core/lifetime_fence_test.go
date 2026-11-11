package core

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/types"
	"crude/workspace"
)

func idleRuntime(t *testing.T) (*Runtime, *actor, *actor) {
	t.Helper()
	r, err := New(func(root string) (*workspace.Workspace, error) {
		return workspace.NewWorkspace(root, "", "tester", types.Settings{}, coreTestLogger{}), nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	a, err := r.workspace(t.Context(), "a", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.workspace(t.Context(), "b", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r.SetOwnerGeneration("owner")
	r.SetLifetimePolicy(true, time.Minute, func() {})
	return r, a, b
}

func TestIdleShutdownRollbackAcrossWorkspaces(t *testing.T) {
	r, a, b := idleRuntime(t)
	if err := b.inspect(t.Context(), func() { b.inFlight = 1 }); err != nil {
		t.Fatal(err)
	}
	result := r.shutdown(t.Context(), nil, coreprotocol.ShutdownRequest{ExpectedOwner: "owner"})
	if result.Accepted || !strings.Contains(result.Reason, b.ws.RootPath) {
		t.Fatal(result)
	}
	if r.draining || r.stopping || len(r.workspaces) != 2 {
		t.Fatal("refusal changed runtime")
	}
	if err := a.inspect(t.Context(), func() {
		if a.idleFenced {
			t.Error("first actor reservation leaked")
		}
	}); err != nil {
		t.Fatal(err)
	}
	if c, err := r.Connect("after-refusal"); err != nil {
		t.Fatal(err)
	} else {
		c.Close()
	}
	if err := b.inspect(t.Context(), func() { b.inFlight = 0 }); err != nil {
		t.Fatal(err)
	}
	result = r.shutdown(t.Context(), nil, coreprotocol.ShutdownRequest{ExpectedOwner: "owner"})
	if !result.Accepted {
		t.Fatal(result)
	}
	if _, err := r.workspace(t.Context(), "new", t.TempDir()); err == nil {
		t.Fatal("factory crossed accepted fence")
	}
}

func TestIdleShutdownRefusesResourceLaneAndReleases(t *testing.T) {
	r, a, _ := idleRuntime(t)
	started, finish := make(chan struct{}), make(chan struct{})
	if !a.resourceLane.submit(func(context.Context) { close(started); <-finish }) {
		t.Fatal("resource not admitted")
	}
	<-started
	result := r.shutdown(t.Context(), nil, coreprotocol.ShutdownRequest{})
	if result.Accepted || !strings.Contains(result.Reason, "resource") {
		close(finish)
		t.Fatal(result)
	}
	close(finish)
	// This barrier sits behind the cleanup callback on the same lane.
	done := make(chan struct{})
	if !a.resourceLane.submit(func(context.Context) { close(done) }) {
		t.Fatal("resource lane leaked stop reservation")
	}
	<-done
	if _, err := r.Connect("still-usable"); err != nil {
		t.Fatal(err)
	}
}

func TestIdleShutdownCancellationRollback(t *testing.T) {
	r, _, _ := idleRuntime(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result := r.shutdown(ctx, nil, coreprotocol.ShutdownRequest{})
	if result.Accepted || r.draining || r.stopping {
		t.Fatal(result)
	}
	if err := r.Each(t.Context(), func(w *workspace.Workspace) {
		u, err := w.BeginIdleLease(t.Context(), workspace.IdleAutomatic)
		if err != nil {
			t.Error(err)
		} else {
			u()
		}
	}); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestIdleShutdownRacesConnectionAdmission(t *testing.T) {
	for i := 0; i < 30; i++ {
		r, err := New(func(string) (*workspace.Workspace, error) { t.Error("factory unexpectedly called"); return nil, nil }, filepath.Join(t.TempDir(), "receipts.db"))
		if err != nil {
			t.Fatal(err)
		}
		r.SetLifetimePolicy(true, time.Minute, func() {})
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var result coreprotocol.ShutdownResult
		var client *Connection
		go func() {
			defer wg.Done()
			<-start
			result = r.shutdown(t.Context(), nil, coreprotocol.ShutdownRequest{})
		}()
		go func() { defer wg.Done(); <-start; client, _ = r.Connect("racing-shell") }()
		close(start)
		wg.Wait()
		if result.Accepted && client != nil {
			t.Fatal("shell and shutdown both admitted")
		}
		if client != nil {
			client.Close()
		}
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// An explicit stop closes a shell that waits at its prompt instead of
// demanding --force, while the automatic idle exit keeps it alive.
func TestExplicitStopAcceptsIdleShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX job control")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is required")
	}
	r, a, _ := idleRuntime(t)
	a.ws.TermMgr.SetSettings(types.TerminalSettings{Shell: types.TerminalShellCustom, Command: bash, Args: []string{"--norc", "--noprofile", "-i"}})
	if _, err := a.ws.Terminals.Create(workspace.TerminalCreateOpts{Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	if r.BeginDrain() {
		t.Fatal("automatic idle exit would close an open shell")
	}
	result := r.shutdown(t.Context(), nil, coreprotocol.ShutdownRequest{})
	if !result.Accepted {
		t.Fatalf("idle shell required --force: %s", result.Reason)
	}
}

// Work that is already finishing gets a short time to settle; the stop does
// not refuse at once and ask the user to retry.
func TestExplicitStopWaitsForSettlingOperation(t *testing.T) {
	r, a, _ := idleRuntime(t)
	if err := a.inspect(t.Context(), func() { a.inFlight = 1 }); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	release, err := reserveStopLease(t.Context(), func() (func(), error) {
		attempts++
		release, err := r.reserveIdle(t.Context(), workspace.IdleExplicitStop)
		if attempts == 1 {
			if !errors.Is(err, types.ErrIdleSettling) {
				t.Fatalf("busy operation was not observed: %v", err)
			}
			if e := a.inspect(t.Context(), func() { a.inFlight = 0 }); e != nil {
				t.Fatal(e)
			}
		}
		return release, err
	})
	if err != nil || attempts != 2 {
		t.Fatalf("settling operation refused the stop: attempts=%d err=%v", attempts, err)
	}
	release()
}
