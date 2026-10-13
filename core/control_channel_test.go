package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/types"
	"crude/types/control"
	"crude/workspace"
)

func shellEnvelope(id, ws string, owner coreprotocol.Owner) coreprotocol.ControlEnvelope {
	s := control.ShellOwner{ClientID: owner.ClientID, Epoch: owner.Epoch}
	schema := control.Schema{ID: "empty", Revision: 1, Document: json.RawMessage(`{}`)}
	return coreprotocol.ControlEnvelope{
		Request:    control.Request{ID: id, CommandID: "shell.inspect", Caller: control.Caller{Kind: control.ExternalCaller, ClientID: "caller"}, Target: control.Target{WorkspaceID: ws, Shell: &s}, Input: json.RawMessage(`{}`)},
		Descriptor: control.Descriptor{ID: "shell.inspect", Scope: control.Shell, Effect: control.Read, Input: schema, Output: schema, Availability: control.Availability{Available: true}},
	}
}

func controlWireClient(t *testing.T, r *Runtime, id string) *coreclient.Client {
	t.Helper()
	a, b := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- ServeConnection(context.Background(), r, b) }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := coreclient.Dial(ctx, a, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		select {
		case <-done:
		case <-cleanupCtx.Done():
			t.Error("control server did not close")
		}
	})
	return c
}

