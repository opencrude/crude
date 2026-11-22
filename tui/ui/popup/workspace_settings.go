package popup

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/settingspages"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/setting"
)

// WorkspaceSettings adapts the settings surface to a floating window. The
// surface is the one owner of the current model: a section reset or save
// installs a replacement there, and events must reach that replacement.
type WorkspaceSettings struct {
	surface *settingspages.WorkspaceSettingsSurface
}

// NewWorkspaceSettings builds the settings window for application scope.
func NewWorkspaceSettings(th *theme.Theme, appSets setting.App, presets []setting.ProviderPreset) *WorkspaceSettings {
	return NewWorkspaceSettingsScoped(th, appSets, presets, setting.ScopeApplication, appSets, setting.WorkspaceOverrides{})
}

// NewWorkspaceSettingsScoped builds the settings window for one scope and its defaults.
func NewWorkspaceSettingsScoped(th *theme.Theme, working setting.App, presets []setting.ProviderPreset, scope setting.Scope, appDefaults setting.App, overrides setting.WorkspaceOverrides) *WorkspaceSettings {
	model := settingspages.NewScoped(th, working, presets, scope, appDefaults, overrides)
	return &WorkspaceSettings{surface: settingspages.NewSurface(th, model)}
}

func (ws *WorkspaceSettings) Component() ui.Component { return ws.surface }

// ModelForTest exposes the underlying settings model for tests.
func (ws *WorkspaceSettings) ModelForTest() *settingspages.Model { return ws.surface.Model() }

func (ws *WorkspaceSettings) SetFocusHandler(fn func(ui.ComponentID)) {
	ws.surface.SetFocusHandler(fn)
}

func (ws *WorkspaceSettings) FocusSearch() { ws.surface.FocusSearch() }

func (ws *WorkspaceSettings) ShowAccounts() { ws.surface.ShowAccounts() }

func (ws *WorkspaceSettings) ShowAccountSetup() { ws.surface.ShowAccountSetup() }

func (ws *WorkspaceSettings) HandleEvent(msg tea.Msg) tea.Cmd {
	model := ws.surface.Model()
	oldPane := model.Pane()
	// Update mutates the model in place and returns a copy; keep the surface
	// on the same instance. Replacing it with the copy would orphan the form's
	// onChanged closures, which capture the original pointer.
	_, cmd := model.Update(msg)
	ws.surface.SetModel(model)
	if oldPane == 1 && model.Pane() == 0 {
		ws.surface.FocusCategories()
	}
	return cmd
}
