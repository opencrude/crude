package workspace

import (
	"reflect"
	"testing"
	"time"

	ttypes "crude/types"
	"crude/types/reasoning"
)

func TestConversationKeepsImmutableIdentityPerTurnDerivedMessage(t *testing.T) {
	conversation := NewConversation()
	first := ttypes.TurnIdentity{Generation: 1, ProviderID: "provider-a", AccountID: "personal", ModelID: "model-a"}
	if !conversation.BeginTurn(first) {
		t.Fatal("first turn identity was rejected")
	}
	conversation.AppendThinking("reasoning")
	conversation.UpdateToolCall("tool-a", "internal-a", "read_file", "a.go")
	conversation.EnsureAssistant()
	conversation.AppendToken("first answer")

	second := ttypes.TurnIdentity{Generation: 2, ProviderID: "provider-a", AccountID: "work", ModelID: "model-b"}
	if !conversation.BeginTurn(second) {
		t.Fatal("second turn identity was rejected")
	}
	conversation.AppendMessage(Message{Role: MessageRoleUser, Content: "continue"})
	conversation.EnsureAssistant()
	conversation.AppendToken("second answer")

	if conversation.BeginTurn(first) {
		t.Fatal("stale turn identity moved attribution backward")
	}
	messages := conversation.Messages()
	for _, message := range messages[:3] {
		if message.Role == MessageRoleThinking || message.Role == MessageRoleTool || message.Role == MessageRoleAssistant {
			if message.ModelID != "model-a" || message.ProviderID != "provider-a" || message.AccountID != "personal" || message.TurnGeneration != 1 {
				t.Fatalf("first turn message was relabeled: %#v", message)
			}
		}
	}
	last := messages[len(messages)-1]
	if last.Role != MessageRoleAssistant || last.ModelID != "model-b" || last.ProviderID != "provider-a" || last.AccountID != "work" || last.TurnGeneration != 2 {
		t.Fatalf("second turn assistant attribution = %#v", last)
	}
}

func TestMessageContainsNoPresentationCacheFields(t *testing.T) {
	typeOfMessage := reflect.TypeOf(Message{})
	for _, name := range []string{"CachedLines", "CursorLine", "StreamCompletedLines", "StreamCompletedLen", "SessionPickerSessions"} {
		if _, ok := typeOfMessage.FieldByName(name); ok {
			t.Fatalf("workspace Message contains TUI presentation field %s", name)
		}
	}
}

func TestConversationStreamingLifecycle(t *testing.T) {
	conversation := NewConversation()
	agent := NewAgentState("agent", conversation, &testLogger{})
	identity := ttypes.TurnIdentity{Generation: 3, ProviderID: "ollama-cloud", ModelID: "kimi-k3"}

	agent.HandleEvent(AgentEvent{Type: "turn_started", AgentID: "agent", Data: identity}, nil, ignoreStatus, ignoreThinking, func() {}, ignoreConfirm, func(string) {}, func() {})
	agent.HandleEvent(AgentEvent{Type: "thinking", AgentID: "agent", Data: reasoning.Delta{Text: "considering"}}, nil, ignoreStatus, ignoreThinking, func() {}, ignoreConfirm, func(string) {}, func() {})
	agent.HandleEvent(AgentEvent{Type: "stream_token", AgentID: "agent", Data: "hello"}, nil, ignoreStatus, ignoreThinking, func() {}, ignoreConfirm, func(string) {}, func() {})
	agent.Tick(1)

	messages := conversation.Messages()
	if len(messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(messages))
	}
	if messages[0].Role != MessageRoleThinking || messages[0].ThinkingActive {
		t.Fatalf("thinking message not completed: %#v", messages[0])
	}
	if messages[1].Role != MessageRoleAssistant || messages[1].Content != "hello" {
		t.Fatalf("assistant message = %#v", messages[1])
	}
	for _, message := range messages {
		if message.TurnGeneration != identity.Generation || message.ProviderID != identity.ProviderID || message.ModelID != identity.ModelID {
			t.Fatalf("live event attribution = %#v", message)
		}
	}
}

