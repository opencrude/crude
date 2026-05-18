package sessiondb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"crude/provider"
)

func openTestDB(t *testing.T) (*DB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	return &DB{db: db, path: dbPath}, dir
}

type DB struct {
	db   *sql.DB
	path string
}

func (d *DB) Close() error {
	return d.db.Close()
}

func TestOpenAndMigrate(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	defer db.Close()

	var count int
	err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table'").Scan(&count)
	if err != nil {
		t.Fatalf("query master: %v", err)
	}
	if count < 3 {
		t.Errorf("expected >=3 tables, got %d", count)
	}
}

func TestOpenAndMigrateReopen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db1, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	db1.Close()

	db2, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	db2.Close()
}

func TestReplaceMessagesEmpty(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_ = Create(db, SessionMeta{
		ID:          "test",
		ProjectPath: "/tmp",
		ProjectName: "test",
		Model:       "test-model",
		Provider:    "test",
		CreatedAt:   1,
	})

	n, err := ReplaceMessages(db, "test", nil, nil)
	if err != nil {
		t.Fatalf("ReplaceMessages empty: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0, got %d", n)
	}
}

func TestReplaceMessagesRoundtrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_ = Create(db, SessionMeta{
		ID:          "test",
		ProjectPath: "/tmp",
		ProjectName: "test",
		Model:       "test-model",
		Provider:    "test",
		CreatedAt:   1,
	})

	cc := provider.CacheControl{Type: "ephemeral"}
	msgs := []provider.ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there", ReasoningContent: "thinking...", Reasoning: "reasoning...", CacheControl: &cc, ToolCalls: []provider.ToolCall{
			{Index: 0, ID: "tc1", Type: "function", Function: provider.FunctionCall{Name: "read", Arguments: `{"path":"/foo"}`}},
		}},
		{Role: "tool", Content: "file contents", ToolCallID: "tc1"},
	}
	turns := []int{1, 1, 1}

	n, err := ReplaceMessages(db, "test", msgs, turns)
	if err != nil {
		t.Fatalf("ReplaceMessages: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3, got %d", n)
	}

	loaded, loadedTurns, err := LoadMessages(db)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(loaded))
	}
	if len(loadedTurns) != 3 {
		t.Fatalf("expected 3 turn numbers, got %d", len(loadedTurns))
	}

	if loaded[0].Role != "user" || loaded[0].Content != "hello" {
		t.Errorf("msg[0]: expected user/hello, got %s/%s", loaded[0].Role, loaded[0].Content)
	}
	if loaded[1].ReasoningContent != "thinking..." {
		t.Errorf("msg[1] ReasoningContent: expected 'thinking...', got %q", loaded[1].ReasoningContent)
	}
	if loaded[1].Reasoning != "reasoning..." {
		t.Errorf("msg[1] Reasoning: expected 'reasoning...', got %q", loaded[1].Reasoning)
	}
	if loaded[1].CacheControl == nil || loaded[1].CacheControl.Type != "ephemeral" {
		t.Errorf("msg[1] CacheControl: expected ephemeral, got %v", loaded[1].CacheControl)
	}
	if len(loaded[1].ToolCalls) != 1 || loaded[1].ToolCalls[0].ID != "tc1" {
		t.Errorf("msg[1] ToolCalls: expected [tc1], got %v", loaded[1].ToolCalls)
	}
	if loaded[2].Role != "tool" || loaded[2].Content != "file contents" {
		t.Errorf("msg[2]: expected tool/file contents, got %s/%s", loaded[2].Role, loaded[2].Content)
	}
	if loaded[2].ToolCallID != "tc1" {
		t.Errorf("msg[2] ToolCallID: expected tc1, got %s", loaded[2].ToolCallID)
	}
	if loadedTurns[0] != 1 || loadedTurns[1] != 1 || loadedTurns[2] != 1 {
		t.Errorf("turn numbers: expected [1,1,1], got %v", loadedTurns)
	}
}