func TestControlWireExactTargetAndImmutableOutcome(t *testing.T) {
	r, err := New(func(root string) (*workspace.Workspace, error) {
		return workspace.NewWorkspace(root, "model", "tester", types.Settings{}, coreTestLogger{}), nil
	}, t.TempDir()+"/receipts.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	a, b := controlWireClient(t, r, "shell-a"), controlWireClient(t, r, "shell-b")
	root := t.TempDir()
	var attached coreprotocol.AttachResult
	if err := a.Call(ctx, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &attached); err != nil {
		t.Fatal(err)
	}
	ws := attached.WorkspaceID
	if err := b.Call(ctx, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, nil); err != nil {
		t.Fatal(err)
	}
	e := shellEnvelope("inspect-1", ws, a.Owner())
	// Immutable mutation receipts must survive repeated observation/reply.
	// Read-only inspection instead refreshes and releases its bounded evidence.
	e.Request.CommandID, e.Descriptor.ID = "tree.set_visible", "tree.set_visible"
	e.Descriptor.Effect = control.Write
	if _, err := r.RequestShellControl(ctx, ws, *e.Request.Target.Shell, e); err == nil {
		t.Fatal("unregistered target accepted")
	}
	for _, c := range []*coreclient.Client{a, b} {
		if err := c.RegisterControlShell(ctx, ws); err != nil {
			t.Fatal(err)
		}
	}
	resultCh := make(chan control.Result, 1)
	errCh := make(chan error, 1)
	go func() {
		out, err := r.RequestShellControl(ctx, ws, *e.Request.Target.Shell, e)
		resultCh <- out
		errCh <- err
	}()
	select {
	case got := <-a.ControlRequests():
		if got.Request.ID != e.Request.ID || got.Request.Target.WorkspaceID != ws {
			t.Fatal(got)
		}
	case <-ctx.Done():
		t.Fatal("target did not receive")
	}
	select {
	case <-b.ControlRequests():
		t.Fatal("control broadcast to foreign shell")
	default:
	}
	result := control.Result{RequestID: e.Request.ID, OperationID: "op-1", State: control.Applied, Output: json.RawMessage(`{"observed":true}`)}
	if err := b.Call(ctx, ws, coreprotocol.ControlShellReply, result, nil); err == nil {
		t.Fatal("foreign reply accepted")
	}
	if err := a.ReplyControl(ctx, ws, result); err != nil {
		t.Fatal(err)
	}
	if got := <-resultCh; got.State != control.Applied || got.OperationID != "op-1" {
		t.Fatal(got)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if err := a.ReplyControl(ctx, ws, result); err != nil {
		t.Fatal("duplicate exact reply rejected", err)
	}
	changed := result.Clone()
	changed.OperationID = "changed"
	if err := a.Call(ctx, ws, coreprotocol.ControlShellReply, changed, nil); err == nil {
		t.Fatal("changed reply accepted")
	}
	recovered, err := r.RequestShellControl(ctx, ws, *e.Request.Target.Shell, e)
	if err != nil || recovered.OperationID != "op-1" {
		t.Fatal(recovered, err)
	}
	select {
	case <-a.ControlRequests():
		t.Fatal("completed effect replayed")
	default:
	}
	e.Request.Input = json.RawMessage(`{"changed":true}`)
	if _, err := r.RequestShellControl(ctx, ws, *e.Request.Target.Shell, e); err == nil {
		t.Fatal("changed request preimage accepted")
	}
	e = shellEnvelope("stale", ws, a.Owner())
	e.Request.Target.Shell.Epoch++
	if _, err := r.RequestShellControl(ctx, ws, *e.Request.Target.Shell, e); err == nil {
		t.Fatal("stale epoch accepted")
	}
	if err := a.Call(ctx, ws, coreprotocol.Detach, struct{}{}, nil); err != nil {
		t.Fatal(err)
	}
	e = shellEnvelope("after-switch", ws, a.Owner())
	if _, err := r.RequestShellControl(ctx, ws, *e.Request.Target.Shell, e); err == nil {
		t.Fatal("old workspace target accepted after detach")
	}
}

func TestControlBoundsCancellationAndClose(t *testing.T) {
	r := transportRuntime(t)
	c, err := r.Connect("bounded-shell")
	if err != nil {
		t.Fatal(err)
	}
	// This fixture exercises the transport-only lifecycle without constructing
	// mutable workspace services; the real attach/wire journey is covered above.
	c.mu.Lock()
	c.attached["ws"] = &actor{}
	c.controlRegistered["ws"] = true
	c.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan control.Result, 16)
	for i := range 16 {
		e := shellEnvelope(fmt.Sprint("request-", i), "ws", c.Owner)
		go func() { out, _ := r.RequestShellControl(ctx, "ws", *e.Request.Target.Shell, e); results <- out }()
		<-c.Controls() // precise admission barrier, no sleeps/polling
	}
	e := shellEnvelope("overflow", "ws", c.Owner)
	if _, err := r.RequestShellControl(context.Background(), "ws", *e.Request.Target.Shell, e); err == nil {
		t.Fatal("outstanding bound ignored")
	}
	cancel()
	for range 16 {
		if got := <-results; got.State != control.Uncertain {
			t.Fatal(got)
		}
	}
	c.mu.Lock()
	outstanding := c.controlOutstanding
	c.mu.Unlock()
	if outstanding != 0 {
		t.Fatal("cancelled requests still pending", outstanding)
	}
	e = shellEnvelope("oversized-id", "ws", c.Owner)
	e.Request.ID = strings.Repeat("x", 257)
	if _, err := r.RequestShellControl(context.Background(), "ws", *e.Request.Target.Shell, e); err == nil {
		t.Fatal("neutral oversized ID reached transport")
	}
	e = shellEnvelope("bytes", "ws", c.Owner)
	c.mu.Lock()
	c.controlBytes = coreprotocol.ControlMaxQueuedBytes
	c.mu.Unlock()
	if _, err := r.RequestShellControl(context.Background(), "ws", *e.Request.Target.Shell, e); err == nil {
		t.Fatal("aggregate byte bound ignored")
	}
	c.mu.Lock()
	c.controlBytes = 0
	c.mu.Unlock()
	go func() {
		out, _ := r.RequestShellControl(context.Background(), "ws", *e.Request.Target.Shell, e)
		results <- out
	}()
	<-c.Controls()
	c.mu.Lock()
	c.attached = make(map[string]*actor)
	c.mu.Unlock() // no service teardown for fixture
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if got := <-results; got.State != control.Uncertain {
		t.Fatal("shutdown lost request", got)
	}
}

func TestControlPendingDetachAndDisconnect(t *testing.T) {
	for _, mode := range []string{"detach", "disconnect", "cancel-queued"} {
		t.Run(mode, func(t *testing.T) {
			r := transportRuntime(t)
			c, err := r.Connect("shell")
			if err != nil {
				t.Fatal(err)
			}
			c.mu.Lock()
			c.attached["ws"] = &actor{}
			c.controlRegistered["ws"] = true
			c.mu.Unlock()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			e := shellEnvelope("pending", "ws", c.Owner)
			out := make(chan control.Result, 1)
			go func() { result, _ := r.RequestShellControl(ctx, "ws", *e.Request.Target.Shell, e); out <- result }()
			admitted := <-c.Controls()
			c.mu.Lock()
			if mode == "cancel-queued" {
				c.controls <- admitted
			}
			if mode == "detach" {
				c.detachControlsLocked("ws")
			}
			// Prevent fixture-only actor teardown; no workspace service exists.
			c.attached = make(map[string]*actor)
			c.mu.Unlock()
			switch mode {
			case "disconnect":
				c.Close()
			case "cancel-queued":
				cancel()
			}
			if result := <-out; result.State != control.Uncertain {
				t.Fatal(result)
			}
			if c.ControlPending(admitted) {
				t.Fatal("terminal request remained deliverable")
			}
			select {
			case <-c.Controls():
				t.Fatal("cancelled backlog was not removed")
			default:
			}
			c.Close()
		})
	}
}
