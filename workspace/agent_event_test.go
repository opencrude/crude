package workspace

import (
	"strings"
	"testing"
	"time"

	ttypes "crude/types"
	"crude/types/reasoning"
)

func TestAgentStateRuntimeEventIdentityRejectsDuplicateAndWrongRun(t *testing.T) {
	state := NewAgentState("agent", nil, nil)
	base := ttypes.RuntimeEventIdentity{EventID: "one", OwnerID: "session", AgentID: "agent", RunID: "run", RuntimeEpoch: 2, Generation: 3, Sequence: 1}
	if !state.AcceptRuntimeEvent(base) {
		t.Fatal("first exact envelope was rejected")
	}
	if state.AcceptRuntimeEvent(base) {
		t.Fatal("duplicate envelope was accepted")
	}
	wrongRun := base
	wrongRun.EventID, wrongRun.RunID, wrongRun.Sequence = "two", "replacement", 2
	if state.AcceptRuntimeEvent(wrongRun) {
		t.Fatal("same-generation event from another run was accepted")
	}
	next := base
	next.EventID, next.Sequence = "three", 2
	if !state.AcceptRuntimeEvent(next) {
		t.Fatal("monotonic same-run semantic event was rejected")
	}
}

func TestSummarizeToolArgsUsesCanonicalJSONOperands(t *testing.T) {
	if got := SummarizeToolArgs("update_plan", `{"plan_id":"plan-123","objective":"O"}`); got != "plan-123" {
		t.Fatalf("update_plan summary = %q", got)
	}
	if got := SummarizeToolArgs("grep_files", `{"pattern":"func (f *FilterField) type FilterField","path":"tui/ui"}`); !strings.Contains(got, `"func (f *FilterField)`) || !strings.Contains(got, "in tui/ui") {
		t.Fatalf("grep summary = %q", got)
	}
}

func TestToolEventsNeverPublishStructuredArgumentsAsStatusOrOperand(t *testing.T) {
	for _, tc := range []struct{ tool, args, want string }{
		{"agent_wait", `{"run_id":"run-8f21"}`, "run-8f21"},
		{"agent_control", `{"operation":"cancel","agent_id":"agent-8f21"}`, "cancel · agent-8f21"},
		{"agent_list", `{"query":"renderer"}`, "renderer"},
		{"plugin_tool", `{"unrecognized":{"private":"detail"}}`, ""},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			state := NewAgentState("agent", nil, nil)
			effects := state.ApplyEvent(AgentEvent{Type: "tool_call", Data: map[string]any{
				"internal_id": "message", "tool_id": "call", "name": tc.tool, "args": tc.args,
			}})
			status := effects.Status
			if !effects.StatusChanged {
				t.Fatal("tool call did not publish a status effect")
			}
			messages := state.Messages.Messages()
			if len(messages) != 1 || messages[0].ToolArgs != tc.want ||
				strings.Contains(messages[0].ToolProgressLabel, `"`) || strings.Contains(status, `"`) {
				t.Fatalf("tool event leaked JSON: messages=%+v status=%q", messages, status)
			}
		})
	}
}

func TestToolStatusRejectsStructuredToolNames(t *testing.T) {
	got := ToolLabel(`{"name":"agent_wait","private_id":"agent-8f21"}`, "")
	if got != "Tool" {
		t.Fatalf("structured name leaked into status: %q", got)
	}
	for _, phase := range []string{"preparing", "failed", "done"} {
		status := toolOperationStatus(`{"private_id":"agent-8f21"}`, phase)
		if strings.Contains(status, "{") || strings.Contains(status, "\"") {
			t.Fatalf("%s status leaked structured tool name: %q", phase, status)
		}
	}
}

