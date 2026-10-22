package popup

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/settings"
	"crude/tui/theme"
	"crude/tui/ui"
	ttypes "crude/types"
)

// WorkspaceSettings adapts the settings surface to a floating window. The
// surface is the one owner of the current model: a section reset or save
// installs a replacement there, and events must reach that replacement.
type WorkspaceSettings struct {
	surface *settings.WorkspaceSettingsSurface
}

func NewWorkspaceSettings(th *theme.Theme, appSets ttypes.AppSettings, presets []ttypes.ProviderPreset) *WorkspaceSettings {
	return NewWorkspaceSettingsScoped(th, appSets, presets, ttypes.SettingsScopeApplication, appSets, ttypes.WorkspaceOverrides{})
}

func NewWorkspaceSettingsScoped(th *theme.Theme, working ttypes.AppSettings, presets []ttypes.ProviderPreset, scope ttypes.SettingsScope, appDefaults ttypes.AppSettings, overrides ttypes.WorkspaceOverrides) *WorkspaceSettings {
	model := settings.NewScoped(th, working, presets, scope, appDefaults, overrides)
	return &WorkspaceSettings{surface: settings.NewSurface(th, model)}
}

func (ws *WorkspaceSettings) Component() ui.Component { return ws.surface }

// ModelForTest exposes the underlying settings model for tests.
func (ws *WorkspaceSettings) ModelForTest() *settings.Model { return ws.surface.Model() }

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