func TestConversationToolLifecycle(t *testing.T) {
	conversation := NewConversation()
	conversation.UpdateToolStreaming("tool-1", "internal-1", "read_file", map[string]any{
		"target":            "main.go",
		"content_lines_int": 12,
	})
	conversation.UpdateToolCall("tool-1", "internal-1", "read_file", "main.go")
	conversation.SetToolPermTag("tool-1", "internal-1", "allowed")
	conversation.UpdateToolResult("tool-1", "internal-1", "read_file", "contents", false, 20, 0, 0)

	messages := conversation.Messages()
	if len(messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(messages))
	}
	message := messages[0]
	if message.ToolPreviewPhase != "preparing" || message.ToolStatus == "success" {
		t.Fatalf("atomic tool skipped preparing: %#v", message)
	}
	firstTick := time.Now().Add(time.Second)
	if !conversation.AdvanceToolLifecycles(firstTick) {
		t.Fatal("tool did not advance to doing")
	}
	message = conversation.Messages()[0]
	if message.ToolPreviewPhase != "streaming" || message.ToolStatus == "success" {
		t.Fatalf("atomic tool skipped doing: %#v", message)
	}
	if !conversation.AdvanceToolLifecycles(firstTick.Add(time.Second)) {
		t.Fatal("tool did not advance to done")
	}
	message = conversation.Messages()[0]
	if message.ToolStatus != "success" || message.ToolPreviewPhase != "done" || message.Content != "contents" || message.PermTag != "allowed" {
		t.Fatalf("unexpected tool message: %#v", message)
	}
}

func TestConversationSnapshotDoesNotExposeMutableState(t *testing.T) {
	conversation := NewConversation()
	conversation.AppendMessage(Message{Role: MessageRoleUser, Content: "original"})

	snapshot := conversation.Messages()
	snapshot[0].Content = "mutated"

	if got := conversation.Messages()[0].Content; got != "original" {
		t.Fatalf("snapshot mutation changed conversation: %q", got)
	}
}

func TestConversationFinishCancelledPreservesContentAndAppendsStop(t *testing.T) {
	conversation := NewConversation()
	conversation.AppendMessage(Message{Role: MessageRoleAssistant, Content: "partial"})
	conversation.Finish("", "cancelled", true, "stop")

	message := conversation.Messages()[0]
	if message.Incomplete || message.Content != "partial" || len(conversation.Messages()) != 2 || conversation.Messages()[1].LifecyclePhase != "stopped" {
		t.Fatalf("unexpected cancelled message: %#v", message)
	}
}

func TestConversationThinkingPromotionRestoresIdentityAndPosition(t *testing.T) {
	conversation := NewConversation()
	conversation.BeginTurn(ttypes.TurnIdentity{Generation: 2, ModelID: "model", ProviderID: "provider"})
	conversation.AppendThinking("private reasoning")
	original := conversation.Messages()[0]
	thought, ok := conversation.PromoteThinking("pane")
	if !ok || !reflect.DeepEqual(thought, original) || conversation.Messages()[0].InternalID != original.InternalID {
		t.Fatalf("promotion lost thought identity: %+v", thought)
	}
	conversation.AppendMessage(Message{Role: MessageRoleTool, ToolName: "read_file"})
	if conversation.RestoreThinking("pane", thought) {
		t.Fatal("active thought restored before it finished")
	}
	thought.Content += " continued"
	thought.ThinkingActive = false
	thought.ThinkingDuration = time.Second
	if !conversation.RestoreThinking("pane", thought) {
		t.Fatal("settled thought was not restored")
	}
	got := conversation.Messages()
	if len(got) != 2 || got[0].InternalID != original.InternalID || got[0].Content != thought.Content || got[0].SubPaneID != "" || got[0].ModelID != "model" {
		t.Fatalf("restore lost thought position, content, or attribution: %+v", got)
	}
}

