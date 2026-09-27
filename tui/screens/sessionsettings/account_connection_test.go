package sessionsettings

import (
	"strings"
	"testing"

	"crude/provider"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func TestRoleAccountPickerListsOnlyAccountsAndPinsSelection(t *testing.T) {
	st := types.Settings{ProviderID: "openrouter", AccountID: "home", Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{"default": {Provider: "openrouter", AccountID: "home", Model: "home-model"}}}}
	screen := New(theme.DefaultDark(), st, st, nil)
	screen.ConfigureProviders([]types.ProviderPreset{{ID: "openrouter", Name: "OpenRouter"}, {ID: "openai", Name: "OpenAI"}}, map[string]types.ProviderConfig{"openrouter": provider.DefaultTemplates()["openrouter"], "openai": provider.DefaultTemplates()["openai"]})
	screen.ConfigureAccounts(map[string]provider.Account{"home": {ID: "home", ProviderID: "openrouter", Label: "Home", APIKey: "h"}, "work": {ID: "work", ProviderID: "openrouter", Label: "Work", APIKey: "w"}, "other": {ID: "other", ProviderID: "openai", Label: "Team", APIKey: "o"}})
	screen.openRole("default")
	screen.openProviderPicker()
	buf := surface.NewCellBuffer(95, 22)
	screen.Layout(ui.LayoutContext{}, ui.Rect{W: 95, H: 22})
	screen.Paint(buf, 0, 0, ui.PaintContext{})
	out := bufferString(buf)
	for _, want := range []string{"Account", "Home", "Work", "Team"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in picker:\n%s", want, out)
		}
	}
	if strings.Contains(out, "OpenRouter") || strings.Contains(out, "OpenAI") {
		t.Fatal("session picker must not display template headings")
	}
	if strings.Contains(out, "h-key") || strings.Contains(out, "o-key") {
		t.Fatal("picker painted credentials")
	}
	for i, item := range screen.modelList.Items() {
		if item.ID == "work" {
			screen.modelList.SelectIndex(i)
			break
		}
	}
	screen.commitProviderSelection()
	screen.openRole("default")
	screen.applyRoleForm()
	got := screen.ToSettings()
	role := got.SessionOverrides["default"]
	if role.AccountID != "work" || got.AccountID != "work" || got.ProviderAPIKey != "" || got.APIKey != "" {
		t.Fatalf("selection not atomic or leaked credentials: %+v", got)
	}
	if st.Agents.Categories["default"].AccountID != "home" {
		t.Fatal("picker modified baseline")
	}
}

func TestRoleModelsIgnoreStaleAccountAndMissingAccountFailsClosed(t *testing.T) {
	st := types.Settings{ProviderID: "openrouter", AccountID: "a", Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{"default": {Provider: "openrouter", AccountID: "a"}}}}
	screen := New(theme.DefaultDark(), st, st, nil)
	screen.ConfigureProviders([]types.ProviderPreset{{ID: "openrouter", Name: "OpenRouter"}}, map[string]types.ProviderConfig{"openrouter": {}})
	screen.ConfigureAccounts(map[string]provider.Account{"a": {ID: "a", ProviderID: "openrouter", Label: "A"}, "b": {ID: "b", ProviderID: "openrouter", Label: "B"}})
	cmd := screen.OpenModelPicker("default")
	request := cmd().(FetchModelsMsg)
	screen.applyProviderToEffective("b")
	screen.openRole("default")
	screen.openModelPicker()
	screen.ApplyModelsForAccount("openrouter", request.AccountID, request.Generation, []types.ModelEntry{{ID: "old"}})
	if len(screen.models[sessionModelKey("openrouter", "b")]) != 0 {
		t.Fatal("models from A entered B")
	}
	screen.ConfigureAccounts(map[string]provider.Account{"a": {ID: "a", ProviderID: "openrouter", Label: "A"}})
	screen.openRole("default")
	if got := screen.formModel.Field(fieldProvider).Description; !strings.Contains(got, "Missing account: b") {
		t.Fatalf("missing explicit account redirected: %q", got)
	}
	if cmd := screen.openModelPicker(); cmd != nil {
		t.Fatal("missing account fetched models using another account")
	}
}
