package settings

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/types"
)

func TestAppSettingsPreserveProvidersAndAgentDefaults(t *testing.T) {
	th := theme.DefaultDark()
	current := types.AppSettings{
		ActiveProvider: "openrouter",
		Providers: map[string]types.ProviderConfig{
			"openrouter": {APIKey: "key-a", Model: "model-a", ThinkingEffort: "low"},
			"openai":     {APIKey: "key-b", Model: "model-b", ThinkingEffort: "high"},
		},
		Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
			"default": {Provider: "openrouter", Model: "main-model", ThinkingEffort: "medium"},
			"crafter": {Provider: "openai", Model: "craft-model", ThinkingEffort: "high"},
		}},
	}
	m := New(th, current, []types.ProviderPreset{
		{ID: "openrouter", Name: "OpenRouter"},
		{ID: "openai", Name: "OpenAI"},
	})

	got := m.AppSettings()
	if got.Providers["openrouter"].Model != "model-a" || got.Providers["openai"].Model != "model-b" {
		t.Fatalf("provider defaults were lost: %#v", got.Providers)
	}
	if got.Agents.Categories["default"].Model != "main-model" || got.Agents.Categories["crafter"].Model != "craft-model" {
		t.Fatalf("agent defaults were lost: %#v", got.Agents.Categories)
	}
}

func TestSettingsViewAndMouseNavigation(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{}, nil)
	lines := m.View(90, 28)
	if len(lines) != 28 {
		t.Fatalf("line count = %d, want 28", len(lines))
	}
	for i, line := range lines {
		if width := rendering.VisibleWidth(line); width != 90 {
			t.Fatalf("line %d width = %d, want 90", i, width)
		}
	}

	updated, _ := m.handleMouseClick(tea.MouseClickMsg{X: 2, Y: 2, Button: tea.MouseLeft})
	if updated.selected != 1 || updated.pane != 0 {
		t.Fatalf("category click selected=%d pane=%d, want 1/0", updated.selected, updated.pane)
	}
	updated.View(90, 28)
	x := updated.lastLayout.formX + 2
	updated, _ = updated.handleMouseClick(tea.MouseClickMsg{X: x, Y: 3, Button: tea.MouseLeft})
	if updated.pane != 1 || !updated.currentForm().IsExpanded() {
		t.Fatal("provider field click should focus form pane and expand selector")
	}
}
