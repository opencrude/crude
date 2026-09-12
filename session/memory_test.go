package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"crude/memory"
	"crude/provider"

	_ "modernc.org/sqlite"
)

func newTestMemoryStore(t *testing.T) *memory.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workspace.db")
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	store, err := memory.Attach(db)
	if err != nil {
		t.Fatalf("memory.Attach: %v", err)
	}
	return store
}

func TestMemoryToolsAndAutomaticRecall(t *testing.T) {
	store := newTestMemoryStore(t)
	s := &Session{sessionID: "session-1", memoryStore: store}
	result, handled, err := s.executeMemoryTool(context.Background(), "remember", map[string]any{
		"title": "Testing convention", "content": "Use race tests before handoff.",
		"kind": "convention", "importance": float64(70),
		"paths": []any{"tui/editor/search.go"},
	})
	if err != nil || !handled || result.Content == "" {
		t.Fatalf("remember result=%+v handled=%v err=%v", result, handled, err)
	}

	saved, err := store.Recall(context.Background(), memory.RecallQuery{Text: "race tests"})
	if err != nil || len(saved) != 1 {
		t.Fatalf("stored memories = %+v err=%v", saved, err)
	}
	if saved[0].SourceSessionID != "session-1" || saved[0].Importance != 70 {
		t.Fatalf("stored memory = %+v", saved[0])
	}
	if len(saved[0].ScopePaths) != 1 || saved[0].ScopePaths[0] != "tui/editor/search.go" {
		t.Fatalf("scope paths = %+v", saved[0].ScopePaths)
	}

	contextText := s.recallMemoryContext(context.Background(), "testing convention", nil)
	if contextText == "" || !containsAll(contextText, "<workspace_memory>", "Use race tests before handoff.") {
		t.Fatalf("memory context = %q", contextText)
	}

	// Injection reinforces usage.
	got, err := store.Get(context.Background(), saved[0].ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.UseCount == 0 {
		t.Fatalf("injected memory was not touched: %+v", got)
	}
}

func TestMemoryToolUpdateAndForget(t *testing.T) {
	store := newTestMemoryStore(t)
	s := &Session{sessionID: "session-1", memoryStore: store}
	ctx := context.Background()

	saveResult, _, err := s.executeMemoryTool(ctx, "remember", map[string]any{
		"title": "Build gate", "content": "Run go build only.",
	})
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	var savedPayload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(saveResult.Content), &savedPayload); err != nil {
		t.Fatalf("parse remember payload: %v", err)
	}

	updateResult, _, err := s.executeMemoryTool(ctx, "update_memory", map[string]any{
		"id": savedPayload.ID, "content": "Run go build ./... and go test ./...",
	})
	if err != nil {
		t.Fatalf("update_memory: %v", err)
	}
	if !strings.Contains(updateResult.Content, `"status":"updated"`) {
		t.Fatalf("update result = %q", updateResult.Content)
	}
	got, err := store.Get(ctx, savedPayload.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Content != "Run go build ./... and go test ./..." {
		t.Fatalf("updated content = %q", got.Content)
	}

	if _, _, err := s.executeMemoryTool(ctx, "forget_memory", map[string]any{
		"id": savedPayload.ID,
	}); err != nil {
		t.Fatalf("forget_memory: %v", err)
	}
	after, err := store.Recall(ctx, memory.RecallQuery{Text: "build gate"})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("forgotten memory recalled: %+v", after)
	}
}

func TestRecallMemoryToolReportsEmptyExplicitly(t *testing.T) {
	store := newTestMemoryStore(t)
	s := &Session{sessionID: "session-1", memoryStore: store}
	result, handled, err := s.executeMemoryTool(context.Background(), "recall_memory", map[string]any{
		"query": "nothing stored about this",
	})
	if err != nil || !handled {
		t.Fatalf("recall_memory handled=%v err=%v", handled, err)
	}
	if !strings.Contains(result.Content, "No workspace memories matched") {
		t.Fatalf("empty recall should be explicit, got %q", result.Content)
	}
}

func TestSearchHistoryTool(t *testing.T) {
	store := newTestMemoryStore(t)
	s := &Session{sessionID: "session-2", memoryStore: store}
	ctx := context.Background()
	if err := store.RecordTurn(ctx, memory.TurnRecord{
		SessionID: "session-1", Turn: 3,
		UserText:      "fix the scrollbar artifact",
		AssistantText: "Matched VisibleWidth to ultraviolet.",
		ToolNames:     []string{"edit_file"},
		Files:         []string{"tui/editor/render.go"},
	}); err != nil {
		t.Fatalf("RecordTurn: %v", err)
	}
	result, handled, err := s.executeMemoryTool(ctx, "search_history", map[string]any{
		"query": "scrollbar artifact",
	})
	if err != nil || !handled {
		t.Fatalf("search_history handled=%v err=%v", handled, err)
	}
	if !containsAll(result.Content, "session-1", "VisibleWidth") {
		t.Fatalf("search_history result = %q", result.Content)
	}
}

func TestMemoryDisabledSettings(t *testing.T) {
	store := newTestMemoryStore(t)
	s := &Session{sessionID: "session-1", memoryStore: store}
	s.cfg = &Config{}
	s.cfg.Memory.AutoRecall = "off"
	if _, err := store.Remember(context.Background(), memory.Record{
		Kind: "fact", Title: "Hidden", Content: "Should not inject.",
	}); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if got := s.recallMemoryContext(context.Background(), "hidden", nil); got != "" {
		t.Fatalf("recall disabled but injected: %q", got)
	}
}

func TestTurnRecordFromMessages(t *testing.T) {
	msgs := []provider.ChatMessage{
		provider.NewTextMessage("user", "fix the bug in parser"),
		{
			Role: "assistant",
			ToolCalls: []provider.ToolCall{{
				ID: "call_1",
				Function: provider.FunctionCall{
					Name:      "edit_file",
					Arguments: `{"path":"pkg/parser/parse.go","content":"..."}`,
				},
			}},
		},
		provider.NewTextMessage("tool", "ok"),
		provider.NewTextMessage("assistant", "Fixed the parser bug."),
	}
	record := turnRecordFromMessages("s1", 4, msgs, "Fixed the parser bug.")
	if record.UserText != "fix the bug in parser" {
		t.Fatalf("user text = %q", record.UserText)
	}
	if record.AssistantText != "Fixed the parser bug." {
		t.Fatalf("assistant text = %q", record.AssistantText)
	}
	if len(record.ToolNames) != 1 || record.ToolNames[0] != "edit_file" {
		t.Fatalf("tool names = %+v", record.ToolNames)
	}
	if len(record.Files) != 1 || record.Files[0] != "pkg/parser/parse.go" {
		t.Fatalf("files = %+v", record.Files)
	}
}

func containsAll(value string, wants ...string) bool {
	for _, want := range wants {
		if !strings.Contains(value, want) {
			return false
		}
	}
	return true
}

func TestMemoryExtractionRequiresGenuineHumanInput(t *testing.T) {
	runtime := provider.NewTextMessage("user", "child completed")
	runtime.Origin = provider.OriginSubAgentWake
	runtime.Visibility = provider.VisibilityContextOnly
	if containsHumanInput([]provider.ChatMessage{runtime}) {
		t.Fatal("runtime wake was attributed as human input")
	}
	human := provider.NewTextMessage("user", "continue")
	if !containsHumanInput([]provider.ChatMessage{runtime, human}) {
		t.Fatal("genuine human input was not detected")
	}
}
