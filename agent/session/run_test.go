package session

import (
	"testing"
	"time"
)

// TestAsyncCommandStatusEventsCarryOrchestratorID locks in the attribution of
// async_command_status events: the polling ticker must stamp each event with
// the owning orchestrator's ID. The TUI resolves the background-jobs deck's
// session projection from that ID; empty-ID events are dropped by
// sessionProjectionForAgent, which kept the deck hidden forever.
func TestAsyncCommandStatusEventsCarryOrchestratorID(t *testing.T) {
	sm := NewSessionManager()
	info, err := sm.NewSession(testConfig(), t.TempDir())
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	sender := newMockEventSender()
	orch, err := sm.StartOrchestrator(info.ID, sender)
	if err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}

	// StartOrchestrator already owns and starts the run goroutine. Starting a
	// second copy here races sender/sub-agent wiring and does not model any
	// production lifecycle.
	t.Cleanup(func() { info.Session.CloseOrchestrator(orch.ID) })

	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for async_command_status event")
		case evt := <-sender.ch:
			if evt.typ != "async_command_status" {
				continue // tolerate unrelated startup events
			}
			if evt.agentID != orch.ID {
				t.Fatalf("async_command_status agentID = %q, want orchestrator ID %q", evt.agentID, orch.ID)
			}
			return
		}
	}
}
