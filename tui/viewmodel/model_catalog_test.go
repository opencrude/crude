package viewmodel

import (
	"reflect"
	"testing"

	"crude/provider"
	settingsui "crude/tui/settings"
	"crude/types"
)

func TestCatalogUpdateIsSilentAndRefreshesExistingAgentDraft(t *testing.T) {
	app := newTestApp(t)
	wsOf(app).Logger = noopLogger{}
	modelID := "catalog-ui-" + t.TempDir()
	wsOf(app).SetAppDefaults(types.AppSettings{Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{"default": {Model: modelID}}}})
	app.openSettings(types.SettingsScopeApplication)
	fw := app.windowManager.Window("workspace-settings")
	widget := settingsWidgetForTest(t, fw)
	root := widget.Component().(*settingsui.WorkspaceSettingsSurface)
	root.ShowMainAgent()
	model := widget.ModelForTest()
	category := model.SelectedCategory()
	model.SetValueForTest(category, "agent-instructions", "Keep this unsaved draft")
	before := app.ws.LoadAppSettings()
	provider.LoadModelMetas(map[string]provider.ModelMeta{modelID: {Modalities: []string{provider.ModalityImage}, ModalitySupport: map[string]bool{provider.ModalityImage: true}, ContextWindow: 128000}})
	_, command := app.Update(types.ModelCatalogUpdatedMsg{Generation: 1})
	if command != nil {
		t.Fatal("background catalog update started a visible operation or persistence command")
	}
	if widget.ModelForTest() != model || model.ValueForTest(category, "agent-instructions") != "Keep this unsaved draft" || model.ValueForTest(category, "modality-image") != "yes" {
		t.Fatal("metadata update rebuilt/lost the draft or failed to refresh its capabilities")
	}
	if !reflect.DeepEqual(before, app.ws.LoadAppSettings()) {
		t.Fatal("metadata maintenance silently saved user preferences")
	}
}
