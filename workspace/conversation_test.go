package workspace

import (
	"reflect"
	"testing"
	"time"

	"crude/types/conversation"
	"crude/types/reasoning"
)

func TestConversationKeepsImmutableIdentityPerTurnDerivedMessage(t *testing.T) {
	transcript := NewConversation()
	first := conversation.TurnIdentity{Generation: 1, ProviderID: "provider-a", AccountID: "personal", ModelID: "model-a"}
	if !transcript.BeginTurn(first) {
		t.Fatal("first turn identity was rejected")
	}
	transcript.AppendThinking("reasoning")
	transcript.UpdateToolCall("tool-a", "internal-a", "read_file", "a.go")
	transcript.EnsureAssistant()
	transcript.AppendToken("first answer")

	second := conversation.TurnIdentity{Generation: 2, ProviderID: "provider-a", AccountID: "work", ModelID: "model-b"}
	if !transcript.BeginTurn(second) {
		t.Fatal("second turn identity was rejected")
	}
	transcript.AppendMessage(conversation.Message{Role: MessageRoleUser, Content: "continue"})
	transcript.EnsureAssistant()
	transcript.AppendToken("second answer")

	if transcript.BeginTurn(first) {
		t.Fatal("stale turn identity moved attribution backward")
	}
	messages := transcript.Messages()
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
	typeOfMessage := reflect.TypeOf(conversation.Message{})
	for _, name := range []string{"CachedLines", "CursorLine", "StreamCompletedLines", "StreamCompletedLen", "SessionPickerSessions"} {
		if _, ok := typeOfMessage.FieldByName(name); ok {
			t.Fatalf("workspace Message contains TUI presentation field %s", name)
		}
	}
}

func TestConversationStreamingLifecycle(t *testing.T) {
	transcript := NewConversation()
	agent := NewAgentState("agent", transcript, &testLogger{})
	identity := conversation.TurnIdentity{Generation: 3, ProviderID: "ollama-cloud", ModelID: "kimi-k3"}

	agent.ApplyEvent(AgentEvent{Type: "turn_started", AgentID: "agent", Data: identity})
	agent.ApplyEvent(AgentEvent{Type: "thinking", AgentID: "agent", Data: reasoning.Delta{Text: "considering"}})
	agent.ApplyEvent(AgentEvent{Type: "stream_token", AgentID: "agent", Data: "hello"})
	agent.Tick(1)

	messages := transcript.Messages()
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
	transcript := NewConversation()
	transcript.UpdateToolStreaming("tool-1", "internal-1", "read_file", map[string]any{
		"target":            "main.go",
		"content_lines_int": 12,
	})
	transcript.UpdateToolCall("tool-1", "internal-1", "read_file", "main.go")
	transcript.SetToolPermTag("tool-1", "internal-1", "allowed")
	transcript.UpdateToolResult("tool-1", "internal-1", "read_file", "contents", false, 20, 0, 0)

	messages := transcript.Messages()
	if len(messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(messages))
	}
	message := messages[0]
	if message.ToolPreviewPhase != "preparing" || message.ToolStatus == "success" {
		t.Fatalf("atomic tool skipped preparing: %#v", message)
	}
	firstTick := time.Now().Add(time.Second)
	if !transcript.AdvanceToolLifecycles(firstTick) {
		t.Fatal("tool did not advance to doing")
	}
	message = transcript.Messages()[0]
	if message.ToolPreviewPhase != "streaming" || message.ToolStatus == "success" {
		t.Fatalf("atomic tool skipped doing: %#v", message)
	}
	if !transcript.AdvanceToolLifecycles(firstTick.Add(time.Second)) {
		t.Fatal("tool did not advance to done")
	}
	message = transcript.Messages()[0]
	if message.ToolStatus != "success" || message.ToolPreviewPhase != "done" || message.Content != "contents" || message.PermTag != "allowed" {
		t.Fatalf("unexpected tool message: %#v", message)
	}
}

func TestConversationSnapshotDoesNotExposeMutableState(t *testing.T) {
	transcript := NewConversation()
	transcript.AppendMessage(conversation.Message{Role: MessageRoleUser, Content: "original"})

	snapshot := transcript.Messages()
	snapshot[0].Content = "mutated"

	if got := transcript.Messages()[0].Content; got != "original" {
		t.Fatalf("snapshot mutation changed conversation: %q", got)
	}
}

func TestConversationFinishCancelledPreservesContentAndAppendsStop(t *testing.T) {
	transcript := NewConversation()
	transcript.AppendMessage(conversation.Message{Role: MessageRoleAssistant, Content: "partial"})
	transcript.Finish("", "cancelled", "stop")

	message := transcript.Messages()[0]
	if message.Incomplete || message.Content != "partial" || len(transcript.Messages()) != 2 || transcript.Messages()[1].LifecyclePhase != "stopped" {
		t.Fatalf("unexpected cancelled message: %#v", message)
	}
}

