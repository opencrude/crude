package workspace

import (
	"strings"
	"testing"
)

func TestSummarizeToolArgsUsesCanonicalJSONOperands(t *testing.T) {
	if got := SummarizeToolArgs("update_plan", `{"plan_id":"plan-123","objective":"O"}`); got != "plan-123" {
		t.Fatalf("update_plan summary = %q", got)
	}
	if got := SummarizeToolArgs("grep_files", `{"pattern":"func (f *FilterField) type FilterField","path":"tui/ui"}`); !strings.Contains(got, `"func (f *FilterField)`) || !strings.Contains(got, "in tui/ui") {
		t.Fatalf("grep summary = %q", got)
	}
}

func TestAgentToolLifecycleRemainsActiveBetweenPermissionAndNextTurn(t *testing.T) {
	conversation := NewConversation()
	state := NewAgentState("agent", conversation, nil)
	state.SubmitInput("edit the file")
	initial := conversation.Messages()
	if len(initial) != 1 || initial[0].Role != MessageRoleStatus {
		t.Fatalf("submit lifecycle row = %#v", initial)
	}
	statusID := initial[0].InternalID
	status := ""
	active := false
	handle := func(evt AgentEvent) {
		state.HandleEvent(evt, nil,
			func(line string) { status = line },
			func(thinking, streaming, blocking bool) { active = thinking || streaming || blocking },
			func() { active = true },
			func(string, string, string, string, string, string, string) {},
			func(string) {},
			func() {},
		)
	}

	handle(AgentEvent{Type: "tool_call_streaming", Data: map[string]any{
		"internal_id": "internal-1", "tool_id": "tool-1", "name": "edit_file", "path": "main.go",
	}})
	if !state.IsActive() || !active || !strings.Contains(status, "Preparing Edit File") {
		t.Fatalf("streaming tool lifecycle: active=%v callback=%v status=%q", state.IsActive(), active, status)
	}
	if messages := conversation.Messages(); len(messages) != 1 || messages[0].Role != MessageRoleTool || messages[0].InternalID != statusID {
		t.Fatalf("lifecycle row did not transform in place: %#v", messages)
	}
	handle(AgentEvent{Type: "blocking", Data: map[string]string{
		"operation": "Update", "tool": "edit_file", "target": "main.go",
	}})
	handle(AgentEvent{Type: "permission_resolved", Data: map[string]any{
		"internal_id": "internal-1", "tool_id": "tool-1", "tool": "edit_file", "decision": "allowed",
	}})
	handle(AgentEvent{Type: "blocking_complete"})
	if !state.IsActive() || !active {
		t.Fatal("blocking_complete incorrectly made the running tool idle")
	}
	if messages := conversation.Messages(); len(messages) != 1 || messages[0].PermTag != "allowed" {
		t.Fatalf("permission lifecycle was not attached to tool: %#v", messages)
	}
	handle(AgentEvent{Type: "tool_result", Data: map[string]any{
		"internal_id": "internal-1", "tool_id": "tool-1", "name": "edit_file", "result": "ok",
	}})
	if !state.IsActive() || !strings.Contains(status, "done") {
		t.Fatalf("post-tool lifecycle: active=%v status=%q", state.IsActive(), status)
	}
	handle(AgentEvent{Type: "done", Data: map[string]any{"content": "done", "reason": "completed"}})
	if state.IsActive() {
		t.Fatal("completed turn remained active")
	}
}

func TestStructuredTurnPhasesExposeUploadAndProviderWait(t *testing.T) {
	state := NewAgentState("agent", nil, nil)
	thinking := false
	var statusID string
	for index, event := range []AgentEvent{
		{Type: "phase", Data: map[string]any{"name": "preparing", "progress": 37, "provider": "OpenAI", "uploaded_bytes": int64(37), "total_bytes": int64(100)}},
		{Type: "phase", Data: map[string]any{"name": "waiting_provider", "provider": "OpenAI"}},
	} {
		state.HandleEvent(event, nil, func(string) {}, func(active, _, _ bool) {
			thinking = active
		}, func() {}, func(string, string, string, string, string, string, string) {}, func(string) {}, func() {})
		if !thinking || !state.IsActive() {
			t.Fatalf("phase %#v was not active", event.Data)
		}
		messages := state.Messages.Messages()
		if len(messages) != 1 || messages[0].Role != MessageRoleStatus {
			t.Fatalf("phase lifecycle rows = %#v", messages)
		}
		if index == 0 {
			statusID = messages[0].InternalID
			if messages[0].LifecyclePhase != "preparing" || messages[0].LifecycleProgress != 37 ||
				messages[0].LifecycleProvider != "OpenAI" || messages[0].LifecycleUploadedBytes != 37 || messages[0].LifecycleTotalBytes != 100 {
				t.Fatalf("preparing row = %#v", messages[0])
			}
		} else if messages[0].InternalID != statusID || messages[0].LifecyclePhase != "waiting_provider" {
			t.Fatalf("provider wait replaced rather than updated row: %#v", messages[0])
		}
	}
	state.HandleEvent(AgentEvent{Type: "thinking", Data: "reasoning"}, nil, func(string) {}, func(bool, bool, bool) {}, func() {}, func(string, string, string, string, string, string, string) {}, func(string) {}, func() {})
	messages := state.Messages.Messages()
	if len(messages) != 1 || messages[0].Role != MessageRoleThinking {
		t.Fatalf("normal provider output did not consume lifecycle row: %#v", messages)
	}
}
