package viewmodel

import (
	"reflect"
	"testing"

	"crude/provider"
	settingsui "crude/tui/settings"
	"crude/types"
)

// The model catalog lives in the core, so a catalog update refetches the
// draft's account models through the workspace port: no visible operation, no
// persistence, the unsaved draft kept, and capabilities refreshed from the
// enriched entries when they arrive.
func TestCatalogUpdateIsSilentAndRefreshesExistingAgentDraft(t *testing.T) {
	app := newTestApp(t)
	wsOf(app).Logger = noopLogger{}
	modelID := "catalog-ui-" + t.TempDir()
	wsOf(app).SetAppDefaults(types.AppSettings{
		Accounts: map[string]provider.Account{"work": {ID: "work", ProviderID: "openai", Label: "Work", APIKey: "test", Connection: provider.Template{ProviderURL: "https://example.test/v1", APIStandard: "openai"}}},
		Agents:   types.AgentsSettings{Categories: map[string]types.CategorySettings{"default": {AccountID: "work", Model: modelID}}},
	})
	app.openSettings(types.SettingsScopeApplication)
	fw := app.windowManager.Window("workspace-settings")
	widget := settingsWidgetForTest(t, fw)
	root := widget.Component().(*settingsui.WorkspaceSettingsSurface)
	open, ok := root.ShowMainAgent()().(settingsui.FetchModelsMsg)
	if !ok {
		t.Fatal("opening the agent editor did not prefetch its models")
	}
	_, _ = app.Update(settingsui.ModelsMsg{ProviderID: open.ProviderID, AccountID: open.AccountID, Generation: open.Generation, Models: []types.ModelEntry{{ID: modelID}}})
	model := widget.ModelForTest()
	category := model.SelectedCategory()
	model.SetValueForTest(category, "agent-instructions", "Keep this unsaved draft")
	before := app.ws.LoadAppSettings()
	_, command := app.Update(types.ModelCatalogUpdatedMsg{Generation: 1})
	if command == nil {
		t.Fatal("catalog update did not refetch the loaded account through the core")
	}
	refetch, ok := command().(settingsui.FetchModelsMsg)
	if !ok || refetch.AccountID != "work" {
		t.Fatalf("catalog update issued %#v, want a silent model refetch", refetch)
	}
	_, _ = app.Update(settingsui.ModelsMsg{ProviderID: refetch.ProviderID, AccountID: refetch.AccountID, Generation: refetch.Generation, Models: []types.ModelEntry{
		{ID: modelID, ContextWindow: 128000, Modalities: []string{provider.ModalityImage}, ModalitySupport: map[string]bool{provider.ModalityImage: true}},
	}})
	if widget.ModelForTest() != model || model.ValueForTest(category, "agent-instructions") != "Keep this unsaved draft" || model.ValueForTest(category, "modality-image") != "yes" {
		t.Fatal("metadata update rebuilt/lost the draft or failed to refresh its capabilities")
	}
	if !reflect.DeepEqual(before, app.ws.LoadAppSettings()) {
		t.Fatal("metadata maintenance silently saved user preferences")
	}
}
