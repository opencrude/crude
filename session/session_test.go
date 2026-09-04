package session

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/agent"
	"crude/logger"
	"crude/provider"
	"crude/sessiondb"
	ttypes "crude/types"
)

func TestDetectInterruption_Empty(t *testing.T) {
	ok, _ := detectInterruption(nil)
	if ok {
		t.Error("empty messages should not be interrupted")
	}
}

func TestDetectInterruption_CleanClose(t *testing.T) {
	msgs := []provider.ChatMessage{
		{Role: "user", Content: provider.TextContent("hi")},
		{Role: "assistant", Content: provider.TextContent("hello")},
	}
	ok, _ := detectInterruption(msgs)
	if ok {
		t.Error("clean assistant-ending session should not be interrupted")
	}
}

func TestDetectInterruption_MidToolCall(t *testing.T) {
	msgs := []provider.ChatMessage{
		{Role: "user", Content: provider.TextContent("read file")},
		{Role: "assistant", Content: provider.TextContent(""), ToolCalls: []provider.ToolCall{
			{ID: "tc1", Type: "function", Function: provider.FunctionCall{Name: "read"}},
		}},
	}
	ok, action := detectInterruption(msgs)
	if !ok {
		t.Error("mid-tool-call should be interrupted")
	}
	if action == "" {
		t.Error("action should not be empty for mid-tool-call")
	}
}

func TestDetectInterruption_ToolResult(t *testing.T) {
	msgs := []provider.ChatMessage{
		{Role: "user", Content: provider.TextContent("hi")},
		{Role: "assistant", Content: provider.TextContent("let me check"), ToolCalls: []provider.ToolCall{
			{ID: "tc1", Type: "function", Function: provider.FunctionCall{Name: "read"}},
		}},
		{Role: "tool", Content: provider.TextContent("file contents"), ToolCallID: "tc1"},
	}
	ok, action := detectInterruption(msgs)
	if !ok {
		t.Error("orphaned tool result should be interrupted")
	}
	if action == "" {
		t.Error("action should not be empty for tool result")
	}
}

func TestDetectInterruption_UnansweredUser(t *testing.T) {
	msgs := []provider.ChatMessage{
		{Role: "user", Content: provider.TextContent("do something")},
	}
	ok, action := detectInterruption(msgs)
	if !ok {
		t.Error("unanswered user message should be interrupted")
	}
	if action == "" {
		t.Error("action should not be empty for unanswered user")
	}
}

func TestSanitizeMessages_CleanTail(t *testing.T) {
	msgs := []provider.ChatMessage{
		{Role: "user", Content: provider.TextContent("hi")},
		{Role: "assistant", Content: provider.TextContent("hello")},
	}
	result := sanitizeMessages(msgs)
	if len(result) != 2 {
		t.Errorf("expected 2, got %d", len(result))
	}
}

func TestSanitizeMessages_TrailingTool(t *testing.T) {
	msgs := []provider.ChatMessage{
		{Role: "user", Content: provider.TextContent("hi")},
		{Role: "assistant", Content: provider.TextContent(""), ToolCalls: []provider.ToolCall{
			{ID: "tc1", Type: "function", Function: provider.FunctionCall{Name: "read"}},
		}},
		{Role: "tool", Content: provider.TextContent("data"), ToolCallID: "tc1"},
	}
	result := sanitizeMessages(msgs)
	if len(result) < 1 {
		t.Errorf("expected >=1, got %d", len(result))
	}
}

func TestSanitizeMessages_TrailingToolNoAssistant(t *testing.T) {
	msgs := []provider.ChatMessage{
		{Role: "user", Content: provider.TextContent("hi")},
		{Role: "tool", Content: provider.TextContent("orphan")},
	}
	result := sanitizeMessages(msgs)
	if len(result) != 1 {
		t.Errorf("expected 1 (orphan tool removed), got %d", len(result))
	}
}