func TestConversationThinkingRetainsIdentityAndPosition(t *testing.T) {
	transcript := NewConversation()
	transcript.BeginTurn(conversation.TurnIdentity{Generation: 2, ModelID: "model", ProviderID: "provider"})
	transcript.AppendThinking("private reasoning")
	original := transcript.Messages()[0]
	transcript.AppendThinking(" continued")
	transcript.MarkAllThinkingDone()
	transcript.AppendMessage(conversation.Message{Role: MessageRoleTool, ToolName: "read_file"})
	got := transcript.Messages()
	if len(got) != 2 || got[0].InternalID != original.InternalID || got[0].Content != "private reasoning continued" || got[0].ThinkingActive || got[0].SubPaneID != "" || got[0].ModelID != "model" {
		t.Fatalf("canonical reasoning lost identity or content: %+v", got)
	}
}

func TestConversationNewThinkingDoesNotExtendSettledBlock(t *testing.T) {
	transcript := NewConversation()
	transcript.AppendThinking("first block")
	transcript.MarkAllThinkingDone()
	first := transcript.Messages()[0]
	transcript.AppendThinking("second block")
	messages := transcript.Messages()
	if len(messages) != 2 || !reflect.DeepEqual(messages[0], first) || !messages[1].ThinkingActive || messages[1].InternalID == first.InternalID {
		t.Fatalf("new reasoning overwrote settled evidence: %+v", messages)
	}
}

func TestTurnStatusStaysLastAcrossSteeringAndTransformsInPlace(t *testing.T) {
	transcript := NewConversation()
	transcript.AppendMessage(conversation.Message{Role: MessageRoleUser, Content: "start"})
	transcript.UpdateTurnStatus(TurnStatus{Phase: "preparing", Progress: 25})
	statusID := transcript.Messages()[1].InternalID
	transcript.AppendMessage(conversation.Message{Role: MessageRoleUser, Content: "human steering: include tests"})

	messages := transcript.Messages()
	if len(messages) != 3 || messages[1].Content != "human steering: include tests" || messages[2].Role != MessageRoleStatus {
		t.Fatalf("human steering did not stay before live status: %#v", messages)
	}
	transcript.UpdateToolStreaming("tool-1", "internal-1", "read_file", map[string]any{"target": "main.go"})
	messages = transcript.Messages()
	if len(messages) != 3 || messages[2].Role != MessageRoleTool || messages[2].InternalID != statusID || messages[2].ToolID != "tool-1" {
		t.Fatalf("status did not transform in place after human steering: %#v", messages)
	}
}

func TestPendingSteeringSettlementRetainsEveryLocallyAcceptedMessage(t *testing.T) {
	transcript := NewConversation()
	consumed := transcript.AppendPendingSteering(conversation.Message{Role: MessageRoleUser, Content: "keep me"})
	cancelled := transcript.AppendPendingSteering(conversation.Message{Role: MessageRoleUser, Content: "keep stopped note"})
	if !transcript.ConsumePendingSteering(consumed) {
		t.Fatal("consumed human steering was not resolved")
	}
	if settled := transcript.SettlePendingSteeringIDs([]string{cancelled}); settled != 1 {
		t.Fatalf("settled=%d, want 1", settled)
	}
	messages := transcript.Messages()
	if len(messages) != 2 || messages[0].Content != "keep me" || messages[1].Content != "keep stopped note" || messages[0].PendingSteering || messages[1].PendingSteering {
		t.Fatalf("canonical messages=%#v", messages)
	}
}

func TestPendingSteeringStaysAfterProviderOutputUntilAcknowledged(t *testing.T) {
	transcript := NewConversation()
	transcript.UpdateTurnStatus(TurnStatus{Phase: "waiting_provider"})
	pendingID := transcript.AppendPendingSteering(conversation.Message{Role: MessageRoleUser, Content: "include tests"})
	transcript.UpdateToolStreaming("tool-1", "internal-1", "write_file", map[string]any{
		"target": "main.go", "content_lines_int": 3,
	})

	messages := transcript.Messages()
	if len(messages) != 2 || messages[0].Role != MessageRoleTool || !messages[1].PendingSteering || messages[1].InternalID != pendingID {
		t.Fatalf("pending human steering was not pinned after live output: %#v", messages)
	}
	if !transcript.ConsumePendingSteering(pendingID) {
		t.Fatal("pending human steering acknowledgement was rejected")
	}
	transcript.AppendMessage(conversation.Message{Role: MessageRoleAssistant, Content: "now I saw it"})
	messages = transcript.Messages()
	if len(messages) != 3 || messages[1].InternalID != pendingID || messages[1].PendingSteering || messages[2].Role != MessageRoleAssistant {
		t.Fatalf("acknowledged human steering did not rejoin chronology: %#v", messages)
	}
}