func TestParallelToolEventRetainsChildrenWithoutShowingAPIFields(t *testing.T) {
	input := `{"tool_uses":[{"recipient_name":"functions.execute_command","parameters":{"command":"go test ./..."}},{"recipient_name":"functions.read_file","parameters":{"path":"main.go"}}]}`
	state := NewAgentState("agent", nil, nil)
	effects := state.ApplyEvent(AgentEvent{Type: "tool_call", Data: map[string]any{
		"internal_id": "batch", "tool_id": "call", "name": "multi_tool_use.parallel", "args": input,
	}})
	status := effects.Status
	if !effects.StatusChanged {
		t.Fatal("parallel tool call did not publish a status effect")
	}
	messages := state.Messages.Messages()
	if len(messages) != 1 || messages[0].ToolInput != input || messages[0].ToolArgs != "2 tools" || status != "Running 2 tools" {
		t.Fatalf("batch call state = %+v; status = %q", messages, status)
	}
}

func TestExecutingPhaseRemovesOnlyTransientProviderWait(t *testing.T) {
	state := NewAgentState("agent", nil, nil)
	state.Messages.UpdateTurnStatus(TurnStatus{Phase: "compaction_completed", OperationID: "checkpoint"})
	state.Messages.UpdateTurnStatus(TurnStatus{Phase: "waiting_provider", Provider: "gateway"})
	state.ApplyEvent(AgentEvent{Type: "phase", Data: "executing"})
	messages := state.Messages.Messages()
	if len(messages) != 1 || messages[0].LifecycleOperationID != "checkpoint" {
		t.Fatalf("tool execution retained stale provider wait or cleared durable checkpoint: %+v", messages)
	}
}

func TestRetryUpdatesOneStatusAndStopIgnoresLateRetry(t *testing.T) {
	state := NewAgentState("agent", nil, nil)
	emit := func(event AgentEvent) {
		state.ApplyEvent(event)
	}
	for _, attempt := range []int{1, 2, 3} {
		emit(AgentEvent{Type: "retry", Data: map[string]any{"attempt": attempt, "limit": 6, "backoff": float64(4), "reason": "Provider temporarily unavailable"}})
	}
	msgs := state.Messages.Messages()
	if len(msgs) != 1 || msgs[0].LifecyclePhase != "retrying" || msgs[0].LifecycleRetryAttempt != 4 {
		t.Fatalf("retry spammed transcript: %+v", msgs)
	}
	state.Cancelling = true
	emit(AgentEvent{Type: "retry", Data: map[string]any{"attempt": 4, "limit": 6}})
	if state.Messages.Messages()[0].LifecycleRetryAttempt != 4 {
		t.Fatal("late retry mutated cancelled turn")
	}
}

func TestThinkingBlockFinalizesWhenToolActionStarts(t *testing.T) {
	state := NewAgentState("agent", nil, nil)
	handle := func(event AgentEvent) {
		state.ApplyEvent(event)
	}
	handle(AgentEvent{Type: "thinking", Data: reasoning.Delta{Text: "brief thought"}})
	time.Sleep(time.Millisecond)
	handle(AgentEvent{Type: "tool_call_streaming", Data: map[string]any{
		"internal_id": "internal-1", "tool_id": "tool-1", "name": "read_file", "path": "main.go",
	}})

	messages := state.Messages.Messages()
	if len(messages) != 2 || messages[0].Role != MessageRoleThinking || messages[0].ThinkingActive {
		t.Fatalf("thinking-to-tool transition=%#v", messages)
	}
	if messages[0].ThinkingDuration <= 0 {
		t.Fatalf("finalized thinking duration=%s, want positive", messages[0].ThinkingDuration)
	}
	if messages[1].Role != MessageRoleTool {
		t.Fatalf("next action=%#v, want tool", messages[1])
	}
}

