package core

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/types"
	"crude/workspace"
)

// A user presses Stop repeatedly while a shell's view lags the core: every
// press after the first reaches the core once the turn already ended. The turn
// must stop on the first press and the agent must end idle, never left in a
// stopping boundary that no terminal acknowledgement will ever clear.
func TestRepeatedStopEndsIdleNotStopping(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var startedOnce, releaseOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"held turn\"}}]}\n\n")
		w.(http.Flusher).Flush()
		startedOnce.Do(func() { close(started) })
		select {
		case <-request.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(server.Close)
	r, root := featureRuntime(t, func(ws *workspace.Workspace) {
		app, cfg := inputResetConfig(server.URL)
		ws.SetAppDefaults(app)
		ws.SetBackendConfig(cfg, t.TempDir())
	})
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	wire := newResourceWire(t, r, "stop-author")
	var attached coreprotocol.AttachResult
	wire.call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &attached)
	ws := attached.WorkspaceID
	var created workspace.NewSessionResult
	wire.call(t, ws, coreprotocol.NewSession, coreprotocol.InvocationSessionRequest{}, &created)
	var admitted coreprotocol.InputResult
	featureCall(t, wire, ws, "turn", coreprotocol.SubmitInput, coreprotocol.InputRequest{AgentID: created.OrchID, Text: "Hold this turn", DraftRevision: 1}, &admitted)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("turn never reached the provider")
	}
	agent := func() workspace.AgentSnapshot {
		t.Helper()
		var changes coreprotocol.AgentChangesResult
		wire.call(t, ws, coreprotocol.AgentChanges, coreprotocol.AgentChangesRequest{}, &changes)
		for _, delta := range changes.Agents {
			if delta.State.ID == created.OrchID {
				return delta.State
			}
		}
		t.Fatal("agent missing from inventory")
		return workspace.AgentSnapshot{}
	}
	stop := func(id string) bool {
		var out coreprotocol.DecisionResult
		featureCall(t, wire, ws, id, coreprotocol.CancelAgent, coreprotocol.AgentRequest{AgentID: created.OrchID, Generation: admitted.Receipt.TurnGeneration}, &out)
		return out.Accepted
	}
	if !stop("stop-0") {
		t.Fatal("Stop of a running turn was refused")
	}
	deadline := time.Now().Add(5 * time.Second)
	for state := agent(); state.Running || state.Thinking || state.Streaming || state.Cancelling; state = agent() {
		if time.Now().After(deadline) {
			t.Fatalf("turn did not stop: %+v", state)
		}
		time.Sleep(10 * time.Millisecond)
	}
	accepted := 0
	for i := 1; i <= 12; i++ {
		if stop(fmt.Sprintf("stop-%d", i)) {
			accepted++
		}
	}
	if state := agent(); state.Cancelling || state.Activity.Status.Phase == types.AgentCancelling {
		t.Fatalf("finished agent shows Stopping: cancelling=%v phase=%s detail=%q", state.Cancelling, state.Activity.Status.Phase, state.Activity.Status.Detail)
	}
	var status coreprotocol.StatusSnapshot
	wire.call(t, ws, coreprotocol.StatusSnapshotMethod, coreprotocol.Empty{}, &status)
	for _, a := range status.Agents {
		if a.Ref.ID == created.OrchID && a.Phase == types.AgentCancelling {
			t.Fatalf("status center shows Stopping for a finished agent: %+v", a)
		}
	}
	if accepted != 0 {
		t.Fatalf("%d Stops of a finished turn were accepted", accepted)
	}
}