func TestConversationNewThinkingDoesNotExtendSettledBlock(t *testing.T) {
	conversation := NewConversation()
	conversation.AppendThinking("first block")
	conversation.MarkAllThinkingDone()
	first := conversation.Messages()[0]
	conversation.AppendThinking("second block")
	messages := conversation.Messages()
	if len(messages) != 2 || !reflect.DeepEqual(messages[0], first) || !messages[1].ThinkingActive || messages[1].InternalID == first.InternalID {
		t.Fatalf("new reasoning overwrote settled evidence: %+v", messages)
	}
}

func TestTurnStatusStaysLastAcrossSteeringAndTransformsInPlace(t *testing.T) {
	conversation := NewConversation()
	conversation.AppendMessage(Message{Role: MessageRoleUser, Content: "start"})
	conversation.UpdateTurnStatus(TurnStatus{Phase: "preparing", Progress: 25})
	statusID := conversation.Messages()[1].InternalID
	conversation.AppendMessage(Message{Role: MessageRoleUser, Content: "human steering: include tests"})

	messages := conversation.Messages()
	if len(messages) != 3 || messages[1].Content != "human steering: include tests" || messages[2].Role != MessageRoleStatus {
		t.Fatalf("human steering did not stay before live status: %#v", messages)
	}
	conversation.UpdateToolStreaming("tool-1", "internal-1", "read_file", map[string]any{"target": "main.go"})
	messages = conversation.Messages()
	if len(messages) != 3 || messages[2].Role != MessageRoleTool || messages[2].InternalID != statusID || messages[2].ToolID != "tool-1" {
		t.Fatalf("status did not transform in place after human steering: %#v", messages)
	}
}

func TestPendingSteeringSettlementRetainsEveryLocallyAcceptedMessage(t *testing.T) {
	conversation := NewConversation()
	consumed := conversation.AppendPendingSteering(Message{Role: MessageRoleUser, Content: "keep me"})
	cancelled := conversation.AppendPendingSteering(Message{Role: MessageRoleUser, Content: "keep stopped note"})
	if !conversation.ConsumePendingSteering(consumed) {
		t.Fatal("consumed human steering was not resolved")
	}
	if settled := conversation.SettlePendingSteeringIDs([]string{cancelled}); settled != 1 {
		t.Fatalf("settled=%d, want 1", settled)
	}
	messages := conversation.Messages()
	if len(messages) != 2 || messages[0].Content != "keep me" || messages[1].Content != "keep stopped note" || messages[0].PendingSteering || messages[1].PendingSteering {
		t.Fatalf("canonical messages=%#v", messages)
	}
}

func TestPendingSteeringStaysAfterProviderOutputUntilAcknowledged(t *testing.T) {
	conversation := NewConversation()
	conversation.UpdateTurnStatus(TurnStatus{Phase: "waiting_provider"})
	pendingID := conversation.AppendPendingSteering(Message{Role: MessageRoleUser, Content: "include tests"})
	conversation.UpdateToolStreaming("tool-1", "internal-1", "write_file", map[string]any{
		"target": "main.go", "content_lines_int": 3,
	})

	messages := conversation.Messages()
	if len(messages) != 2 || messages[0].Role != MessageRoleTool || !messages[1].PendingSteering || messages[1].InternalID != pendingID {
		t.Fatalf("pending human steering was not pinned after live output: %#v", messages)
	}
	if !conversation.ConsumePendingSteering(pendingID) {
		t.Fatal("pending human steering acknowledgement was rejected")
	}
	conversation.AppendMessage(Message{Role: MessageRoleAssistant, Content: "now I saw it"})
	messages = conversation.Messages()
	if len(messages) != 3 || messages[1].InternalID != pendingID || messages[1].PendingSteering || messages[2].Role != MessageRoleAssistant {
		t.Fatalf("acknowledged human steering did not rejoin chronology: %#v", messages)
	}
}

