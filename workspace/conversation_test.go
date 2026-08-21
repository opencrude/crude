package workspace

import (
	"reflect"
	"testing"
	"time"
)

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

	agent.HandleEvent(AgentEvent{Type: "thinking", AgentID: "agent", Data: "considering"}, nil, ignoreStatus, ignoreThinking, func() {}, ignoreConfirm, func(string) {}, func() {})
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
	if message.ToolStatus != "success" || message.Content != "contents" || message.PermTag != "allowed" {
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

func TestConversationFinishCancelledMarksIncomplete(t *testing.T) {
	conversation := NewConversation()
	conversation.AppendMessage(Message{Role: MessageRoleAssistant, Content: "partial"})
	conversation.Finish("", "cancelled", true)

	message := conversation.Messages()[0]
	if !message.Incomplete || message.IncompleteReason != "operation cancelled by user" {
		t.Fatalf("unexpected cancelled message: %#v", message)
	}
}

func TestConversationRemoveActiveThinking(t *testing.T) {
	conversation := NewConversation()
	conversation.AppendThinking("private reasoning")
	content := conversation.RemoveActiveThinking()
	if content != "private reasoning" || len(conversation.Messages()) != 0 {
		t.Fatalf("RemoveActiveThinking() = %q, messages=%#v", content, conversation.Messages())
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

func ignoreStatus(string) {}

func ignoreThinking(bool, bool, bool) {}

func ignoreConfirm(string, string, string, string, string, string, string) {}
