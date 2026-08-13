package popup

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/settings"
	"crude/tui/theme"
	ttypes "crude/tui/types"
	"crude/tui/ui"
)

type WorkspaceSettings struct {
	model   *settings.Model
	surface *settings.WorkspaceSettingsSurface
}

func NewWorkspaceSettings(th *theme.Theme, appSets ttypes.AppSettings, presets []ttypes.ProviderPreset) *WorkspaceSettings {
	return NewWorkspaceSettingsScoped(th, appSets, presets, ttypes.SettingsScopeApplication, appSets, ttypes.WorkspaceOverrides{})
}

func NewWorkspaceSettingsScoped(th *theme.Theme, working ttypes.AppSettings, presets []ttypes.ProviderPreset, scope ttypes.SettingsScope, appDefaults ttypes.AppSettings, overrides ttypes.WorkspaceOverrides) *WorkspaceSettings {
	model := settings.NewScoped(th, working, presets, scope, appDefaults, overrides)
	return &WorkspaceSettings{
		model:   model,
		surface: settings.NewSurface(th, model),
	}
}

func (ws *WorkspaceSettings) Component() ui.Component { return ws.surface }

func (ws *WorkspaceSettings) SetFocusHandler(fn func(ui.ComponentID)) {
	ws.surface.SetFocusHandler(fn)
}

func (ws *WorkspaceSettings) FocusSearch() { ws.surface.FocusSearch() }

func (ws *WorkspaceSettings) HandleEvent(msg tea.Msg) tea.Cmd {
	oldPane := ws.model.Pane()
	updated, cmd := ws.model.Update(msg)
	ws.model = &updated
	ws.surface.SetModel(ws.model)
	if oldPane == 1 && ws.model.Pane() == 0 {
		ws.surface.FocusCategories()
	}
	return cmd
}