func TestSteeringAdmissionMovesAboveTurnIndicatorAndRejectedNotesReturn(t *testing.T) {
	conversation := NewConversation()
	conversation.UpdateTurnStatus(TurnStatus{Phase: "preparing"})
	first := conversation.AppendPendingSteering(Message{Role: MessageRoleUser, Content: "first"})
	second := conversation.AppendPendingSteering(Message{Role: MessageRoleUser, Content: "second"})
	if !conversation.ConsumePendingSteering(first) {
		t.Fatal("first upload was not acknowledged")
	}
	messages := conversation.Messages()
	if len(messages) != 3 || messages[0].InternalID != first || messages[0].PendingSteering || messages[1].Role != MessageRoleStatus || messages[2].InternalID != second || !messages[2].PendingSteering {
		t.Fatalf("upload handoff did not move note above indicator: %+v", messages)
	}
	settled := conversation.SettlePendingSteeringMessages([]string{first, second})
	if len(settled) != 2 || settled[0].Content != "first" || settled[1].Content != "second" || len(conversation.Messages()) != 3 {
		t.Fatalf("stopped upload did not retain both notes in order: %+v, remaining %+v", settled, conversation.Messages())
	}
}

func TestAttachSubPanePromotesExistingSubAgentToolWithoutDuplicate(t *testing.T) {
	conversation := NewConversation()
	conversation.UpdateToolCall("tool-1", "internal-1", "agent_start", "category: researcher description: survey")
	if !conversation.AttachSubPaneToTool("tool-1", "child-1", "Lily — survey") {
		t.Fatal("active sub-agent tool row was not promoted")
	}
	conversation.UpdateToolResult("tool-1", "internal-1", "agent_start", "completed", false, 0, 0, 0)
	now := time.Now().Add(time.Second)
	conversation.AdvanceToolLifecycles(now)
	conversation.AdvanceToolLifecycles(now.Add(time.Second))
	messages := conversation.Messages()
	if len(messages) != 1 || messages[0].SubPaneID != "child-1" || messages[0].ToolProgressLabel != "Lily — survey" || messages[0].ToolStatus == "calling" {
		t.Fatalf("promoted sub-agent lifecycle = %+v", messages)
	}
}

func TestAttachSubPaneCorrelatesParallelCallsAndRejectsUnmatched(t *testing.T) {
	conversation := NewConversation()
	conversation.UpdateToolCall("first", "first-id", "agent_start", "survey")
	conversation.UpdateToolCall("second", "second-id", "agent_start", "implement")
	if conversation.AttachSubPaneToTool("missing", "ghost", "ghost") ||
		!conversation.AttachSubPaneToTool("first", "child-a", "Lily — survey") ||
		!conversation.AttachSubPaneToTool("second", "child-b", "Jane — implement") ||
		conversation.AttachSubPaneToTool("first", "child-c", "duplicate") {
		t.Fatal("failed to correlate children by exact tool call IDs")
	}
	msgs := conversation.Messages()
	if len(msgs) != 2 || msgs[0].SubPaneID != "child-a" || msgs[1].SubPaneID != "child-b" {
		t.Fatalf("parallel child links crossed: %+v", msgs)
	}
}

func TestCancellingTurnStatusRejectsLateProviderPhase(t *testing.T) {
	state := NewAgentState("agent", nil, nil)
	state.SubmitInput("work")
	state.Cancelling = true
	state.Messages.UpdateTurnStatus(TurnStatus{Phase: "cancelling"})
	state.HandleEvent(
		AgentEvent{Type: "phase", Data: map[string]any{"name": "waiting_provider"}}, nil,
		func(string) {}, func(bool, bool, bool) {}, func() {},
		func(string, string, string, string, string, string, string) {}, func(string) {}, func() {},
	)
	messages := state.Messages.Messages()
	if len(messages) != 1 || messages[0].LifecyclePhase != "cancelling" {
		t.Fatalf("late provider phase replaced cancellation: %#v", messages)
	}
}

