package core

import (
	"context"
	"fmt"
	"testing"
	"time"

	"crude/core/coreprotocol"
	"crude/internal/providersim"
	"crude/types/statuscenter"
	"crude/workspace"
)

// A user presses Stop repeatedly while a shell's view lags the core: every
// press after the first reaches the core once the turn already ended. The turn
// must stop on the first press and the agent must end idle, never left in a
// stopping boundary that no terminal acknowledgement will ever clear.
func TestRepeatedStopEndsIdleNotStopping(t *testing.T) {
	gate := providersim.NewGate()
	server := providersim.New(t)
	// Chat flushes an assistant-role event, then the first text delta.
	server.Script(providersim.Text("held turn").WithGateAfter(2, gate))
	r, root := featureRuntime(t, func(ws *workspace.Workspace) {
		app, cfg := inputResetConfig(server.BaseURL(providersim.Chat))
		ws.SetAppDefaults(app)
		ws.SetBackendConfig(cfg, t.TempDir())
	})
	t.Cleanup(gate.Release)
	wire := newResourceWire(t, r, "stop-author")
	var attached coreprotocol.AttachResult
	wire.call(t, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root}, &attached)
	ws := attached.WorkspaceID
	var created workspace.NewSessionResult
	wire.call(t, ws, coreprotocol.NewSession, coreprotocol.InvocationSessionRequest{}, &created)
	var admitted coreprotocol.InputResult
	featureCall(t, wire, ws, "turn", coreprotocol.SubmitInput, coreprotocol.InputRequest{AgentID: created.OrchID, Text: "Hold this turn", DraftRevision: 1}, &admitted)
	providerWait, cancelProviderWait := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelProviderWait()
	if err := gate.Wait(providerWait); err != nil {
		t.Fatal("turn never reached the provider", err)
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
	if state := agent(); state.Cancelling || state.Activity.Status.Phase == statuscenter.AgentCancelling {
		t.Fatalf("finished agent shows Stopping: cancelling=%v phase=%s detail=%q", state.Cancelling, state.Activity.Status.Phase, state.Activity.Status.Detail)
	}
	var status coreprotocol.StatusSnapshot
	wire.call(t, ws, coreprotocol.StatusSnapshotMethod, coreprotocol.Empty{}, &status)
	for _, a := range status.Agents {
		if a.Ref.ID == created.OrchID && a.Phase == statuscenter.AgentCancelling {
			t.Fatalf("status center shows Stopping for a finished agent: %+v", a)
		}
	}
	if accepted != 0 {
		t.Fatalf("%d Stops of a finished turn were accepted", accepted)
	}
}