func TestStreamCompletionWithQueuedTailDoesNotDuplicateAnswer(t *testing.T) {
	c := NewConversation()
	c.AppendPendingSteering(conversation.Message{InternalID: "next", Role: MessageRoleUser, Content: "next prompt"})
	c.EnsureAssistant()
	c.AppendToken("one accepted answer")
	c.Finish("one accepted answer", "completed", "")
	messages := c.Messages()
	if len(messages) != 2 || messages[0].Role != MessageRoleAssistant || messages[0].Content != "one accepted answer" || messages[1].InternalID != "next" || !messages[1].PendingSteering {
		t.Fatalf("completion duplicated output or consumed pending input: %+v", messages)
	}
}

func TestSteeringAdmissionMovesAboveTurnIndicatorAndRejectedNotesReturn(t *testing.T) {
	transcript := NewConversation()
	transcript.UpdateTurnStatus(TurnStatus{Phase: "preparing"})
	first := transcript.AppendPendingSteering(conversation.Message{Role: MessageRoleUser, Content: "first"})
	second := transcript.AppendPendingSteering(conversation.Message{Role: MessageRoleUser, Content: "second"})
	if !transcript.ConsumePendingSteering(first) {
		t.Fatal("first upload was not acknowledged")
	}
	messages := transcript.Messages()
	if len(messages) != 3 || messages[0].InternalID != first || messages[0].PendingSteering || messages[1].Role != MessageRoleStatus || messages[2].InternalID != second || !messages[2].PendingSteering {
		t.Fatalf("upload handoff did not move note above indicator: %+v", messages)
	}
	settled := transcript.SettlePendingSteeringMessages([]string{first, second})
	if len(settled) != 2 || settled[0].Content != "first" || settled[1].Content != "second" || len(transcript.Messages()) != 3 {
		t.Fatalf("stopped upload did not retain both notes in order: %+v, remaining %+v", settled, transcript.Messages())
	}
}

func TestAttachSubPanePromotesExistingSubAgentToolWithoutDuplicate(t *testing.T) {
	transcript := NewConversation()
	transcript.UpdateToolCall("tool-1", "internal-1", "agent_start", "category: researcher description: survey")
	if !transcript.AttachSubPaneToTool("tool-1", "child-1", "Lily — survey") {
		t.Fatal("active sub-agent tool row was not promoted")
	}
	transcript.UpdateToolResult("tool-1", "internal-1", "agent_start", "completed", false, 0, 0, 0)
	now := time.Now().Add(time.Second)
	transcript.AdvanceToolLifecycles(now)
	transcript.AdvanceToolLifecycles(now.Add(time.Second))
	messages := transcript.Messages()
	if len(messages) != 1 || messages[0].SubPaneID != "child-1" || messages[0].ToolProgressLabel != "Lily — survey" || messages[0].ToolStatus == "calling" {
		t.Fatalf("promoted sub-agent lifecycle = %+v", messages)
	}
}

func TestAttachSubPaneCorrelatesParallelCallsAndRejectsUnmatched(t *testing.T) {
	transcript := NewConversation()
	transcript.UpdateToolCall("first", "first-id", "agent_start", "survey")
	transcript.UpdateToolCall("second", "second-id", "agent_start", "implement")
	if transcript.AttachSubPaneToTool("missing", "ghost", "ghost") ||
		!transcript.AttachSubPaneToTool("first", "child-a", "Lily — survey") ||
		!transcript.AttachSubPaneToTool("second", "child-b", "Jane — implement") ||
		transcript.AttachSubPaneToTool("first", "child-c", "duplicate") {
		t.Fatal("failed to correlate children by exact tool call IDs")
	}
	msgs := transcript.Messages()
	if len(msgs) != 2 || msgs[0].SubPaneID != "child-a" || msgs[1].SubPaneID != "child-b" {
		t.Fatalf("parallel child links crossed: %+v", msgs)
	}
}

func TestCancellingTurnStatusRejectsLateProviderPhase(t *testing.T) {
	state := NewAgentState("agent", nil, nil)
	state.SubmitInput("work")
	state.Cancelling = true
	state.Messages.UpdateTurnStatus(TurnStatus{Phase: "cancelling"})
	state.ApplyEvent(
		AgentEvent{Type: "phase", Data: map[string]any{"name": "waiting_provider"}})
	messages := state.Messages.Messages()
	if len(messages) != 1 || messages[0].LifecyclePhase != "cancelling" {
		t.Fatalf("late provider phase replaced cancellation: %#v", messages)
	}
}

