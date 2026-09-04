package chatprojection

import (
	"strings"
	"testing"
	"time"

	"crude/tui/theme"
	"crude/tui/types"
)

func TestAssistantHeadersUseStoredTurnModelAndLegacyFallbackOnly(t *testing.T) {
	renderer := NewRenderer(theme.DefaultDark())
	renderer.SetSize(100, 30)
	renderer.SetChatAreaBounds(0, 25)
	renderer.SetLegacyModelFallback("legacy-before")
	renderer.SyncMessages([]types.ChatMessage{
		{InternalID: "u1", Role: types.RoleUser, Content: "first", Time: time.Unix(1, 0)},
		{InternalID: "a1", Role: types.RoleAssistant, Content: "answer one", Time: time.Unix(2, 0), TurnGeneration: 1, ProviderID: "provider-a", ModelID: "model-a"},
		{InternalID: "u2", Role: types.RoleUser, Content: "second", Time: time.Unix(3, 0)},
		{InternalID: "a2", Role: types.RoleAssistant, Content: "answer two", Time: time.Unix(4, 0), TurnGeneration: 2, ProviderID: "provider-b", ModelID: "model-b"},
		{InternalID: "u3", Role: types.RoleUser, Content: "legacy", Time: time.Unix(5, 0)},
		{InternalID: "legacy", Role: types.RoleAssistant, Content: "old answer", Time: time.Unix(6, 0)},
	}, 1)

	renderer.SetLegacyModelFallback("legacy-after")
	_ = renderer.Render(100)
	assertHeaderModel(t, renderer, 1, "model-a", "legacy-after", "model-b")
	assertHeaderModel(t, renderer, 3, "model-b", "legacy-after", "model-a")
	assertHeaderModel(t, renderer, 5, "legacy-after", "model-a", "model-b")
}

func TestAssistantHeaderIsShownAfterThinking(t *testing.T) {
	renderer := NewRenderer(theme.DefaultDark())
	renderer.SetSize(100, 30)
	renderer.SetChatAreaBounds(0, 25)
	renderer.SyncMessages([]types.ChatMessage{
		{InternalID: "user", Role: types.RoleUser, Content: "so you say we group them?"},
		{InternalID: "thinking", Role: types.RoleThinking, Content: "considering", TurnGeneration: 4, ProviderID: "ollama-cloud", ModelID: "kimi-k3"},
		{InternalID: "assistant", Role: types.RoleAssistant, Content: "Yes, but with a specific shape.", TurnGeneration: 4, ProviderID: "ollama-cloud", ModelID: "kimi-k3"},
	}, 1)

	_ = renderer.Render(100)
	assertHeaderModel(t, renderer, 2, "kimi-k3")
}

func TestAssistantContinuationAfterToolSuppressesDuplicateHeaderWithinTurn(t *testing.T) {
	renderer := NewRenderer(theme.DefaultDark())
	renderer.SetSize(100, 30)
	renderer.SetChatAreaBounds(0, 25)
	renderer.SyncMessages([]types.ChatMessage{
		{InternalID: "assistant-1", Role: types.RoleAssistant, Content: "I will inspect it.", TurnGeneration: 4, ModelID: "kimi-k3"},
		{InternalID: "tool", Role: types.RoleTool, ToolName: "read_file", ToolStatus: "success", TurnGeneration: 4, ModelID: "kimi-k3"},
		{InternalID: "assistant-2", Role: types.RoleAssistant, Content: "The result is correct.", TurnGeneration: 4, ModelID: "kimi-k3"},
	}, 1)

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