func TestReplyResultCreatesOneAttributedProgressMessageNotAToolRow(t *testing.T) {
	state := NewAgentState("root", nil, nil)
	identity := ttypes.TurnIdentity{Generation: 1, ProviderID: "openai", ModelID: "model"}
	handle := func(evt AgentEvent) {
		state.ApplyEvent(evt)
	}
	handle(AgentEvent{Type: "turn_started", Data: identity})
	handle(AgentEvent{Type: "tool_call", Data: map[string]any{"tool_id": "progress-1", "name": "reply"}})
	handle(AgentEvent{Type: "tool_result", Data: map[string]any{
		"tool_id": "progress-1", "name": "reply", "result": "Checking tests.",
	}})
	handle(AgentEvent{Type: "done", Data: map[string]any{"content": "", "reason": "completed"}})
	messages := state.Messages.Messages()
	if len(messages) != 1 || messages[0].InternalID != "reply:progress-1" || messages[0].Role != MessageRoleAssistant ||
		messages[0].Content != "Checking tests." || messages[0].ProviderID != "openai" || messages[0].ModelID != "model" {
		t.Fatalf("reply communication should be unique and attributed: %+v", messages)
	}
}

func TestCancellingAgentIgnoresQueuedNonTerminalEvents(t *testing.T) {
	state := NewAgentState("agent", nil, nil)
	state.SubmitInput("start")
	state.ApplyEvent(AgentEvent{Type: "thinking", Data: reasoning.Delta{Text: "before cancel"}})
	state.BeginCancellation()
	if state.IsActive() {
		t.Fatal("cancellation left the visual turn active")
	}
	before := state.Messages.Messages()
	state.ApplyEvent(AgentEvent{Type: "thinking", Data: reasoning.Delta{Text: " stale"}})
	after := state.Messages.Messages()
	if len(after) != len(before) || after[0].Content != before[0].Content {
		t.Fatalf("queued event mutated cancelling state: before=%#v after=%#v", before, after)
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
	handle := func(evt AgentEvent) AgentEffects { return state.ApplyEvent(evt) }

	effects := handle(AgentEvent{Type: "tool_call_streaming", Data: map[string]any{
		"internal_id": "internal-1", "tool_id": "tool-1", "name": "edit_file", "path": "main.go",
	}})
	if !state.IsActive() || !effects.ThinkingChanged || !effects.Thinking || !effects.StatusChanged || !strings.Contains(effects.Status, "Preparing Edit File") {
		t.Fatalf("streaming tool lifecycle: active=%v effects=%+v", state.IsActive(), effects)
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
	effects = handle(AgentEvent{Type: "blocking_complete"})
	if !state.IsActive() || !effects.ThinkingChanged || !effects.Thinking || effects.Streaming || effects.Blocking {
		t.Fatal("blocking_complete incorrectly made the running tool idle")
	}
	if messages := conversation.Messages(); len(messages) != 1 || messages[0].PermTag != "allowed" {
		t.Fatalf("permission lifecycle was not attached to tool: %#v", messages)
	}
	effects = handle(AgentEvent{Type: "tool_result", Data: map[string]any{
		"internal_id": "internal-1", "tool_id": "tool-1", "name": "edit_file", "result": "ok",
	}})
	if !state.IsActive() || !effects.StatusChanged || !strings.Contains(effects.Status, "done") {
		t.Fatalf("post-tool lifecycle: active=%v effects=%+v", state.IsActive(), effects)
	}
	handle(AgentEvent{Type: "done", Data: map[string]any{"content": "done", "reason": "completed"}})
	if state.IsActive() {
		t.Fatal("completed turn remained active")
	}
}

func TestStructuredTurnPhasesExposeUploadAndProviderWait(t *testing.T) {
	state := NewAgentState("agent", nil, nil)
	var statusID string
	for index, event := range []AgentEvent{
		{Type: "phase", Data: map[string]any{"name": "preparing", "progress": 37, "provider": "OpenAI", "uploaded_bytes": int64(37), "total_bytes": int64(100)}},
		{Type: "phase", Data: map[string]any{"name": "waiting_provider", "provider": "OpenAI"}},
	} {
		effects := state.ApplyEvent(event)
		if !effects.ThinkingChanged || !effects.Thinking || !state.IsActive() {
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
	state.ApplyEvent(AgentEvent{Type: "thinking", Data: reasoning.Delta{Text: "reasoning"}})
	messages := state.Messages.Messages()
	if len(messages) != 1 || messages[0].Role != MessageRoleThinking {
		t.Fatalf("normal provider output did not consume lifecycle row: %#v", messages)
	}
}
