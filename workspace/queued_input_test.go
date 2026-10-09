package workspace

import (
	"testing"
	"time"

	"crude/provider"
	"crude/session"
)

func TestQueuedInputShowsOnePendingRowAndAdmitsInPlace(t *testing.T) {
	w := newIngressWorkspace(t)
	intent := &InputAdmissionIntent{SessionID: "session", AgentID: "root", InputID: "queued-1", DraftRevision: 3}
	result := InputAdmissionResult{Status: InputAdmissionQueued, Receipt: session.InputAdmissionReceipt{
		SessionID: "session", AgentID: "root", InputID: "queued-1", DraftRevision: 3, AcceptedAt: time.Now()}}
	if !w.ApplyAgentInputAdmission(intent, result, provider.PlainInput("after compaction"), nil) {
		t.Fatal("queued receipt was not accepted")
	}
	// The session also announces the queued row; it must not duplicate.
	w.ApplyAgentEvent(AgentEvent{AgentID: "root", Type: "input_queued", Data: map[string]any{"input_id": "queued-1", "text": "after compaction"}})
	rows := 0
	for _, message := range w.AgentMgr.Get("root").State.Messages.Messages() {
		if message.InternalID == "queued-1" {
			rows++
			if !message.PendingSteering || message.Content != "after compaction" {
				t.Fatalf("queued row is not pending: %+v", message)
			}
		}
	}
	if rows != 1 {
		t.Fatalf("queued input rendered %d rows", rows)
	}
	w.ApplyAgentEvent(AgentEvent{AgentID: "root", Type: "queued_input_admitted", Data: map[string]any{"input_id": "queued-1"}})
	for _, message := range w.AgentMgr.Get("root").State.Messages.Messages() {
		if message.InternalID == "queued-1" && message.PendingSteering {
			t.Fatal("admitted input is still pending")
		}
	}
}