func TestSanitizeMessages_OrphanedToolCalls(t *testing.T) {
	msgs := []provider.ChatMessage{
		{Role: "user", Content: provider.TextContent("hi")},
		{Role: "assistant", Content: provider.TextContent(""), ToolCalls: []provider.ToolCall{
			{ID: "tc1", Type: "function", Function: provider.FunctionCall{Name: "read"}},
			{ID: "tc2", Type: "function", Function: provider.FunctionCall{Name: "write"}},
		}},
		{Role: "tool", Content: provider.TextContent("data"), ToolCallID: "tc1"},
	}
	result := sanitizeMessages(msgs)
	if len(result) != 4 {
		t.Errorf("expected 4 (missing result backfilled, context preserved), got %d", len(result))
	}
	last := result[len(result)-1]
	if last.Role != "tool" || last.ToolCallID != "tc2" {
		t.Errorf("expected backfilled tool result for tc2, got role=%s id=%s", last.Role, last.ToolCallID)
	}
	if !strings.HasPrefix(last.ContentString(), "[cancelled]") {
		t.Errorf("expected backfilled result to start with [cancelled], got %q", last.ContentString())
	}
	if !last.ToolError {
		t.Error("backfilled cancellation should be marked as a tool error")
	}
}

func TestPersistedToolStatusDoesNotMislabelValidationErrorAsCancelled(t *testing.T) {
	validation := provider.ChatMessage{
		Role: "tool", Content: provider.TextContent("Error: question validation failed: header missing"), ToolError: true,
	}
	if got := persistedToolStatus(validation, ""); got != "error" {
		t.Fatalf("validation status = %q, want error", got)
	}
	cancelled := provider.ChatMessage{
		Role: "tool", Content: provider.TextContent("[cancelled] operation cancelled"), ToolError: true,
	}
	if got := persistedToolStatus(cancelled, ""); got != "cancelled" {
		t.Fatalf("cancellation status = %q, want cancelled", got)
	}
}

func TestCanonicalToolSnapshotCarriesLiveLifecycleAndSubPaneLink(t *testing.T) {
	tracker := &displayMetaTracker{metas: make(map[string]*sessiondb.DisplayMeta)}
	tracker.updateToolCall("call-1", "run_sub_agent", "description: inspect wait: true")
	result := "## Sub-Agent Result\n**Session:** child-1\n**Status:** OK"
	tracker.updateToolResult("call-1", "run_sub_agent", result, "success", 1500, 42, 3, 1)
	message := provider.NewTextMessage("tool", result)
	message.ToolCallID = "call-1"
	tracked := tracker.get("call-1")
	tracked.TurnGeneration = 4
	tracked.ProviderID = "ollama-cloud"
	tracked.ModelID = "kimi-k3"
	meta := canonicalizeDisplayMeta(message, tracked, `{"description":"inspect","wait":true}`, 3, time.Now())
	if meta.SnapshotVersion != sessiondb.ConversationSnapshotVersion || len(meta.Snapshots) != 1 {
		t.Fatalf("canonical metadata = %#v", meta)
	}
	snapshot := meta.Snapshots[0]
	if snapshot.ToolName != "run_sub_agent" || snapshot.ToolStatus != "success" ||
		snapshot.ToolPreviewPhase != "done" || snapshot.SubPaneID != "child-1" ||
		snapshot.ToolDuration != 1500*time.Millisecond || snapshot.ToolEstTokens != 42 ||
		snapshot.ToolLinesAdded != 3 || snapshot.ToolLinesRemoved != 1 ||
		snapshot.TurnGeneration != 4 || snapshot.ProviderID != "ollama-cloud" || snapshot.ModelID != "kimi-k3" {
		t.Fatalf("canonical tool snapshot = %#v", snapshot)
	}
}

func TestCanonicalAssistantAndThinkingSnapshotsShareTurnIdentity(t *testing.T) {
	message := provider.NewTextMessage("assistant", "answer")
	message.ReasoningContent = "reasoning"
	meta := canonicalizeDisplayMeta(message, sessiondb.DisplayMeta{
		DisplayRole: "assistant", TurnGeneration: 9,
		ProviderID: "commandcode", ModelID: "z-ai/glm-5.3-flash",
	}, "", 0, time.Now())
	if len(meta.Snapshots) != 2 {
		t.Fatalf("assistant snapshots = %#v", meta.Snapshots)
	}
	for _, snapshot := range meta.Snapshots {
		if snapshot.TurnGeneration != 9 || snapshot.ProviderID != "commandcode" || snapshot.ModelID != "z-ai/glm-5.3-flash" {
			t.Fatalf("snapshot attribution = %#v", snapshot)
		}
	}
}

