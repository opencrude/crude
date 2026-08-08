package sessionsettings

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func TestScreenUsesNativeNestedRoleForm(t *testing.T) {
	base := types.Settings{ProviderID: "openrouter", Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {Provider: "openrouter", Model: "workspace-main", ThinkingEffort: "low"},
	}}}
	effective := cloneSettings(base)
	setCategory(&effective, "default", types.CategorySettings{Provider: "openai", Model: "session-main", ThinkingEffort: "high"})
	screen := New(theme.DefaultDark(), effective, base, map[string]types.CategorySettings{
		"default": {Provider: "openai", Model: "session-main", ThinkingEffort: "high"},
	})
	screen.ConfigureProviders([]types.ProviderPreset{{ID: "openrouter", Name: "OpenRouter"}, {ID: "openai", Name: "OpenAI"}}, map[string]types.ProviderConfig{
		"openrouter": {Model: "workspace-main"}, "openai": {Model: "session-main"},
	})
	deck := ui.NewPanelDeck("deck", ui.NewTokenSet(theme.DefaultDark()))
	deck.Open(ui.PanelEntry{ID: "settings", Title: "Session Settings", Content: screen, Closable: true, Policy: ui.PanelInteractive, PreferredHeight: 12})
	host := ui.NewHost(theme.DefaultDark(), deck)
	host.Resize(80, 13)
	host.Layout()
	host.Focus(FormID)

	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Action != nil || screen.page != rolePage || screen.role != "default" {
		t.Fatalf("role open result=%#v page=%d role=%q", result, screen.page, screen.role)
	}
	if screen.formModel.FieldCount() != 3 {
		t.Fatalf("role form fields=%d, want provider/model/thinking", screen.formModel.FieldCount())
	}

	buf := surface.NewCellBuffer(80, 13)
	host.Paint(buf)
	if !bufferContains(buf, "Main Agent") || !bufferContains(buf, "Thinking level") {
		t.Fatal("native role form did not paint expected content")
	}
}

func TestRoleResetRestoresInheritanceAndRemovesOverride(t *testing.T) {
	base := types.Settings{Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"crafter": {Provider: "openrouter", Model: "workspace-crafter", ThinkingEffort: "medium"},
	}}}
	effective := cloneSettings(base)
	setCategory(&effective, "crafter", types.CategorySettings{Provider: "openai", Model: "session-crafter", ThinkingEffort: "high"})
	screen := New(theme.DefaultDark(), effective, base, map[string]types.CategorySettings{
		"crafter": {Provider: "openai", Model: "session-crafter", ThinkingEffort: "high"},
	})

	screen.resetRole("crafter")
	settings := screen.ToSettings()
	if _, ok := settings.SessionOverrides["crafter"]; ok {
		t.Fatal("reset role still has an explicit session override")
	}
	if got := settings.Agents.Categories["crafter"].Model; got != "workspace-crafter" {
		t.Fatalf("model after reset=%q, want workspace-crafter", got)
	}
}

func TestModelSelectEmitsFetchAndAppliesAsyncResult(t *testing.T) {
	settings := types.Settings{ProviderID: "openrouter", Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{
		"default": {Provider: "openrouter"},
	}}}
	screen := New(theme.DefaultDark(), settings, settings, nil)
	screen.ConfigureProviders([]types.ProviderPreset{{ID: "openrouter", Name: "OpenRouter"}}, map[string]types.ProviderConfig{
		"openrouter": {ProviderURL: "https://router", APIKey: "secret"},
	})
	screen.openRole("default")
	result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: "form.select.open", Payload: fieldModel})
	if result.Cmd == nil {
		t.Fatal("empty model select did not request models")
	}
	request, ok := result.Cmd().(FetchModelsMsg)
	if !ok || request.ProviderID != "openrouter" || request.APIKey != "secret" {
		t.Fatalf("fetch request=%#v", request)
	}
	screen.ApplyModels("openrouter", []types.ModelEntry{{ID: "model-a", Name: "Model A"}})
	field := screen.formModel.Field(fieldModel)
	if field.Loading || len(field.Options) != 1 || field.Options[0].ID != "model-a" {
		t.Fatalf("model field after result=%#v", field)
	}
}

func bufferContains(buf *surface.CellBuffer, want string) bool {
	runes := []rune(want)
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x <= buf.Width()-len(runes); x++ {
			matched := true
			for i, r := range runes {
				if buf.Get(x+i, y).Char != r {
					matched = false
					break
				}
			}
			if matched {
				return true
			}
		}
	}
	return false
}