func TestReplaceMessagesOverwrite(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_ = Create(db, SessionMeta{
		ID:          "test",
		ProjectPath: "/tmp",
		ProjectName: "test",
		Model:       "test-model",
		Provider:    "test",
		CreatedAt:   1,
	})

	msgs1 := []provider.ChatMessage{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "reply1"},
	}
	turns1 := []int{1, 1}
	ReplaceMessages(db, "test", msgs1, turns1)

	msgs2 := []provider.ChatMessage{
		{Role: "user", Content: "second"},
		{Role: "assistant", Content: "reply2"},
	}
	turns2 := []int{2, 2}
	ReplaceMessages(db, "test", msgs2, turns2)

	loaded, loadedTurns, err := LoadMessages(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 messages after overwrite, got %d", len(loaded))
	}
	if loaded[0].Content != "second" {
		t.Errorf("msg[0] Content: expected 'second', got %q", loaded[0].Content)
	}
	if loadedTurns[0] != 2 || loadedTurns[1] != 2 {
		t.Errorf("turn numbers: expected [2,2], got %v", loadedTurns)
	}
}

func TestPerMessageTurnNumbers(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_ = Create(db, SessionMeta{
		ID:          "test",
		ProjectPath: "/tmp",
		ProjectName: "test",
		Model:       "m",
		Provider:    "p",
		CreatedAt:   1,
	})

	msgs := []provider.ChatMessage{
		{Role: "user", Content: "turn1"},
		{Role: "assistant", Content: "reply1"},
		{Role: "user", Content: "turn2"},
		{Role: "assistant", Content: "reply2"},
	}
	turns := []int{1, 1, 2, 2}

	ReplaceMessages(db, "test", msgs, turns)
	_, loadedTurns, err := LoadMessages(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(loadedTurns) != 4 {
		t.Fatalf("expected 4 turns, got %d", len(loadedTurns))
	}
	if loadedTurns[0] != 1 || loadedTurns[1] != 1 || loadedTurns[2] != 2 || loadedTurns[3] != 2 {
		t.Errorf("turn numbers: expected [1,1,2,2], got %v", loadedTurns)
	}
}

func TestGetMeta(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	err = Create(db, SessionMeta{
		ID:          "sid-123",
		ProjectPath: "/home/user/project",
		ProjectName: "project",
		Model:       "claude-sonnet-4",
		Provider:    "anthropic",
		CreatedAt:   1000,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	meta, err := GetMeta(db)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if meta.ID != "sid-123" {
		t.Errorf("ID: expected sid-123, got %s", meta.ID)
	}
	if meta.ProjectName != "project" {
		t.Errorf("ProjectName: expected project, got %s", meta.ProjectName)
	}
	if meta.Model != "claude-sonnet-4" {
		t.Errorf("Model: expected claude-sonnet-4, got %s", meta.Model)
	}
}

func TestUpdateFirstLastPrompt(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	Create(db, SessionMeta{ID: "t", ProjectPath: "/", ProjectName: "p", CreatedAt: 1})

	UpdateFirstPrompt(db, "t", "first prompt text that is very long and should be truncated at 200 characters but this one is short enough")
	UpdateLastPrompt(db, "t", "last prompt")

	meta, _ := GetMeta(db)
	if !meta.FirstPrompt.Valid {
		t.Fatal("first_prompt should be valid")
	}
	if !meta.LastPrompt.Valid {
		t.Fatal("last_prompt should be valid")
	}
	if meta.LastPrompt.String != "last prompt" {
		t.Errorf("last_prompt: expected 'last prompt', got %q", meta.LastPrompt.String)
	}

	UpdateFirstPrompt(db, "t", "should not overwrite")
	meta2, _ := GetMeta(db)
	if meta2.FirstPrompt.String == "should not overwrite" {
		t.Error("UpdateFirstPrompt should not overwrite existing value")
	}
}

func TestListSessions(t *testing.T) {
	root := t.TempDir()

	for i, name := range []string{"sess1", "sess2", "sess3"} {
		dbPath := filepath.Join(root, name+".db")
		db, err := OpenAndMigrate(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		Create(db, SessionMeta{
			ID: name, ProjectPath: "/", ProjectName: name,
			Model: "m", Provider: "p", CreatedAt: int64(i + 1),
		})
		ReplaceMessages(db, name, []provider.ChatMessage{
			{Role: "user", Content: "hello"},
		}, []int{1})
		db.Close()
	}

	metas, err := ListSessions(root)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(metas) != 3 {
		t.Errorf("expected 3 sessions, got %d", len(metas))
	}
	if len(metas) >= 2 && metas[0].CreatedAt < metas[1].CreatedAt {
		t.Error("sessions should be sorted newest first")
	}
}

func TestListSessionsPaginated(t *testing.T) {
	root := t.TempDir()

	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("sess%d", i+1)
		dbPath := filepath.Join(root, name+".db")
		db, err := OpenAndMigrate(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		Create(db, SessionMeta{
			ID: name, ProjectPath: "/", ProjectName: name,
			Model: "m", Provider: "p", CreatedAt: int64(i + 1),
		})
		ReplaceMessages(db, name, []provider.ChatMessage{
			{Role: "user", Content: "hello"},
		}, []int{1})
		db.Close()
	}

	metas, total, err := ListSessionsPaginated(root, ListSessionsOpts{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("ListSessionsPaginated: %v", err)
	}
	if total != 5 {
		t.Errorf("total: expected 5, got %d", total)
	}
	if len(metas) != 2 {
		t.Errorf("page size: expected 2, got %d", len(metas))
	}

	metas2, total2, err := ListSessionsPaginated(root, ListSessionsOpts{Limit: 2, Offset: 4})
	if err != nil {
		t.Fatalf("ListSessionsPaginated offset=4: %v", err)
	}
	if total2 != 5 {
		t.Errorf("total2: expected 5, got %d", total2)
	}
	if len(metas2) != 1 {
		t.Errorf("last page size: expected 1, got %d", len(metas2))
	}

	metas3, total3, err := ListSessionsPaginated(root, ListSessionsOpts{Limit: 0, Offset: 0})
	if err != nil {
		t.Fatalf("ListSessionsPaginated default limit: %v", err)
	}
	if total3 != 5 {
		t.Errorf("total3: expected 5, got %d", total3)
	}
	if len(metas3) != 5 {
		t.Errorf("default limit page size: expected 5, got %d", len(metas3))
	}

	emptyRoot := t.TempDir()
	metas4, total4, err := ListSessionsPaginated(emptyRoot, ListSessionsOpts{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListSessionsPaginated empty: %v", err)
	}
	if total4 != 0 {
		t.Errorf("empty total: expected 0, got %d", total4)
	}
	if len(metas4) != 0 {
		t.Errorf("empty metas: expected 0, got %d", len(metas4))
	}
}

func TestDeleteSession(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "delme.db")
	db, _ := OpenAndMigrate(dbPath)
	Create(db, SessionMeta{ID: "delme", ProjectPath: "/", ProjectName: "p", CreatedAt: 1})
	db.Close()

	if _, err := os.Stat(dbPath); err != nil {
		t.Fatal("db should exist before delete")
	}
	if err := DeleteSession(root, "delme"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Error("db should not exist after delete")
	}
}

func TestInsertCostSnapshot(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, _ := OpenAndMigrate(dbPath)
	defer db.Close()

	byModel := map[string]json.RawMessage{
		"claude": json.RawMessage(`{"cost":1.5}`),
	}
	err := InsertCostSnapshot(db, 100, 50, 10, 0.05, byModel)
	if err != nil {
		t.Fatalf("InsertCostSnapshot: %v", err)
	}
}