func TestCanonicalUserSnapshotPreservesDurableMessageIDForHistory(t *testing.T) {
	message := provider.NewTextMessage("user", "change the files")
	message.InternalID = "msg-42"
	meta := canonicalizeDisplayMeta(message, sessiondb.DisplayMeta{DisplayRole: "user", DisplayContent: "change the files"}, "", 0, time.Now())
	if len(meta.Snapshots) != 1 || meta.Snapshots[0].InternalID != "msg-42" {
		t.Fatalf("durable user message identity = %#v", meta.Snapshots)
	}
}

func TestAugmentedProviderUserPersistsWhileDisplaySnapshotStaysOriginal(t *testing.T) {
	message := provider.ChatMessage{
		InternalID: "msg-1", Role: "user",
		Content:        provider.TextContent("actual request\n\n<turn_context>recalled fact</turn_context>"),
		DisplayContent: "actual request",
	}
	meta := canonicalizeDisplayMeta(message, sessiondb.DisplayMeta{
		DisplayRole: "user", DisplayContent: "actual request",
	}, "", 0, time.Now())
	if len(meta.Snapshots) != 1 || meta.Snapshots[0].Content != "actual request" ||
		!strings.Contains(message.Content.String(), "recalled fact") {
		t.Fatalf("provider=%q display=%#v", message.Content.String(), meta.Snapshots)
	}
}

func TestFinalizeTurnPersistsActiveContextSeparatelyFromFullArchive(t *testing.T) {
	db, err := sessiondb.OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sessiondb.Create(db, sessiondb.SessionMeta{ID: "session", ProjectPath: "/project", ProjectName: "project", CreatedAt: time.Now().UnixNano()}); err != nil {
		t.Fatal(err)
	}
	lg, err := logger.New(logger.Options{HomeDir: t.TempDir(), SessionID: "session"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lg.Close() })
	s := &Session{
		db: db, sessionID: "session", logger: lg, turnNumber: 1,
		msgs:     []provider.ChatMessage{provider.NewTextMessage("user", "archived old request")},
		msgTurns: []int{1}, displayMetas: []sessiondb.DisplayMeta{{DisplayRole: "user"}},
		contextGeneration: 3, contextReason: "automatic_compaction",
	}
	active := []provider.ChatMessage{
		provider.NewTextMessage("system", "<context_checkpoint>checkpoint</context_checkpoint>"),
		provider.NewTextMessage("user", "current request"),
		provider.NewTextMessage("assistant", "answer"),
	}
	agentMessages := append([]provider.ChatMessage{provider.NewTextMessage("system", "base")}, active...)
	s.finalizeTurn(agentMessages, 2, &displayMetaTracker{metas: make(map[string]*sessiondb.DisplayMeta)}, "answer", "msg", ttypes.TurnIdentity{}, active)
	s.flushWg.Wait()
	state, ok, err := sessiondb.LoadContextState(db, "session")
	if err != nil || !ok || state.Generation < 3 || len(state.Messages) != 3 || !strings.Contains(state.Messages[0].Content.String(), "checkpoint") {
		t.Fatalf("context state=%#v ok=%v err=%v", state, ok, err)
	}
	if len(s.msgs) != 2 || s.msgs[0].Content.String() != "archived old request" || s.msgs[1].Content.String() != "answer" {
		t.Fatalf("full archive was replaced by active epoch: %#v", s.msgs)
	}
}

