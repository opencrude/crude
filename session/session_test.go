package session

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/logger"
	rootprompt "crude/prompt"
	"crude/provider"
	"crude/sessiondb"
	ttypes "crude/types"
)

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
	tracker.updateToolCall("call-1", "agent_start", "description: inspect wait: true")
	result := `{"session_id":"child-session","agent_id":"child-1","status":"completed"}`
	tracker.updateToolResult("call-1", "agent_start", result, "success", 1500, 42, 3, 1)
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
	if snapshot.ToolName != "agent_start" || snapshot.ToolStatus != "success" ||
		snapshot.ToolPreviewPhase != "done" || snapshot.SubPaneID != "child-1" ||
		snapshot.ToolDuration != 1500*time.Millisecond || snapshot.ToolEstTokens != 42 ||
		snapshot.ToolLinesAdded != 3 || snapshot.ToolLinesRemoved != 1 ||
		snapshot.TurnGeneration != 4 || snapshot.ProviderID != "ollama-cloud" || snapshot.ModelID != "kimi-k3" {
		t.Fatalf("canonical tool snapshot = %#v", snapshot)
	}
}

func TestReplyToolResultBecomesDurableProgressBoundary(t *testing.T) {
	call := provider.ToolCall{ID: "progress-1", Type: "function", Function: provider.FunctionCall{Name: "reply", Arguments: `{"content":"Checking tests."}`}}
	wire := []provider.ChatMessage{
		provider.NewTextMessage("user", "fix the issue"),
		{Role: "assistant", ToolCalls: []provider.ToolCall{call}},
		{Role: "tool", ToolCallID: call.ID, Content: provider.TextContent("Checking tests.")},
	}
	clean := sanitizeMessages(wire)
	if len(clean) != 3 || clean[2].ToolCallID != "progress-1" {
		t.Fatalf("reply tool result lost its durable identity: %+v", clean)
	}
	tracker := &displayMetaTracker{metas: map[string]*sessiondb.DisplayMeta{}}
	tracker.updateToolCall(call.ID, "reply", `content: "Checking tests."`)
	meta := canonicalizeDisplayMeta(clean[2], tracker.get(call.ID), call.Function.Arguments, 2, time.Unix(10, 0))
	if len(meta.Snapshots) != 1 || meta.Snapshots[0].Role != "assistant" || meta.Snapshots[0].Content != "Checking tests." || meta.Snapshots[0].InternalID != "reply:progress-1" {
		t.Fatalf("reply recovery snapshot = %+v", meta.Snapshots)
	}
	failed := clean[2]
	failed.ToolError = true
	meta = tracker.get(call.ID)
	meta.ToolStatus = "error"
	meta = canonicalizeDisplayMeta(failed, meta, call.Function.Arguments, 2, time.Unix(10, 0))
	if len(meta.Snapshots) != 1 || meta.Snapshots[0].Role != "error" || meta.Snapshots[0].InternalID != "reply:progress-1" {
		t.Fatalf("failed reply incorrectly shown as progress: %+v", meta.Snapshots)
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
	if err := s.finalizeTurn(agentMessages, 2, &displayMetaTracker{metas: make(map[string]*sessiondb.DisplayMeta)}, "answer", "msg", ttypes.TurnIdentity{}, active); err != nil {
		t.Fatal(err)
	}
	state, ok, err := sessiondb.LoadContextState(db, "session", "session")
	if err != nil || !ok || state.Generation < 3 || len(state.Messages) != 3 || !strings.Contains(state.Messages[0].Content.String(), "checkpoint") {
		t.Fatalf("context state=%#v ok=%v err=%v", state, ok, err)
	}
	if len(s.msgs) != 2 || s.msgs[0].Content.String() != "archived old request" || s.msgs[1].Content.String() != "answer" {
		t.Fatalf("full archive was replaced by active epoch: %#v", s.msgs)
	}
}

func TestManualCompactRebuildsActiveContextWithoutDeletingArchive(t *testing.T) {
	var calls int
	var requestModels []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		requestModels = append(requestModels, request.Model)
		calls++
		encoded, _ := json.Marshal(fmt.Sprintf("- stage: %d\n- exact evidence retained", calls))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%s},\"finish_reason\":\"stop\"}]}\n\n", encoded)
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	var history []provider.ChatMessage
	for i := 0; i < 20; i++ {
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
	db := sessiondb.OpenAndMigrateAt(t.TempDir())
	t.Cleanup(func() { _ = db.Close() })
	if err := sessiondb.CreateSessionRow(db, "compact"); err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.SaveContextState(db, "compact", "compact", sessiondb.ContextState{
		AgentID: "compact", Generation: 1, Reason: "turn_complete", Messages: history,
	}); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	profile.Categories[CategoryDefault] = CategoryConfig{AccountID: "account", Model: "main-test-model"}
	profile.Categories[CategoryAeron] = CategoryConfig{AccountID: "account", Model: "aeron-test-model"}
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	provider.SetModelContext("main-test-model", 200_000)
	provider.SetModelContext("aeron-test-model", 200_000)
	s := &Session{
		logger: lg, msgs: append([]provider.ChatMessage(nil), history...),
		activeContext: append([]provider.ChatMessage(nil), history...), contextGeneration: 1,
		cfg: cfg, sessionID: "compact", db: db,
	}
	account := s.cfg.Accounts["account"]
	providerConfig := account.Connection
	providerConfig.ProviderURL = server.URL
	account.Connection = providerConfig
	s.cfg.Accounts["account"] = account
	result := s.HandleCompact()
	if result.Error != "" || calls != 4 || len(s.msgs) != len(history) || len(s.activeContext) >= len(history) || s.contextGeneration <= 1 {
		t.Fatalf("compact result=%+v archive=%d active=%d generation=%d", result, len(s.msgs), len(s.activeContext), s.contextGeneration)
	}
	for index, model := range requestModels {
		if model != "aeron-test-model" {
			t.Fatalf("compaction request %d model=%q want Aeron category model", index, model)
		}
	}
	if !strings.Contains(s.activeContext[0].ContentString(), `version="3"`) {
		t.Fatalf("active checkpoint=%q", s.activeContext[0].ContentString())
	}
	records, err := sessiondb.ListCompactionRecords(db, "compact", "compact")
	if err != nil || len(records) != 1 || records[0].State != sessiondb.CompactionCompleted || records[0].CompletedStages != 4 || records[0].ModelID != "aeron-test-model" {
		t.Fatalf("compaction lifecycle=%+v err=%v", records, err)
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
	_ = s.finalizeTurn(
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

func TestFinalizeTurnPersistsHumanSteeringDisplayProjection(t *testing.T) {
	s := &Session{}
	steering := provider.ChatMessage{
		InternalID:      "steering-1",
		DisplayContent:  "please include tests",
		ReferenceLabels: []string{"@notes"},
		Role:            "user",
		Content:         provider.TextContent(rootprompt.HumanSteeringPrefix() + "please include tests"),
	}
	_ = s.finalizeTurn([]provider.ChatMessage{steering}, 0, &displayMetaTracker{metas: make(map[string]*sessiondb.DisplayMeta)}, "", "msg", ttypes.TurnIdentity{})
	messages := s.MessagesWithMeta()
	if len(messages) != 1 || messages[0].Meta.DisplayContent != "please include tests" ||
		len(messages[0].Meta.ReferenceLabels) != 1 || messages[0].Meta.ReferenceLabels[0] != "@notes" {
		t.Fatalf("persisted human steering display projection=%#v", messages)
	}
}

func TestPreparedCompactionOwnsCancellationBeforeCommandRuns(t *testing.T) {
	lg, err := logger.New(logger.Options{HomeDir: t.TempDir(), SessionID: "compact-cancel"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lg.Close() })
	s := &Session{
		sessionID: "session", logger: lg,
		activeContext: []provider.ChatMessage{provider.NewTextMessage("user", "request")},
	}
	prepared, early := s.PrepareCompaction("root")
	if early.Error != "" || prepared.Run == nil || !s.compactionActive("root") {
		t.Fatalf("prepared=%+v early=%+v active=%v", prepared, early, s.compactionActive("root"))
	}
	if !s.CancelCompaction("root") {
		t.Fatal("registered compaction did not accept cancellation")
	}
	result := prepared.Run()
	if !result.Cancelled || result.Error != "" || s.compactionActive("root") {
		t.Fatalf("cancel result=%+v active=%v", result, s.compactionActive("root"))
	}
}

func TestCloseReleasesPreparedCompactionThatNeverRan(t *testing.T) {
	lg, err := logger.New(logger.Options{HomeDir: t.TempDir(), SessionID: "compact-close"})
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{
		sessionID: "session", logger: lg,
		activeContext: []provider.ChatMessage{provider.NewTextMessage("user", "request")},
	}
	prepared, early := s.PrepareCompaction("root")
	if early.Error != "" || prepared.Run == nil {
		t.Fatalf("prepare failed: %+v", early)
	}
	done := make(chan struct{})
	go func() {
		s.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close waited forever for an unstarted compaction command")
	}
	if result := prepared.Run(); !result.Cancelled {
		t.Fatalf("late command run=%+v want cancelled", result)
	}
	_ = lg.Close()
}