func TestMarkAllThinkingDoneRecordsDuration(t *testing.T) {
	conversation := NewConversation()
	conversation.AppendMessage(Message{
		Role:           MessageRoleThinking,
		Content:        "reasoning",
		Time:           time.Now().Add(-3 * time.Second),
		ThinkingActive: true,
	})
	conversation.MarkAllThinkingDone()
	messages := conversation.Messages()
	if len(messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(messages))
	}
	msg := messages[0]
	if msg.ThinkingActive || msg.ThinkingInterrupted {
		t.Fatalf("thinking block still active: %#v", msg)
	}
	if msg.ThinkingDuration < 3*time.Second {
		t.Fatalf("ThinkingDuration = %v, want >= 3s", msg.ThinkingDuration)
	}
	// Repeated MarkAllThinkingDone calls must not recompute the duration.
	first := msg.ThinkingDuration
	conversation.MarkAllThinkingDone()
	if got := conversation.Messages()[0].ThinkingDuration; got != first {
		t.Fatalf("duration recomputed on second call: %v -> %v", first, got)
	}
}

func TestMarkAllThinkingInterruptedRecordsDuration(t *testing.T) {
	conversation := NewConversation()
	conversation.AppendMessage(Message{
		Role:           MessageRoleThinking,
		Content:        "reasoning",
		Time:           time.Now().Add(-2 * time.Second),
		ThinkingActive: true,
	})
	conversation.MarkAllThinkingInterrupted()
	msg := conversation.Messages()[0]
	if !msg.ThinkingInterrupted {
		t.Fatalf("thinking block not interrupted: %#v", msg)
	}
	if msg.ThinkingDuration < 2*time.Second {
		t.Fatalf("ThinkingDuration = %v, want >= 2s", msg.ThinkingDuration)
	}
}

func TestCompactionOperationRowsSurviveTurnCleanupAndRemainIDScoped(t *testing.T) {
	conversation := NewConversation()
	conversation.UpdateTurnStatus(TurnStatus{
		Phase: "compaction_extracting", OperationID: "compact-a", StageName: "Capturing state",
		StageIndex: 3, Completed: 2, StageCount: 4,
	})
	conversation.UpdateTurnStatus(TurnStatus{Phase: "preparing"})
	conversation.ClearTurnStatus()
	messages := conversation.Messages()
	if len(messages) != 1 || messages[0].LifecycleOperationID != "compact-a" || messages[0].LifecycleStageName != "Capturing state" {
		t.Fatalf("durable operation row was consumed with turn status: %+v", messages)
	}
	conversation.UpdateTurnStatus(TurnStatus{
		Phase: "compaction_completed", OperationID: "compact-a", Completed: 4, StageCount: 4,
		BeforeTokens: 155955, AfterTokens: 10476,
	})
	conversation.UpdateTurnStatus(TurnStatus{
		Phase: "compaction_preparing", OperationID: "compact-b", StageCount: 4,
	})
	messages = conversation.Messages()
	if len(messages) != 2 || messages[0].LifecyclePhase != "compaction_completed" || messages[1].LifecycleOperationID != "compact-b" {
		t.Fatalf("operation IDs did not retain independent rows: %+v", messages)
	}
}