func TestManualCompactRebuildsActiveContextWithoutDeletingArchive(t *testing.T) {
	var history []provider.ChatMessage
	for i := 0; i < 80; i++ {
		history = append(history,
			provider.NewTextMessage("assistant", strings.Repeat("analysis ", 1000)),
			provider.NewTextMessage("tool", strings.Repeat("output ", 1000)),
		)
	}
	lg, err := logger.New(logger.Options{HomeDir: t.TempDir(), SessionID: "compact"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lg.Close() })
	s := &Session{
		logger: lg, msgs: append([]provider.ChatMessage(nil), history...),
		activeContext: append([]provider.ChatMessage(nil), history...), contextGeneration: 1,
	}
	result := s.HandleCompact()
	if result.Error != "" || len(s.msgs) != len(history) || len(s.activeContext) >= len(history) || s.contextGeneration <= 1 {
		t.Fatalf("compact result=%+v archive=%d active=%d generation=%d", result, len(s.msgs), len(s.activeContext), s.contextGeneration)
	}
}

func TestSanitizeMessages_RemovesMiddleOrphanToolResult(t *testing.T) {
	msgs := []provider.ChatMessage{
		{Role: "user", Content: provider.TextContent("hi")},
		{Role: "tool", ToolCallID: "orphan", Content: provider.TextContent("invalid")},
		{Role: "assistant", Content: provider.TextContent("hello")},
	}
	result := sanitizeMessages(msgs)
	if len(result) != 2 || result[0].Role != "user" || result[1].Role != "assistant" {
		t.Fatalf("orphan tool result survived sanitization: %+v", result)
	}
}

func TestTruncateMessages_NoTruncation(t *testing.T) {
	msgs := []provider.ChatMessage{
		{Role: "user", Content: provider.TextContent("hi")},
		{Role: "assistant", Content: provider.TextContent("hello")},
	}
	result := agent.CompactContext(msgs, 100000, 75, "test")
	if len(result) != 2 {
		t.Errorf("expected 2, got %d", len(result))
	}
}

func TestTruncateMessages_ZeroWindow(t *testing.T) {
	msgs := []provider.ChatMessage{
		{Role: "user", Content: provider.TextContent("hi")},
		{Role: "assistant", Content: provider.TextContent("hello")},
	}
	result := agent.CompactContext(msgs, 0, 75, "test")
	if len(result) != 2 {
		t.Errorf("expected 2 (zero window = no truncation), got %d", len(result))
	}
}

func TestTruncateMessagesKeepsToolExchangeAtomic(t *testing.T) {
	msgs := []provider.ChatMessage{
		{Role: "user", Content: provider.TextContent(strings.Repeat("old context ", 100))},
		{
			Role:    "assistant",
			Content: provider.TextContent(""),
			ToolCalls: []provider.ToolCall{
				{ID: "tc1", Type: "function", Function: provider.FunctionCall{Name: "read", Arguments: `{"path":"a"}`}},
				{ID: "tc2", Type: "function", Function: provider.FunctionCall{Name: "read", Arguments: `{"path":"b"}`}},
			},
		},
		{Role: "tool", ToolCallID: "tc1", Content: provider.TextContent(strings.Repeat("result ", 100))},
		{Role: "tool", ToolCallID: "tc2", Content: provider.TextContent(strings.Repeat("result ", 100))},
	}
	result := agent.CompactContext(msgs, 80, 75, "test")
	if len(result) != 4 {
		t.Fatalf("result length = %d, want summary plus complete tool exchange: %+v", len(result), result)
	}
	if result[0].Role != "system" || result[1].Role != "assistant" ||
		len(result[1].ToolCalls) != 2 || result[2].ToolCallID != "tc1" ||
		result[3].ToolCallID != "tc2" {
		t.Fatalf("tool exchange was split: %+v", result)
	}
}

