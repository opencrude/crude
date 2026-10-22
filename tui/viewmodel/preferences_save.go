package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

type preferencesSavedMsg struct {
	target     *settingsui.Model
	generation uint64
	owner      shellport.Workspace
	result     workspace.PreferencesSaveResult
}

func (m *App) savePreferences(request settingsui.SaveMsg) tea.Cmd {
	if m.ws == nil || m.overlayHost == nil {
		return nil
	}
	body := m.settingsSurface(m.overlayHost.Window(ui.OverlayWorkspaceSettings))
	if body == nil || request.Target == nil || body.Model() != request.Target || !request.Target.Saving() {
		return nil
	}
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
	run := owner.PreparePreferencesSave(request.Scope, request.Edits)
	return func() tea.Msg {
		return preferencesSavedMsg{target: request.Target, generation: generation, owner: owner, result: run()}
	}
}

func (m *App) applyPreferencesSaved(message preferencesSavedMsg) {
	if message.owner != m.ws || message.generation != m.workspaceSwitchGen.Load() || m.overlayHost == nil {
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
	m.ws.ApplyPreferencesSave(message.result)
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
	// Save keeps Settings open on the saved values; Close is the way out.
	application, effective, overrides := m.settingsLayers()
	body.SetModel(message.target.SavedModel(application, effective, overrides))
}
