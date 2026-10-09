package workspace

import (
	"testing"
	"time"

	ttypes "crude/types"
)

// Stop must settle the audit outcome, rather than replay dwell frames after the
// provider has stopped. Results received before stop are not cancelled results.
func TestCancelSettlesQueuedToolResultsWithoutReplayingAnimation(t *testing.T) {
	c := NewConversation()
	state := NewAgentState("agent", c, nil)
	state.SubmitInput("inspect and build")
	for _, tool := range []struct{ id, name string }{{"read", "read_file"}, {"search", "grep_files"}, {"build", "execute_command"}} {
		c.UpdateToolCall(tool.id, tool.id, tool.name, "test")
	}
	c.UpdateToolResult("read", "read", "read_file", "file contents", false, 0, 0, 0)
	c.UpdateToolResult("search", "search", "grep_files", "search failed", true, 0, 0, 0)
	// The result is known but dwell has not yet shown its terminal phase.
	if !c.HasPendingToolLifecycles() {
		t.Fatal("missing queued result phases")
	}
	state.BeginCancellation()
	if c.HasPendingToolLifecycles() || state.IsActive() {
		t.Fatal("stop left animated tool transitions pending")
	}
	want := map[string]struct{ status, preview, result string }{
		"read":   {"success", "done", "file contents"},
		"search": {"error", "error", "search failed"},
		"build":  {"cancelled", "done", "execute_command test"},
	}
	assert := func() {
		t.Helper()
		for _, msg := range c.Messages() {
			if expected, ok := want[msg.InternalID]; ok {
				if msg.ToolStatus != expected.status || msg.ToolPreviewPhase != expected.preview || msg.Content != expected.result || msg.ToolProgressPhase != "" {
					t.Errorf("%s = (%s, %s, %q, %q), want %+v", msg.InternalID, msg.ToolStatus, msg.ToolPreviewPhase, msg.Content, msg.ToolProgressPhase, expected)
				}
			}
		}
	}
	assert()
	if c.AdvanceToolLifecycles(time.Now().Add(10 * time.Second)) {
		t.Fatal("old dwell frame resurrected stopped tools")
	}
	state.ApplyEvent(AgentEvent{Type: "tool_result", Data: map[string]any{
		"tool_id": "build", "internal_id": "build", "name": "execute_command", "result": "late result",
	}})
	state.ApplyEvent(AgentEvent{Type: "done", Data: map[string]any{"reason": "cancelled"}})
	want["build"] = struct{ status, preview, result string }{"success", "done", "late result"}
	assert()
	if state.IsActive() || c.HasPendingToolLifecycles() {
		t.Fatal("late semantic evidence reactivated the stopped turn")
	}
	if !c.BeginTurn(ttypes.TurnIdentity{Generation: 2, ModelID: "next-model"}) {
		t.Fatal("next turn rejected")
	}
	c.UpdateToolCall("next", "next", "read_file", "next.go")
	c.UpdateToolResult("next", "next", "read_file", "next contents", false, 0, 0, 0)
	c.AdvanceToolLifecycles(time.Now().Add(time.Second))
	c.AdvanceToolLifecycles(time.Now().Add(2 * time.Second))
	assert()
}

func TestCancelKeepsAlreadyPaintedResultWhenOtherFramesQueued(t *testing.T) {
	c := NewConversation()
	c.UpdateToolCall("finished", "finished", "read_file", "file.go")
	c.UpdateToolResult("finished", "finished", "read_file", "real result", false, 0, 0, 0)
	c.AdvanceToolLifecycles(time.Now().Add(time.Second))
	c.AdvanceToolLifecycles(time.Now().Add(2 * time.Second))
	c.MarkStreamingToolsCancelled()
	msg := c.Messages()[0]
	if msg.ToolStatus != "success" || msg.Content != "real result" || c.HasPendingToolLifecycles() {
		t.Fatalf("completed result was lost: %+v", msg)
	}
}
