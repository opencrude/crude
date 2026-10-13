package sessionsettings

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

// TestMemoryAgentRoleInInheritanceList verifies the /settings deck lists a
// Memory Agent row alongside the other agents and that opening it yields a
// provider/model/thinking role form.
func TestMemoryAgentRoleInInheritanceList(t *testing.T) {
	base := types.Settings{ProviderID: "openrouter", Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {Provider: "openrouter", Model: "workspace-main"},
	}}}
	screen := New(theme.DefaultDark(), cloneSettings(base), base, nil)
	deck := ui.NewPanelDeck("deck", ui.NewTokenSet(theme.DefaultDark()))
	deck.Open(ui.PanelEntry{ID: "settings", Title: "Session Settings", Content: screen, Closable: true, Policy: ui.PanelInteractive, PreferredHeight: 14})
	host := ui.NewHost(theme.DefaultDark(), deck)
	host.Resize(80, 15)
	host.Layout()
	host.Focus(FormID)

	if field := screen.formModel.Field("memory"); field == nil {
		t.Fatal("root form missing Memory Agent row")
	}
}

// TestMemoryAgentRoleOpensRoleForm verifies entering the Memory Agent role
// opens a provider/model/thinking role page (no protocol field).
func TestMemoryAgentRoleOpensRoleForm(t *testing.T) {
	base := types.Settings{ProviderID: "openrouter", Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {Provider: "openrouter", Model: "workspace-main"},
	}}}
	screen := New(theme.DefaultDark(), cloneSettings(base), base, nil)
	deck := ui.NewPanelDeck("deck", ui.NewTokenSet(theme.DefaultDark()))
	deck.Open(ui.PanelEntry{ID: "settings", Title: "Session Settings", Content: screen, Closable: true, Policy: ui.PanelInteractive, PreferredHeight: 14})
	host := ui.NewHost(theme.DefaultDark(), deck)
	host.Resize(80, 15)
	host.Layout()
	host.Focus(FormID)

	screen.openRole("memory")
	if screen.role != "memory" || screen.page != rolePage {
		t.Fatalf("memory role open: page=%d role=%q", screen.page, screen.role)
	}
	if screen.formModel.Field(fieldModel) == nil || screen.formModel.Field(fieldProvider) == nil || screen.formModel.Field(fieldThinking) == nil {
		t.Fatal("memory role form missing provider/model/thinking")
	}
	if screen.formModel.FieldCount() != 5 {
		t.Fatalf("memory role form has %d fields, want provider/model/thinking/compaction/retention (5)", screen.formModel.FieldCount())
	}
	_ = tea.KeyEnter
}
