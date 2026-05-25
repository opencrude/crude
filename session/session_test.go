package session

import (
	"strings"
	"testing"

	"crude/provider"
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
}

func TestTruncateMessages_NoTruncation(t *testing.T) {
	msgs := []provider.ChatMessage{
		{Role: "user", Content: provider.TextContent("hi")},
		{Role: "assistant", Content: provider.TextContent("hello")},
	}
	result := truncateMessages(msgs, 100000, "test")
	if len(result) != 2 {
		t.Errorf("expected 2, got %d", len(result))
	}
}

func TestTruncateMessages_ZeroWindow(t *testing.T) {
	msgs := []provider.ChatMessage{
		{Role: "user", Content: provider.TextContent("hi")},
		{Role: "assistant", Content: provider.TextContent("hello")},
	}
	result := truncateMessages(msgs, 0, "test")
	if len(result) != 2 {
		t.Errorf("expected 2 (zero window = no truncation), got %d", len(result))
	}
}