func TestMarkAllThinkingDoneRecordsDuration(t *testing.T) {
	transcript := NewConversation()
	transcript.AppendMessage(conversation.Message{
		Role:           MessageRoleThinking,
		Content:        "reasoning",
		Time:           time.Now().Add(-3 * time.Second),
		ThinkingActive: true,
	})
	transcript.MarkAllThinkingDone()
	messages := transcript.Messages()
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
	transcript.MarkAllThinkingDone()
	if got := transcript.Messages()[0].ThinkingDuration; got != first {
		t.Fatalf("duration recomputed on second call: %v -> %v", first, got)
	}
}

func TestMarkAllThinkingInterruptedRecordsDuration(t *testing.T) {
	transcript := NewConversation()
	transcript.AppendMessage(conversation.Message{
		Role:           MessageRoleThinking,
		Content:        "reasoning",
		Time:           time.Now().Add(-2 * time.Second),
		ThinkingActive: true,
	})
	transcript.MarkAllThinkingInterrupted()
	msg := transcript.Messages()[0]
	if !msg.ThinkingInterrupted {
		t.Fatalf("thinking block not interrupted: %#v", msg)
	}
	if msg.ThinkingDuration < 2*time.Second {
		t.Fatalf("ThinkingDuration = %v, want >= 2s", msg.ThinkingDuration)
	}
}

func TestCompactionOperationRowsSurviveTurnCleanupAndRemainIDScoped(t *testing.T) {
	transcript := NewConversation()
	transcript.UpdateTurnStatus(TurnStatus{
		Phase: "compaction_extracting", OperationID: "compact-a", StageName: "Capturing state",
		StageIndex: 3, Completed: 2, StageCount: 4,
	})
	transcript.UpdateTurnStatus(TurnStatus{Phase: "preparing"})
	transcript.ClearTurnStatus()
	messages := transcript.Messages()
	if len(messages) != 1 || messages[0].LifecycleOperationID != "compact-a" || messages[0].LifecycleStageName != "Capturing state" {
		t.Fatalf("durable operation row was consumed with turn status: %+v", messages)
	}
	transcript.UpdateTurnStatus(TurnStatus{
		Phase: "compaction_completed", OperationID: "compact-a", Completed: 4, StageCount: 4,
		BeforeTokens: 155955, AfterTokens: 10476,
	})
	transcript.UpdateTurnStatus(TurnStatus{
		Phase: "compaction_preparing", OperationID: "compact-b", StageCount: 4,
	})
	messages = transcript.Messages()
	if len(messages) != 2 || messages[0].LifecyclePhase != "compaction_completed" || messages[1].LifecycleOperationID != "compact-b" {
		t.Fatalf("operation IDs did not retain independent rows: %+v", messages)
	}
}

func TestAgentStateCompactionRetryAndCancellationRemainOneQuietBoundary(t *testing.T) {
	state := NewAgentState("agent", nil, nil)
	handle := func(event AgentEvent) {
		state.ApplyEvent(event)
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
	transcript := NewConversation()
	transcript.AppendMessage(conversation.Message{Role: MessageRoleUser, Content: "compact this"})
	transcript.UpdateTurnStatus(TurnStatus{
		Phase: "compaction_completed", OperationID: "compact-1", Completed: 4, StageCount: 4,
	})
	transcript.AppendMessage(conversation.Message{Role: MessageRoleUser, Content: "continue after compact"})
	transcript.AppendThinking("first ")
	transcript.AppendThinking("second")
	messages := transcript.Messages()
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
	state.ApplyEvent(AgentEvent{Type: "context_compaction_failed", Data: data})
	state.ApplyEvent(AgentEvent{Type: "error", Data: "context compaction failed"})
	messages := state.Messages.Messages()
	if len(messages) != 1 || messages[0].LifecyclePhase != "compaction_failed" || messages[0].Role != MessageRoleStatus {
		t.Fatalf("automatic failure produced duplicate transcript noise: %+v", messages)
	}
}

func TestCancellationDoesNotMarkPreviousTurnIncomplete(t *testing.T) {
	c := NewConversation()
	c.AppendMessage(conversation.Message{InternalID: "old", Role: MessageRoleAssistant, Content: "Prior turn completed"})
	c.AppendMessage(conversation.Message{InternalID: "new", Role: MessageRoleUser, Content: "New request"})
	c.Finish("", "cancelled", "stop")
	msgs := c.Messages()
	if msgs[0].Incomplete {
		t.Fatal("cancelling a new turn invalidated previous completed work")
	}
	last := msgs[len(msgs)-1]
	if last.Role != MessageRoleStatus || last.LifecyclePhase != "stopped" {
		t.Fatalf("missing clear stopped outcome: %+v", last)
	}
}
