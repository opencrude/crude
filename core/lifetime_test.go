package core

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/workspace"
)

func TestInspectionCancellationOwnsCapturedResultLifetime(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(map[bool]string{false: "queued", true: "running"}[running], func(t *testing.T) {
			a := &actor{commands: make(chan actorCommand, 1), stop: make(chan struct{})}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			var result int
			finished := make(chan error, 1)
			go func() { finished <- a.inspect(ctx, func() { close(entered); <-release; result = 7 }) }()
			var command actorCommand
			select {
			case command = <-a.commands:
			case <-time.After(time.Second):
				t.Fatal("inspection not admitted")
			}
			if running {
				go command.inspect()
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("inspection did not start")
				}
			}
			cancel()
			if running {
				select {
				case <-finished:
					close(release)
					t.Fatal("cancel returned while a running callback still owned the captured result")
				case <-time.After(20 * time.Millisecond):
				}
				close(release)
			} else {
				close(release)
			}
			select {
			case err := <-finished:
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("inspection cancellation did not finish")
			}
			if !running {
				command.inspect()
				if result != 0 {
					t.Fatal("cancelled queued callback changed result after return")
				}
			} else if result != 7 {
				t.Fatal("running callback was not joined")
			}
		})
	}
}

func TestHandoverShutdownFencesAdmissionBeforeAcknowledgement(t *testing.T) {
	r, err := New(func(string) (*workspace.Workspace, error) { t.Fatal("unexpected factory"); return nil, nil }, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	r.SetOwnerGeneration("incarnation")
	stopped := make(chan struct{})
	r.SetLifetimePolicy(true, time.Minute, func() { close(stopped) })
	c, err := r.Connect("stopper")
	if err != nil {
		t.Fatal(err)
	}
	response := r.coreMethod(context.Background(), c, coreprotocol.Request{ID: "stop", Owner: c.Owner, Method: coreprotocol.CoreShutdownMethod, Payload: mustTestPayload(coreprotocol.ShutdownRequest{ExpectedOwner: "incarnation"})})
	var result coreprotocol.ShutdownResult
	if err := testDecode(response.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Accepted {
		t.Fatalf("idle owner refused: %+v", result)
	}
	if _, err := r.Connect("racing-shell"); err == nil {
		t.Fatal("acknowledged shutdown admitted a shell")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("shutdown callback missing")
	}
}

func TestHandoverShutdownRefusesChangedOwnerOrAdmission(t *testing.T) {
	for _, mode := range []string{"generation", "attached", "opening", "inspection"} {
		t.Run(mode, func(t *testing.T) {
			r, err := New(func(string) (*workspace.Workspace, error) { return nil, nil }, filepath.Join(t.TempDir(), "receipts.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = r.Close() }()
			r.SetOwnerGeneration("incarnation")
			r.SetLifetimePolicy(true, time.Minute, func() { t.Error("refused shutdown invoked callback") })
			c, err := r.Connect("stopper")
			if err != nil {
				t.Fatal(err)
			}
			expected := "incarnation"
			ctx := context.Background()
			switch mode {
			case "generation":
				expected = "previous"
			case "attached":
				if _, err := r.Connect("shell"); err != nil {
					t.Fatal(err)
				}
			case "opening":
				r.creating["opening"] = nil
				defer delete(r.creating, "opening")
			case "inspection":
				stop := make(chan struct{})
				r.workspaces["blocked"] = &actor{id: "blocked", commands: make(chan actorCommand), stop: stop}
				defer delete(r.workspaces, "blocked")
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			response := r.coreMethod(ctx, c, coreprotocol.Request{ID: "stop", Owner: c.Owner, Method: coreprotocol.CoreShutdownMethod, Payload: mustTestPayload(coreprotocol.ShutdownRequest{ExpectedOwner: expected})})
			var result coreprotocol.ShutdownResult
			if err := testDecode(response.Payload, &result); err != nil {
				t.Fatal(err)
			}
			if result.Accepted || r.draining {
				t.Fatalf("unsafe stop accepted: %+v draining=%v", result, r.draining)
			}
		})
	}
}
