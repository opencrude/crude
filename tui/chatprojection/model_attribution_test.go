package chatprojection

import (
	"strings"
	"testing"

	"crude/tui/theme"
	"crude/types/conversation"
)

func TestAssistantHeaderIsShownAfterThinking(t *testing.T) {
	renderer := NewRenderer(theme.DefaultDark())
	renderer.SetSize(100, 30)
	renderer.SetChatAreaBounds(0, 25)
	renderer.SyncMessages([]conversation.ChatMessage{
		{InternalID: "user", Role: conversation.RoleUser, Content: "so you say we group them?"},
		{InternalID: "thinking", Role: conversation.RoleThinking, Content: "considering", TurnGeneration: 4, ProviderID: "ollama-cloud", ModelID: "kimi-k3"},
		{InternalID: "assistant", Role: conversation.RoleAssistant, Content: "Yes, but with a specific shape.", TurnGeneration: 4, ProviderID: "ollama-cloud", ModelID: "kimi-k3"},
	}, 1, 0)

	_ = renderer.Render(100)
	assertHeaderModel(t, renderer, 2, "kimi-k3")
}

func TestAssistantContinuationAfterToolSuppressesDuplicateHeaderWithinTurn(t *testing.T) {
	renderer := NewRenderer(theme.DefaultDark())
	renderer.SetSize(100, 30)
	renderer.SetChatAreaBounds(0, 25)
	renderer.SyncMessages([]conversation.ChatMessage{
		{InternalID: "assistant-1", Role: conversation.RoleAssistant, Content: "I will inspect it.", TurnGeneration: 4, ModelID: "kimi-k3"},
		{InternalID: "tool", Role: conversation.RoleTool, ToolName: "read_file", ToolStatus: "success", TurnGeneration: 4, ModelID: "kimi-k3"},
		{InternalID: "assistant-2", Role: conversation.RoleAssistant, Content: "The result is correct.", TurnGeneration: 4, ModelID: "kimi-k3"},
	}, 1, 0)

	_ = renderer.Render(100)
	item := renderer.vl.Item(2)
	if item == nil {
		t.Fatal("assistant continuation was not rendered")
	}
	if strings.Contains(strings.Join(item.Rendered, "\n"), "Crude kimi-k3") {
		t.Fatalf("assistant continuation repeated its turn header: %q", item.Rendered)
	}
}

func assertHeaderModel(t *testing.T, renderer *Renderer, index int, want string, forbidden ...string) {
	t.Helper()
	item := renderer.vl.Item(index)
	if item == nil {
		t.Fatalf("message %d was not rendered", index)
	}
	text := strings.Join(item.Rendered, "\n")
	if !strings.Contains(text, "Crude "+want) {
		t.Fatalf("message %d header = %q, want model %q", index, text, want)
	}
	for _, value := range forbidden {
		if strings.Contains(text, "Crude "+value) {
			t.Fatalf("message %d header was relabeled: %q", index, text)
		}
	}
}
