package core

import (
	"context"
	"fmt"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/document"
	"crude/types"
	"crude/workspace"
)

func idleEventActor(t *testing.T) *actor {
	t.Helper()
	r := failureRuntime(t)
	c, id := resourceConnection(t, r, t.TempDir(), "idle-events")
	c.mu.Lock()
	a := c.attached[id]
	c.mu.Unlock()
	return a
}

func awaitIdleState(t *testing.T, a *actor, topic string, check func(coreprotocol.WorkspaceState) bool) coreprotocol.WorkspaceState {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		entry, wake := a.states.slots[topic].hub.latest()
		var state coreprotocol.WorkspaceState
		if len(entry.body) > 0 {
			if err := coreprotocol.Decode(entry.body, &state); err != nil {
				t.Fatal(err)
			}
			if check(state) {
				return state
			}
		}
		select {
		case <-wake:
		case <-timer.C:
			t.Fatalf("%s did not publish authoritative external mutation", topic)
		}
	}
}

func TestIdleActorExternalOwnersPublishWithoutMaintenanceDeadline(t *testing.T) {
	a := idleEventActor(t)
	assertIdle := func() {
		t.Helper()
		if err := a.inspect(t.Context(), func() {
			if a.maintenance.ready != nil || a.maintenance.timer != nil {
				t.Error("settled actor allocated an idle maintenance clock")
			}
		}); err != nil {
			t.Fatal(err)
		}
	}
	assertIdle()
	// No actor request/job completion announces these mutations. The real
	// select must hear the authoritative owner broadcasts, including bursts
	// arriving while earlier state derivation/publication is in progress.
	for round := range 20 {
		summary := fmt.Sprintf("external-%d", round)
		a.ws.Status.Notify(types.NotificationRecord{Summary: summary})
		awaitIdleState(t, a, coreprotocol.TopicStatus, func(state coreprotocol.WorkspaceState) bool {
			return state.Status != nil && len(state.Status.Notifications) > 0 && state.Status.Notifications[0].Summary == summary
		})
	}
	for round := range 20 {
		path := fmt.Sprintf("external-%d.go", round)
		a.ws.LSPMgr.SetActiveFile(path)
		version := a.ws.LSPMgr.Version()
		awaitIdleState(t, a, coreprotocol.TopicLSP, func(state coreprotocol.WorkspaceState) bool {
			return state.LSP != nil && state.LSP.Version >= version
		})
	}
	a.ws.LSPMgr.Stop()
	version := a.ws.LSPMgr.Version()
	awaitIdleState(t, a, coreprotocol.TopicLSP, func(state coreprotocol.WorkspaceState) bool {
		return state.LSP != nil && state.LSP.Version >= version && state.LSP.Insights.IdleStatus == "Language servers paused"
	})
	assertIdle()
}

func TestIdleActorPendingToolsArmAdvanceAndDisarm(t *testing.T) {
	a := idleEventActor(t)
	since := a.topic.hub.sequence()
	if err := a.inspect(t.Context(), func() { a.ws.RegisterAgentRoot("tool-owner") }); err != nil {
		t.Fatal(err)
	}
	a.SendAgentEventWithID("tool_call", map[string]any{"tool_id": "tool", "internal_id": "tool", "name": "read_file", "args": "a.go"}, "tool-owner")
	a.SendAgentEventWithID("tool_result", map[string]any{"tool_id": "tool", "internal_id": "tool", "name": "read_file", "result": "complete result"}, "tool-owner")
	// Commands and ingress are separate select cases: use a publication fence
	// rather than assuming an inspection command ran after the agent events.
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		entry, wake, _ := a.topic.hub.next(since)
		if entry.seq > since {
			since = entry.seq
			var update coreprotocol.WorkspaceUpdate
			if err := coreprotocol.Decode(entry.body, &update); err != nil {
				t.Fatal(err)
			}
			for _, conversation := range update.Conversations {
				for _, message := range conversation.Delta.Upserts {
					if message.InternalID == "tool" && message.ToolPreviewPhase == "done" && message.Content == "complete result" {
						if err := a.inspect(t.Context(), func() {
							if a.maintenance.timer == nil {
								t.Error("queued lifecycle never armed maintenance")
							}
							if a.maintenance.ready != nil || a.ws.AgentMgr.Get("tool-owner").State.Messages.HasPendingToolLifecycles() {
								t.Error("settled tool kept maintenance deadline")
							}
						}); err != nil {
							t.Fatal(err)
						}
						return
					}
				}
			}
			continue
		}
		select {
		case <-wake:
		case <-timer.C:
			t.Fatal("pending lifecycle did not publish terminal tool result")
		}
	}
}

func TestIdleActorFullLaneDocumentReleaseRetriesThenDisarms(t *testing.T) {
	a := idleEventActor(t)
	gate, started := make(chan struct{}), make(chan struct{})
	if !a.resourceLane.submit(func(context.Context) { close(started); <-gate }) {
		t.Fatal("could not block lane")
	}
	<-started
	for range cap(a.resourceLane.queue) {
		if !a.resourceLane.submit(func(context.Context) {}) {
			t.Fatal("could not fill lane")
		}
	}
	owner := document.Owner{ClientID: "detached", Epoch: 1}
	if err := a.inspect(t.Context(), func() {
		a.documentReleases = append(a.documentReleases, owner)
		a.drainDocumentReleases()
		a.refreshMaintenance()
		if len(a.documentReleases) != 1 || a.maintenance.ready == nil {
			t.Error("refused release lost its pending deadline")
		}
	}); err != nil {
		t.Fatal(err)
	}
	close(gate)
	// The lane has no completion event for plain callbacks. Only this pending
	// release permits bounded retry; once admitted it must not leave idle ticks.
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		var pending bool
		if err := a.inspect(t.Context(), func() {
			pending = len(a.documentReleases) != 0
			if !pending && a.maintenance.ready != nil {
				t.Error("admitted release kept idle deadline")
			}
		}); err != nil {
			t.Fatal(err)
		}
		if !pending {
			return
		}
		select {
		case <-time.After(20 * time.Millisecond):
		case <-deadline.C:
			t.Fatal("full-lane detached release was never retried")
		}
	}
}

func TestActorMaintenanceCancellationStopsPendingDeadline(t *testing.T) {
	mgr := workspace.NewAgentManager()
	c := workspace.NewConversation()
	state := workspace.NewAgentState("agent", c, nil)
	mgr.AddOrchestrator("agent", state)
	a := &actor{ws: &workspace.Workspace{AgentMgr: mgr}}
	defer a.maintenance.stop()
	c.UpdateToolCall("tool", "tool", "read_file", "a.go")
	c.UpdateToolResult("tool", "tool", "read_file", "result", false, 0, 0, 0)
	a.refreshMaintenance()
	if a.maintenance.ready == nil {
		t.Fatal("new tool phases did not arm maintenance")
	}
	state.BeginCancellation()
	a.refreshMaintenance()
	if a.maintenance.ready != nil {
		t.Fatal("cancelled lifecycle retained an idle deadline")
	}
	select {
	case <-a.maintenance.timer.C:
		t.Fatal("stopped lifecycle timer fired")
	case <-time.After(150 * time.Millisecond):
	}
}
