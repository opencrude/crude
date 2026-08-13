package sessiondb

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"crude/provider"
)

func TestMessageRoundTripPreservesProtocolContinuationData(t *testing.T) {
	db, err := OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	defer db.Close()
	if err := Create(db, SessionMeta{
		ID: "session", ProjectPath: "/project", ProjectName: "project", CreatedAt: time.Now().UnixNano(),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	messages := []provider.ChatMessage{
		{
			Role:    "assistant",
			Content: provider.TextContent(""),
			ProviderItems: []provider.ProviderItem{{
				Standard: "anthropic",
				Type:     "thinking",
				Data:     json.RawMessage(`{"type":"thinking","thinking":"reason","signature":"sig"}`),
			}},
			ToolCalls: []provider.ToolCall{{
				ID: "toolu_1", Type: "function",
				Function: provider.FunctionCall{Name: "read_file", Arguments: `{"path":"a.go"}`},
			}},
		},
		{
			Role:       "tool",
			ToolCallID: "toolu_1",
			Content:    provider.TextContent("missing"),
			ToolError:  true,
		},
	}
	if _, err := ReplaceMessages(db, "session", messages, []int{1, 1}); err != nil {
		t.Fatalf("ReplaceMessages: %v", err)
	}

	loaded, _, err := LoadMessages(db)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded messages = %d", len(loaded))
	}
	if len(loaded[0].ProviderItems) != 1 ||
		string(loaded[0].ProviderItems[0].Data) != string(messages[0].ProviderItems[0].Data) {
		t.Fatalf("provider items = %+v", loaded[0].ProviderItems)
	}
	if !loaded[1].ToolError {
		t.Fatal("tool error flag was not preserved")
	}
}

func TestMessageRoundTripPreservesContentWhitespace(t *testing.T) {
	db, err := OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	defer db.Close()
	if err := Create(db, SessionMeta{
		ID: "session", ProjectPath: "/project", ProjectName: "project", CreatedAt: time.Now().UnixNano(),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	const content = "\n  indented\n"
	if _, err := ReplaceMessages(db, "session", []provider.ChatMessage{
		provider.NewTextMessage("user", content),
	}, []int{1}); err != nil {
		t.Fatalf("ReplaceMessages: %v", err)
	}
	loaded, _, err := LoadMessages(db)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if got := loaded[0].ContentString(); got != content {
		t.Fatalf("content = %q, want %q", got, content)
	}
}

func TestReplaceMessagesRejectsInvalidProviderMetadata(t *testing.T) {
	db, err := OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	defer db.Close()
	if err := Create(db, SessionMeta{
		ID: "session", ProjectPath: "/project", ProjectName: "project", CreatedAt: time.Now().UnixNano(),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = ReplaceMessages(db, "session", []provider.ChatMessage{{
		Role:    "assistant",
		Content: provider.TextContent(""),
		ProviderItems: []provider.ProviderItem{{
			Standard: "anthropic", Type: "thinking", Data: json.RawMessage(`{`),
		}},
	}}, []int{1})
	if err == nil {
		t.Fatal("ReplaceMessages accepted invalid provider metadata")
	}
}

func TestMessageRoundTripPreservesMultipartToolOutput(t *testing.T) {
	db, err := OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	defer db.Close()
	if err := Create(db, SessionMeta{
		ID: "session", ProjectPath: "/project", ProjectName: "project", CreatedAt: time.Now().UnixNano(),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	message := provider.ChatMessage{
		Role: "tool", ToolCallID: "call_1",
		Content: provider.PartsContent([]provider.ContentPart{
			{Type: "text", Text: "screenshot"},
			{Type: "image_url", ImageURL: &provider.ImageURL{URL: "data:image/png;base64,abc"}},
		}),
	}
	if _, err := ReplaceMessages(db, "session", []provider.ChatMessage{message}, []int{1}); err != nil {
		t.Fatalf("ReplaceMessages: %v", err)
	}
	loaded, _, err := LoadMessages(db)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(loaded) != 1 || !loaded[0].Content.HasImage() ||
		loaded[0].ContentString() != "screenshot" {
		t.Fatalf("loaded multipart tool output = %+v", loaded)
	}
}

func TestReplaceMessagesPersistsTokenEstimates(t *testing.T) {
	db, err := OpenAndMigrate(filepath.Join(t.TempDir(), "tokens.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	defer db.Close()
	if err := Create(db, SessionMeta{
		ID: "session", ProjectPath: "/project", ProjectName: "project",
		CreatedAt: time.Now().UnixNano(),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	message := provider.NewTextMessage("user", "estimate these persisted tokens")
	if _, err := ReplaceMessages(db, "session", []provider.ChatMessage{message}, []int{1}); err != nil {
		t.Fatalf("ReplaceMessages: %v", err)
	}
	var tokenCount int
	if err := db.QueryRow(
		`SELECT token_count FROM messages ORDER BY sequence_num LIMIT 1`,
	).Scan(&tokenCount); err != nil {
		t.Fatalf("read token_count: %v", err)
	}
	if tokenCount <= 0 {
		t.Fatalf("token_count = %d", tokenCount)
	}
}
