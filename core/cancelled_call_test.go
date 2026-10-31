package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/document"
	"crude/types"
	"crude/workspace"
)

type cancelledSemanticClient struct {
	semanticLSPClient
	entered chan chan struct{}
	calls   atomic.Int32
	retired atomic.Int32
	stops   atomic.Int32
}

func (c *cancelledSemanticClient) RequestSignatureHelp(ctx context.Context, _ string, _, _ int) (types.LSPSignatureHelp, error) {
	c.calls.Add(1)
	retired := make(chan struct{})
	c.entered <- retired
	<-ctx.Done()
	c.retired.Add(1)
	close(retired)
	return types.LSPSignatureHelp{}, ctx.Err()
}

func (c *cancelledSemanticClient) Stop() error {
	c.stops.Add(1)
	return nil
}

type cancelledSemanticRuntime struct{ client *cancelledSemanticClient }

func (r cancelledSemanticRuntime) NewClient(spec types.LSPServerSpec, root string, admit func() error) workspace.LSPClient {
	(semanticLSPRuntime{&r.client.semanticLSPClient}).NewClient(spec, root, admit)
	return r.client
}

// The real framed core, actor, manager and synchronized document are retained
// throughout. Only the provider is fake: it holds each semantic request until
// its actual provider context is cancelled. More than the sixteen retained-result
// slots must retire, including the cancellation command's own final ACK.
func TestConnectedSemanticCancellationJoinsAndReleasesRetainedResults(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\nvar x = call(1)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	engine := &cancelledSemanticClient{entered: make(chan chan struct{}, 1)}
	managers := make(chan *workspace.LSPManager, 1)
	r, err := New(func(root string) (*workspace.Workspace, error) {
		w := workspace.NewWorkspace(root, "", "tester", types.Settings{}, coreTestLogger{})
		w.LSPMgr.SetRuntime(cancelledSemanticRuntime{engine})
		managers <- w.LSPMgr
		return w, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	client := controlWireClient(t, r, "semantic-cancellation")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var attached coreprotocol.AttachResult
	if err := client.Call(ctx, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root}, &attached); err != nil {
		t.Fatal(err)
	}
	manager := <-managers
	if err := client.Call(ctx, attached.WorkspaceID, coreprotocol.WorkspaceOverridesSet,
		&coreprotocol.WorkspacePreferencesRequest{Edits: []types.SettingEdit{{
			Path: []string{"analysis", "trust"}, After: json.RawMessage(`"project_execution_allowed"`),
		}}}, nil); err != nil {
		t.Fatal(err)
	}
	var source document.Snapshot
	if err := client.Call(ctx, attached.WorkspaceID, coreprotocol.DocumentOpen, &coreprotocol.DocumentOpenRequest{Path: path}, &source); err != nil {
		t.Fatal(err)
	}
	if _, settled, _, err := manager.ProbeDiagnostics(ctx, path); err != nil || !settled {
		t.Fatal("provider document did not synchronize", err)
	}
	owner := client.Owner()
	r.mu.Lock()
	connection := r.clients[owner.ClientID]
	r.mu.Unlock()
	connection.mu.Lock()
	a := connection.attached[attached.WorkspaceID]
	connection.mu.Unlock()
	assertReleased := func() {
		t.Helper()
		connection.commandMu.Lock()
		count, bytes := len(connection.commands), connection.commandBytes
		connection.commandMu.Unlock()
		if count != 0 || bytes != 0 {
			t.Fatalf("unacknowledged read/cancel outcomes retained: %d commands, %d bytes", count, bytes)
		}
		if err := a.inspect(ctx, func() {
			if len(a.jobCancels) != 0 || a.inFlight != 0 {
				t.Errorf("semantic provider job not joined: %d cancels, %d in flight", len(a.jobCancels), a.inFlight)
			}
		}); err != nil {
			t.Fatal(err)
		}
	}
	preCancelled, stop := context.WithCancel(ctx)
	stop()
	if err := client.CallCancellable(preCancelled, attached.WorkspaceID, coreprotocol.LSPRequestSignatureHelp,
		&coreprotocol.LSPRequest{Path: path}, nil); !errors.Is(err, context.Canceled) || engine.calls.Load() != 0 {
		t.Fatal("pre-admission cancellation reached provider", err, engine.calls.Load())
	}
	assertReleased()
	for i := range sessionCommandLimit + 8 {
		requestCtx, stop := context.WithCancel(ctx)
		result := make(chan error, 1)
		go func() {
			result <- client.CallCancellable(requestCtx, attached.WorkspaceID, coreprotocol.LSPRequestSignatureHelp,
				&coreprotocol.LSPRequest{Path: path, Line: 1, Col: 13}, &coreprotocol.LSPResult{})
		}()
		var retired <-chan struct{}
		select {
		case retired = <-engine.entered:
		case <-ctx.Done():
			stop()
			t.Fatalf("query %d did not reach provider: %v", i, ctx.Err())
		}
		start := time.Now()
		stop()
		select {
		case err := <-result:
			var uncertain *coreclient.UncertainError
			if !errors.Is(err, context.Canceled) || errors.As(err, &uncertain) {
				t.Fatalf("query %d did not join exact cancellation: %v", i, err)
			}
		case <-ctx.Done():
			t.Fatalf("query %d cancellation was not joined: %v", i, ctx.Err())
		}
		if time.Since(start) > time.Second {
			t.Fatal("context-aware semantic cancellation was not prompt")
		}
		select {
		case <-retired:
		default:
			t.Fatal("call returned before provider retirement")
		}
		assertReleased()
		if engine.stops.Load() != 0 || !manager.ServerReady() || client.Owner() != owner {
			t.Fatal("cancellation stopped provider or replaced attachment", engine.stops.Load(), client.Owner())
		}
	}
	if engine.calls.Load() != sessionCommandLimit+8 || engine.retired.Load() != engine.calls.Load() {
		t.Fatal("provider request counts differ", engine.calls.Load(), engine.retired.Load())
	}
	// An unrelated ordinary command on the very same attachment still works.
	var status coreprotocol.StatusSnapshot
	if err := client.Call(ctx, attached.WorkspaceID, coreprotocol.StatusSnapshotMethod, &coreprotocol.Empty{}, &status); err != nil {
		t.Fatal("retired reads exhausted the original attachment", err)
	}
	assertReleased()

	// Sixteen simultaneous unacknowledged originals leave no slot for the
	// existing typed cancellation command. Refusal must be explicit uncertainty,
	// not a false join, a larger limit or cancellation of another owner.
	var held []*command
	for i := range sessionCommandLimit - 1 {
		cmd, err := connection.admit(ctx, coreprotocol.Request{ID: fmt.Sprintf("capacity-%d", i),
			Owner: owner, WorkspaceID: attached.WorkspaceID, Method: coreprotocol.StatusSnapshotMethod, Payload: emptyPayload})
		if err != nil {
			t.Fatal(err)
		}
		<-cmd.done
		held = append(held, cmd)
	}
	requestCtx, stop := context.WithCancel(ctx)
	result := make(chan error, 1)
	go func() {
		result <- client.CallCancellable(requestCtx, attached.WorkspaceID, coreprotocol.LSPRequestSignatureHelp,
			&coreprotocol.LSPRequest{Path: path, Line: 1, Col: 13}, nil)
	}()
	retired := <-engine.entered
	stop()
	err = <-result
	var uncertain *coreclient.UncertainError
	var refusal *coreprotocol.Fault
	if !errors.As(err, &uncertain) || !errors.As(err, &refusal) || refusal.Code != "busy" {
		t.Fatal("full retained-command table falsely joined cancellation", err)
	}
	select {
	case <-retired:
		t.Fatal("busy cancellation claimed unperformed provider cancellation")
	default:
	}
	// Exact test cleanup settles the still-live provider explicitly. Production
	// returned its original identity; it did not silently retire this work.
	var original *command
	connection.commandMu.Lock()
	original = connection.commands[commandKey(coreprotocol.Request{ID: uncertain.RequestID, Method: coreprotocol.LSPRequestSignatureHelp})]
	connection.commandMu.Unlock()
	if original == nil {
		t.Fatal("busy cleanup lost the exact retained original")
	}
	if err := a.inspect(ctx, func() {
		cancel := a.jobCancels[jobKey(original.request)]
		if cancel == nil {
			t.Error("unknown original no longer has its provider owner")
			return
		}
		cancel()
	}); err != nil {
		t.Fatal(err)
	}
	<-retired
	<-original.done
	for _, cmd := range append(held, original) {
		connection.forget(cmd)
	}
	assertReleased()
}

func TestCommandAdmissionCallbackWaitsForOriginalEnqueue(t *testing.T) {
	r := transportRuntime(t)
	c, err := r.Connect("admission-fence")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	request := coreprotocol.Request{ID: "held", Owner: c.Owner, Method: coreprotocol.CoreStatusMethod, Payload: emptyPayload}
	held := &command{request: request, enqueued: make(chan struct{}), done: make(chan struct{})}
	c.commands = map[string]*command{commandKey(request): held}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	var callbacks atomic.Int32
	if _, err := c.admit(cancelled, request, func() { callbacks.Add(1) }); !errors.Is(err, context.Canceled) || callbacks.Load() != 0 {
		t.Fatal("exact reattachment published admission before original Dispatch", err, callbacks.Load())
	}
	close(held.enqueued)
	if got, err := c.admit(t.Context(), request, func() { callbacks.Add(1) }); err != nil || got != held || callbacks.Load() != 1 {
		t.Fatal("enqueued exact reattachment lost callback", got, err, callbacks.Load())
	}
	c.forget(held)
	var dispatched *command
	dispatched, err = c.admit(t.Context(), request, func() {
		c.commandMu.Lock()
		original := c.commands[commandKey(request)]
		c.commandMu.Unlock()
		select {
		case <-original.enqueued:
		default:
			t.Error("initial admission callback preceded Dispatch")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	<-dispatched.done
	c.forget(dispatched)
}