func TestTruncateMessagesBuildsUsefulCheckpoint(t *testing.T) {
	msgs := []provider.ChatMessage{
		provider.NewTextMessage("user", strings.Repeat("investigate the parser bug ", 40)),
		provider.NewTextMessage("assistant", "The parser drops CR-only SSE boundaries."),
		{
			Role: "assistant",
			ToolCalls: []provider.ToolCall{{
				ID:       "call-1",
				Function: provider.FunctionCall{Name: "read_file", Arguments: `{"path":"provider/sse.go"}`},
			}},
		},
		{Role: "tool", ToolCallID: "call-1", Content: provider.TextContent("reader implementation")},
		provider.NewTextMessage("user", strings.Repeat("continue ", 80)),
	}
	result := agent.CompactContext(msgs, 120, 75, "test")
	if len(result) >= len(msgs) {
		t.Fatalf("messages were not compacted: %d", len(result))
	}
	checkpoint := result[0].ContentString()
	for _, want := range []string{"<context_checkpoint", "Tool call-1"} {
		if !strings.Contains(checkpoint, want) {
			t.Fatalf("checkpoint missing %q: %s", want, checkpoint)
		}
	}
}

func TestFinalizeTurnKeepsDurableHistoryOutsideCompactedContext(t *testing.T) {
	history := []provider.ChatMessage{
		provider.NewTextMessage("user", "old request"),
		provider.NewTextMessage("assistant", "old answer"),
		provider.NewTextMessage("user", "current request"),
	}
	s := &Session{
		msgs:         append([]provider.ChatMessage(nil), history...),
		msgTurns:     []int{1, 1, 2},
		displayMetas: make([]sessiondb.DisplayMeta, 3),
		turnNumber:   2,
	}
	context := []provider.ChatMessage{
		provider.NewTextMessage("user", "<context_checkpoint>old state</context_checkpoint>"),
		history[2],
	}
	agentMessages := []provider.ChatMessage{
		provider.NewTextMessage("system", "system"),
		context[0],
		context[1],
		provider.NewTextMessage("assistant", "new answer"),
	}
	s.finalizeTurn(
		agentMessages, len(context),
		&displayMetaTracker{metas: make(map[string]*sessiondb.DisplayMeta)},
		"new answer", "msg-2", ttypes.TurnIdentity{},
	)
	if len(s.msgs) != 4 {
		t.Fatalf("durable history length = %d: %+v", len(s.msgs), s.msgs)
	}
	if s.msgs[0].ContentString() != "old request" ||
		s.msgs[3].ContentString() != "new answer" {
		t.Fatalf("durable history was replaced by compacted projection: %+v", s.msgs)
	}
	for _, message := range s.msgs {
		if strings.Contains(message.ContentString(), "<context_checkpoint>") {
			t.Fatal("ephemeral compaction checkpoint leaked into durable history")
		}
	}
}

func TestHardCancelSignalsEveryOrchestrator(t *testing.T) {
	s := &Session{orchestrators: make(map[string]*Orchestrator)}
	first := NewOrchestrator("first", s)
	second := NewOrchestrator("second", s)
	s.orchestrators[first.ID] = first
	s.orchestrators[second.ID] = second
	if !first.SubmitInput(provider.PlainInput("first")) || !second.SubmitInput(provider.PlainInput("second")) {
		t.Fatal("failed to admit test turns")
	}

	s.HardCancel()
	for _, orchestrator := range []*Orchestrator{first, second} {
		request := <-orchestrator.inputCh
		select {
		case <-request.ctx.Done():
		default:
			t.Fatalf("orchestrator %q context was not cancelled", orchestrator.ID)
		}
	}
}

func TestFinalizeTurnPersistsBtwDisplayProjection(t *testing.T) {
	s := &Session{}
	btw := provider.ChatMessage{
		InternalID:      "btw-1",
		DisplayContent:  "please include tests",
		ReferenceLabels: []string{"@notes"},
		Role:            "user",
		Content:         provider.TextContent(btwUrgentPrefix + "please include tests"),
	}
	s.finalizeTurn([]provider.ChatMessage{btw}, 0, &displayMetaTracker{metas: make(map[string]*sessiondb.DisplayMeta)}, "", "msg", ttypes.TurnIdentity{})
	messages := s.MessagesWithMeta()
	if len(messages) != 1 || messages[0].Meta.DisplayContent != "please include tests" ||
		len(messages[0].Meta.ReferenceLabels) != 1 || messages[0].Meta.ReferenceLabels[0] != "@notes" {
		t.Fatalf("persisted BTW display projection=%#v", messages)
	}
}
