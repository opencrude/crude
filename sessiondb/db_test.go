package sessiondb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"crude/provider"
	ttypes "crude/types"
)

// DB wraps an open session DB for test fixtures (shared across test files).
type DB struct {
	db   *sql.DB
	path string
}

func TestSubAgentResultChildIDIsExternalTranscriptLocator(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	t.Cleanup(func() { _ = db.Close() })
	if err := CreateSessionRow(db, "parent"); err != nil {
		t.Fatal(err)
	}
	if err := InsertSubAgentResult(db, SubAgentResultRow{
		ParentSessionID: "parent", ChildSessionID: "child-with-separate-db",
		AgentID: "child", AgentName: "Child", AgentCategory: "researcher",
		Status: "completed", CreatedAt: time.Now().UnixNano(),
	}); err != nil {
		t.Fatalf("separate child transcript still required a parent-DB session row: %v", err)
	}
	rows, err := db.Query(`PRAGMA foreign_key_list(sub_agent_results)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, seq int
		var table, from, to, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			t.Fatal(err)
		}
		if from == "child_session_id" {
			t.Fatal("child_session_id still has an invalid same-database foreign key")
		}
	}
}

func TestMigrateRemovesEmbeddedChildStubButKeepsStandaloneChildIdentity(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	if err := CreateSessionRow(db, "parent"); err != nil {
		t.Fatal(err)
	}
	if err := Create(db, SessionMeta{
		ID: "embedded-child", ProjectPath: "/", ProjectName: "test", CreatedAt: time.Now().UnixNano(),
		ParentSessionID: sql.NullString{String: "parent", Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var embedded int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = 'embedded-child'`).Scan(&embedded); err != nil {
		t.Fatal(err)
	}
	if embedded != 0 {
		t.Fatal("legacy child stub survived in parent DB")
	}
	_ = db.Close()

	childDB := OpenAndMigrateAt(t.TempDir())
	if err := Create(childDB, SessionMeta{
		ID: "standalone-child", ProjectPath: "/", ProjectName: "test", CreatedAt: time.Now().UnixNano(),
		ParentSessionID: sql.NullString{String: "parent", Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(childDB); err != nil {
		t.Fatal(err)
	}
	var standalone int
	if err := childDB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = 'standalone-child'`).Scan(&standalone); err != nil {
		t.Fatal(err)
	}
	if standalone != 1 {
		t.Fatal("standalone child transcript lost its canonical identity")
	}
	_ = childDB.Close()
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
		{Role: "user", Content: provider.TextContent("hello")},
		{Role: "assistant", Content: provider.TextContent("hi there"), ReasoningContent: "thinking...", Reasoning: "reasoning...", CacheControl: &cc, ToolCalls: []provider.ToolCall{
			{Index: 0, ID: "tc1", Type: "function", Function: provider.FunctionCall{Name: "read", Arguments: `{"path":"/foo"}`}},
		}},
		{Role: "tool", Content: provider.TextContent("file contents"), ToolCallID: "tc1"},
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

	if loaded[0].Role != "user" || loaded[0].ContentString() != "hello" {
		t.Errorf("msg[0]: expected user/hello, got %s/%s", loaded[0].Role, loaded[0].ContentString())
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
	if loaded[2].Role != "tool" || loaded[2].ContentString() != "file contents" {
		t.Errorf("msg[2]: expected tool/file contents, got %s/%s", loaded[2].Role, loaded[2].ContentString())
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
		{Role: "user", Content: provider.TextContent("first")},
		{Role: "assistant", Content: provider.TextContent("reply1")},
	}
	turns1 := []int{1, 1}
	_, _ = ReplaceMessages(db, "test", msgs1, turns1)

	msgs2 := []provider.ChatMessage{
		{Role: "user", Content: provider.TextContent("second")},
		{Role: "assistant", Content: provider.TextContent("reply2")},
	}
	turns2 := []int{2, 2}
	_, _ = ReplaceMessages(db, "test", msgs2, turns2)

	loaded, loadedTurns, err := LoadMessages(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 messages after overwrite, got %d", len(loaded))
	}
	if loaded[0].ContentString() != "second" {
		t.Errorf("msg[0] Content: expected 'second', got %q", loaded[0].ContentString())
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
		{Role: "user", Content: provider.TextContent("turn1")},
		{Role: "assistant", Content: provider.TextContent("reply1")},
		{Role: "user", Content: provider.TextContent("turn2")},
		{Role: "assistant", Content: provider.TextContent("reply2")},
	}
	turns := []int{1, 1, 2, 2}

	_, _ = ReplaceMessages(db, "test", msgs, turns)
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

	_ = Create(db, SessionMeta{ID: "t", ProjectPath: "/", ProjectName: "p", CreatedAt: 1})

	_ = UpdateFirstPrompt(db, "t", "first prompt text that is very long and should be truncated at 200 characters but this one is short enough")
	_ = UpdateLastPrompt(db, "t", "last prompt")

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

	_ = UpdateFirstPrompt(db, "t", "should not overwrite")
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
		_ = Create(db, SessionMeta{
			ID: name, ProjectPath: "/", ProjectName: name,
			Model: "m", Provider: "p", CreatedAt: int64(i + 1),
		})
		_, _ = ReplaceMessages(db, name, []provider.ChatMessage{
			{Role: "user", Content: provider.TextContent("hello")},
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
		_ = Create(db, SessionMeta{
			ID: name, ProjectPath: "/", ProjectName: name,
			Model: "m", Provider: "p", CreatedAt: int64(i + 1),
		})
		_, _ = ReplaceMessages(db, name, []provider.ChatMessage{
			{Role: "user", Content: provider.TextContent("hello")},
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
	_ = Create(db, SessionMeta{ID: "delme", ProjectPath: "/", ProjectName: "p", CreatedAt: 1})
	_ = db.Close()

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

func TestReplaceMessagesMultipartRoundtrip(t *testing.T) {
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

	// Store a text message and a multipart message
	msgs := []provider.ChatMessage{
		{Role: "user", Content: provider.TextContent("plain text message")},
		{Role: "user", Content: provider.PartsContent([]provider.ContentPart{
			{Type: "text", Text: "look at this"},
			{Type: "image_url", ImageURL: &provider.ImageURL{URL: "data:image/png;base64,abc123"}},
		})},
	}
	turns := []int{1, 1}

	n, err := ReplaceMessages(db, "test", msgs, turns)
	if err != nil {
		t.Fatalf("ReplaceMessages: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 messages, got %d", n)
	}

	loaded, loadedTurns, err := LoadMessages(db)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 loaded messages, got %d", len(loaded))
	}

	// First message should be text
	if !loaded[0].Content.IsText() {
		t.Error("first message should be text content")
	}
	if loaded[0].ContentString() != "plain text message" {
		t.Errorf("msg[0]: got %q, want 'plain text message'", loaded[0].ContentString())
	}

	// Second message should be multipart
	if loaded[1].Content.IsText() {
		t.Error("second message should be multipart content")
	}
	if !loaded[1].Content.HasImage() {
		t.Error("second message should have image")
	}
	parts := loaded[1].Content.Parts()
	if len(parts) != 2 {
		t.Fatalf("second message parts: expected 2, got %d", len(parts))
	}
	if parts[0].Type != "text" || parts[0].Text != "look at this" {
		t.Errorf("parts[0]: got %+v", parts[0])
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL == nil {
		t.Errorf("parts[1]: got %+v", parts[1])
	}

	// Verify content_type column in DB directly
	rows, err := db.Query("SELECT content_type FROM messages ORDER BY sequence_num")
	if err != nil {
		t.Fatalf("query content_type: %v", err)
	}
	defer rows.Close()
	var contentTypes []string
	for rows.Next() {
		var ct string
		if err := rows.Scan(&ct); err != nil {
			t.Fatalf("scan content_type: %v", err)
		}
		contentTypes = append(contentTypes, ct)
	}
	if len(contentTypes) != 2 {
		t.Fatalf("expected 2 content_type rows, got %d", len(contentTypes))
	}
	if contentTypes[0] != "text" {
		t.Errorf("content_type[0]: got %q, want 'text'", contentTypes[0])
	}
	if contentTypes[1] != "multipart" {
		t.Errorf("content_type[1]: got %q, want 'multipart'", contentTypes[1])
	}

	_ = loadedTurns
}

func TestParseMessageContent_NullContentType(t *testing.T) {
	// Old DB rows may have NULL content_type — should default to text behavior
	got := parseMessageContent("hello world", "")
	if !got.IsText() {
		t.Error("empty content_type should default to text")
	}
	if got.String() != "hello world" {
		t.Errorf("got %q, want 'hello world'", got.String())
	}

	// Even content that looks like JSON array should be text when content_type is not "multipart"
	got = parseMessageContent("[{\"type\":\"text\",\"text\":\"hello\"}]", "")
	if !got.IsText() {
		t.Error("content_type='text' should treat even array-looking content as text")
	}
}

func TestReplaceMessagesMultipartWithMeta(t *testing.T) {
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

	msgs := []provider.ChatMessage{
		{Role: "user", Content: provider.PartsContent([]provider.ContentPart{
			{Type: "text", Text: "multi-part msg"},
			{Type: "image_url", ImageURL: &provider.ImageURL{URL: "data:image/jpeg;base64,xyz"}},
		})},
	}
	turns := []int{1}
	metas := []DisplayMeta{
		{
			DisplayRole:     "user",
			DisplayContent:  "[Pasted IMG1] multi-part msg",
			ReferenceLabels: []string{"[Pasted IMG1]"},
		},
	}

	_, err = ReplaceMessagesWithMeta(db, "test", msgs, turns, metas)
	if err != nil {
		t.Fatalf("ReplaceMessagesWithMeta: %v", err)
	}

	loaded, err := LoadMessagesWithMeta(db)
	if err != nil {
		t.Fatalf("LoadMessagesWithMeta: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 message, got %d", len(loaded))
	}
	if loaded[0].Msg.Content.IsText() {
		t.Error("should be multipart")
	}
	if !loaded[0].Msg.Content.HasImage() {
		t.Error("should have image")
	}
	if loaded[0].Meta.DisplayContent != metas[0].DisplayContent ||
		len(loaded[0].Meta.ReferenceLabels) != 1 ||
		loaded[0].Meta.ReferenceLabels[0] != "[Pasted IMG1]" {
		t.Fatalf("display metadata did not round-trip: %+v", loaded[0].Meta)
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

func TestGetCumulativeCostEmpty(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, _ := OpenAndMigrate(dbPath)
	defer db.Close()

	got, err := GetCumulativeCost(db)
	if err != nil {
		t.Fatalf("GetCumulativeCost on empty DB: %v", err)
	}
	if got != 0.0 {
		t.Errorf("GetCumulativeCost on empty DB: expected 0.0, got %v", got)
	}
}

func TestGetCumulativeCostReturnsLatestSnapshot(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, _ := OpenAndMigrate(dbPath)
	defer db.Close()

	// Simulate three API calls with cumulative cost growing.
	if err := InsertCostSnapshot(db, 10, 5, 0, 0.01, nil); err != nil {
		t.Fatalf("InsertCostSnapshot #1: %v", err)
	}
	if err := InsertCostSnapshot(db, 20, 10, 0, 0.025, nil); err != nil {
		t.Fatalf("InsertCostSnapshot #2: %v", err)
	}
	if err := InsertCostSnapshot(db, 30, 15, 0, 0.042, nil); err != nil {
		t.Fatalf("InsertCostSnapshot #3: %v", err)
	}

	got, err := GetCumulativeCost(db)
	if err != nil {
		t.Fatalf("GetCumulativeCost: %v", err)
	}
	// The latest snapshot's total_cost is the cumulative cost.
	const want = 0.042
	if got != want {
		t.Errorf("GetCumulativeCost: expected %v, got %v", want, got)
	}
}

func TestCanonicalConversationSnapshotRoundTripsEveryField(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	t.Cleanup(func() { _ = db.Close() })
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Round(0)
	want := ttypes.ConversationMessage{
		InternalID: "message-1", Role: "tool", Content: "result", ReferenceLabels: []string{"@one"}, Time: now,
		ToolName: "run_sub_agent", ToolArgs: "description: inspect", ToolCommand: "go test ./...",
		ToolInput: "needle", ToolScope: "workspace", ToolID: "call-1", ToolStatus: "success", ToolIndex: 4,
		ToolProgressPhase: "done", ToolProgressMessage: "complete", ToolProgressLabel: "child",
		ToolPreviewPhase: "done", ToolEstTokens: 42, ToolLinesAdded: 3, ToolLinesRemoved: 2,
		ToolStartTime: now.Add(-time.Second), ToolDuration: time.Second,
		ThinkingInterrupted: true, ThinkingDuration: 2 * time.Second,
		LifecyclePhase: "done", LifecycleProgress: 100, LifecycleProvider: "ollama",
		LifecycleUploadedBytes: 7, LifecycleTotalBytes: 9,
		Incomplete: true, IncompleteReason: "partial", SubPaneID: "child-1", PermTag: "always",
	}
	message := provider.NewTextMessage("tool", "result")
	message.ToolCallID = "call-1"
	meta := DisplayMeta{SnapshotVersion: ConversationSnapshotVersion, Snapshots: []ttypes.ConversationMessage{want}}
	if _, err := ReplaceMessagesWithMeta(db, "session", []provider.ChatMessage{message}, []int{1}, []DisplayMeta{meta}); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadMessagesWithMeta(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || len(loaded[0].Meta.Snapshots) != 1 || !reflect.DeepEqual(loaded[0].Meta.Snapshots[0], want) {
		t.Fatalf("canonical snapshot round-trip\n got: %#v\nwant: %#v", loaded, want)
	}
}

func TestContextStateRoundTripsIndependentlyFromDisplayHistory(t *testing.T) {
	db := OpenAndMigrateAt(t.TempDir())
	t.Cleanup(func() { _ = db.Close() })
	if err := CreateSessionRow(db, "session"); err != nil {
		t.Fatal(err)
	}
	want := ContextState{Generation: 4, Reason: "automatic_compaction", Messages: []provider.ChatMessage{
		provider.NewTextMessage("system", "<context_checkpoint>checkpoint</context_checkpoint>"),
		provider.NewTextMessage("user", "latest request"),
	}}
	if err := SaveContextState(db, "session", want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := LoadContextState(db, "session")
	if err != nil || !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("context state round-trip got=%#v ok=%v err=%v want=%#v", got, ok, err, want)
	}
}