func TestAgentStateCompactionRetryAndCancellationRemainOneQuietBoundary(t *testing.T) {
	state := NewAgentState("agent", nil, nil)
	handle := func(event AgentEvent) {
		state.HandleEvent(event, nil, ignoreStatus, ignoreThinking, func() {}, ignoreConfirm, func(string) {}, func() {})
	}
	base := map[string]any{
		"request_id": "compact-1", "trigger": "manual", "role": "root",
		"stage": 2, "completed_stages": 1, "stage_count": 4,
		"stage_label": "Distilling work", "before_tokens": 155955,
	}
	handle(AgentEvent{Type: "context_compaction_started", Data: base})
	retry := make(map[string]any, len(base)+2)
	for key, value := range base {
		retry[key] = value
	}
	retry["retry"] = 2
	retry["retry_limit"] = 3
	retry["error"] = "download stalled"
	handle(AgentEvent{Type: "context_compaction_retrying", Data: retry})
	if messages := state.Messages.Messages(); len(messages) != 1 || messages[0].LifecycleRetryAttempt != 2 || messages[0].LifecycleOperationID != "compact-1" {
		t.Fatalf("retry lifecycle=%+v", messages)
	}
	state.BeginCancellation()
	if got := state.Messages.Messages()[0].LifecyclePhase; got != "compaction_cancelling" {
		t.Fatalf("cancelling phase=%q", got)
	}
	handle(AgentEvent{Type: "context_compaction_cancelled", Data: retry})
	messages := state.Messages.Messages()
	if len(messages) != 1 || messages[0].LifecyclePhase != "compaction_cancelled" || state.Running || state.Cancelling {
		t.Fatalf("cancelled lifecycle=%+v running=%v cancelling=%v", messages, state.Running, state.Cancelling)
	}
}

func TestCompletedCompactionStaysInNaturalFlowAndThinkingChunksCoalesceAfterIt(t *testing.T) {
	conversation := NewConversation()
	conversation.AppendMessage(Message{Role: MessageRoleUser, Content: "compact this"})
	conversation.UpdateTurnStatus(TurnStatus{
		Phase: "compaction_completed", OperationID: "compact-1", Completed: 4, StageCount: 4,
	})
	conversation.AppendMessage(Message{Role: MessageRoleUser, Content: "continue after compact"})
	conversation.AppendThinking("first ")
	conversation.AppendThinking("second")
	messages := conversation.Messages()
	if len(messages) != 4 {
		t.Fatalf("messages=%+v", messages)
	}
	if messages[1].LifecycleOperationID != "compact-1" || messages[2].Content != "continue after compact" {
		t.Fatalf("compaction boundary escaped chronological position: %+v", messages)
	}
	if messages[3].Role != MessageRoleThinking || messages[3].Content != "first second" {
		t.Fatalf("post-compaction thinking fragmented: %+v", messages)
	}
}

func TestAutomaticCompactionFailureDoesNotAppendDuplicateErrorRow(t *testing.T) {
	state := NewAgentState("agent", nil, nil)
	data := map[string]any{
		"request_id": "compact-failed", "trigger": "automatic", "role": "root",
		"stage": 3, "completed_stages": 2, "stage_count": 4,
		"stage_label": "Capturing state", "error": "download recovery exhausted",
	}
	state.HandleEvent(AgentEvent{Type: "context_compaction_failed", Data: data}, nil, ignoreStatus, ignoreThinking, func() {}, ignoreConfirm, func(string) {}, func() {})
	state.HandleEvent(AgentEvent{Type: "error", Data: "context compaction failed"}, nil, ignoreStatus, ignoreThinking, func() {}, ignoreConfirm, func(string) {}, func() {})
	messages := state.Messages.Messages()
	if len(messages) != 1 || messages[0].LifecyclePhase != "compaction_failed" || messages[0].Role != MessageRoleStatus {
		t.Fatalf("automatic failure produced duplicate transcript noise: %+v", messages)
	}
}

func ignoreStatus(string) {}

func ignoreThinking(bool, bool, bool) {}

func ignoreConfirm(string, string, string, string, string, string, string) {}
func TestCancellationDoesNotMarkPreviousTurnIncomplete(t *testing.T) {
	c := NewConversation()
	c.AppendMessage(Message{InternalID: "old", Role: MessageRoleAssistant, Content: "Prior turn completed"})
	c.AppendMessage(Message{InternalID: "new", Role: MessageRoleUser, Content: "New request"})
	c.Finish("", "cancelled", false, "stop")
	msgs := c.Messages()
	if msgs[0].Incomplete {
		t.Fatal("cancelling a new turn invalidated previous completed work")
	}
	last := msgs[len(msgs)-1]
	if last.Role != MessageRoleStatus || last.LifecyclePhase != "stopped" {
		t.Fatalf("missing clear stopped outcome: %+v", last)
	}
}
