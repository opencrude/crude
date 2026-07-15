package widgets

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
	types "crude/tui/types"
)

func TestSessionSettingsAreRoleScopedAndThreeFieldsOnly(t *testing.T) {
	base := types.Settings{
		ProviderID: "openrouter",
		Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
			"default": {Provider: "openrouter", Model: "workspace-main", ThinkingEffort: "low"},
		}},
	}
	effective := cloneSettings(base)
	setCategory(&effective, "default", types.CategorySettings{Provider: "openai", Model: "session-main", ThinkingEffort: "high"})
	m := NewSessionSettingsWithInheritance(theme.DefaultDark(), effective, base, map[string]types.CategorySettings{
		"default": {Provider: "openai", Model: "session-main", ThinkingEffort: "high"},
	})
	m.ConfigureProviders([]types.ProviderPreset{
		{ID: "openrouter", Name: "OpenRouter"},
		{ID: "openai", Name: "OpenAI"},
	}, map[string]types.ProviderConfig{
		"openrouter": {Model: "workspace-main"},
		"openai":     {Model: "session-main"},
	})

	updated, _ := m.Update(tea.MouseClickMsg{X: 3, Y: 3, Button: tea.MouseLeft})
	if updated.page != sessionSettingsRole || updated.role != "default" {
		t.Fatalf("role click opened page=%d role=%q", updated.page, updated.role)
	}
	if updated.roleForm.ItemCount() != 3 {
		t.Fatalf("inline role form has %d fields, want provider/model/thinking only", updated.roleForm.ItemCount())
	}
}

func TestSessionRoleResetRestoresInheritanceAndRemovesOverride(t *testing.T) {
	base := types.Settings{Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"crafter": {Provider: "openrouter", Model: "workspace-crafter", ThinkingEffort: "medium"},
	}}}
	effective := cloneSettings(base)
	setCategory(&effective, "crafter", types.CategorySettings{Provider: "openai", Model: "session-crafter", ThinkingEffort: "high"})
	m := NewSessionSettingsWithInheritance(theme.DefaultDark(), effective, base, map[string]types.CategorySettings{
		"crafter": {Provider: "openai", Model: "session-crafter", ThinkingEffort: "high"},
	})

	m.resetRole("crafter")
	settings := m.ToSettings()
	if _, ok := settings.SessionOverrides["crafter"]; ok {
		t.Fatal("reset role still has an explicit session override")
	}
	if got := settings.Agents.Categories["crafter"].Model; got != "workspace-crafter" {
		t.Fatalf("model after reset = %q, want workspace-crafter", got)
	}
}
