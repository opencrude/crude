package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

type preferencesSavedMsg struct {
	target     *settingsui.Model
	generation uint64
	owner      *workspace.Workspace
	result     workspace.PreferencesSaveResult
}

func (m *App) savePreferences(request settingsui.SaveMsg) tea.Cmd {
	if m.Workspace == nil || m.overlayHost == nil {
		return nil
	}
	body := m.settingsSurface(m.overlayHost.Window(ui.OverlayWorkspaceSettings))
	if body == nil || request.Target == nil || body.Model() != request.Target || !request.Target.Saving() {
		return nil
	}
	owner, generation := m.Workspace, m.workspaceSwitchGen.Load()
	run := owner.PreparePreferencesSave(request.Scope, request.AppSettings, request.Overrides)
	return func() tea.Msg {
		return preferencesSavedMsg{target: request.Target, generation: generation, owner: owner, result: run()}
	}
}

func (m *App) applyPreferencesSaved(message preferencesSavedMsg) {
	if message.owner != m.Workspace || message.generation != m.workspaceSwitchGen.Load() || m.overlayHost == nil {
		return
	}
	body := m.settingsSurface(m.overlayHost.Window(ui.OverlayWorkspaceSettings))
	if body == nil || !body.FinishSave(message.target, message.result.Err) {
		return
	}
	if message.result.Err != nil {
		if m.Feedback != nil {
			m.Feedback.NotifyError("Settings were not saved: " + message.result.Err.Error())
		}
		return
	}
	m.ApplyPreferencesSave(message.result)
	m.clearFramePreview()
	m.applyThemePreset(themePresetFromSettings(message.result.Effective))
	m.applyEditorSettings(message.result.Effective.Editor)
	m.refreshAllSessionSettingsProjections()
	if m.Feedback != nil {
		label := "Application settings saved"
		if message.result.Scope == types.SettingsScopeWorkspace {
			label = "Workspace settings saved"
		}
		m.Feedback.NotifyInfo(label)
	}
	m.overlayHost.Close(ui.OverlayWorkspaceSettings)
}
