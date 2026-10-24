package viewmodel

import (
	"encoding/json"

	tea "charm.land/bubbletea/v2"
	"crude/shellport"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

// Settings shortcuts use the ordinary prepared save and its durable receipt.
type settingsShortcutResultMsg struct {
	owner      shellport.Workspace
	generation uint64
	result     workspace.PreferencesSaveResult
	label      string
	reset      *settingsui.Model
	category   int
}

func (m *App) saveSettingsShortcut(scope types.SettingsScope, edits []types.SettingEdit, label string, reset *settingsui.Model, category int) tea.Cmd {
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
	run := owner.PreparePreferencesSave(scope, edits)
	return func() tea.Msg {
		return settingsShortcutResultMsg{owner: owner, generation: generation, result: run(), label: label, reset: reset, category: category}
	}
}

func (m *App) applySettingsShortcut(msg settingsShortcutResultMsg) {
	if msg.owner != m.ws || msg.generation != m.workspaceSwitchGen.Load() {
		return
	}
	if msg.result.Err != nil {
		if msg.reset != nil {
			msg.reset.FinishSave(msg.result.Err)
			if m.overlayHost != nil {
				if body := m.settingsSurface(m.overlayHost.Window(ui.OverlayWorkspaceSettings)); body != nil && body.Model() == msg.reset {
					body.SetModel(msg.reset)
				}
			}
		}
		m.Feedback.NotifyError(msg.result.Err.Error())
		return
	}
	m.ws.ApplyPreferencesSave(msg.result)
	effective := m.ws.LoadAppSettings()
	m.applyEditorSettings(effective.Editor)
	m.applyThemePreset(themePresetFromSettings(effective))
	m.refreshAllSessionSettingsProjections()
	if msg.reset != nil && m.overlayHost != nil {
		body := m.settingsSurface(m.overlayHost.Window(ui.OverlayWorkspaceSettings))
		if body != nil && body.Model() == msg.reset {
			msg.reset.FinishSave(nil)
			application, effective, overrides := m.settingsLayers()
			body.CancelRemoteAccess()
			body.SetModel(msg.reset.SectionResetModel(application, effective, overrides, msg.category))
			m.bindRemoteSettings(body)
		}
	}
	m.Feedback.NotifyInfo(msg.label)
}

func promotedSessionEdits(settings types.Settings) []types.SettingEdit {
	var edits []types.SettingEdit
	for role, value := range settings.SessionOverrides {
		if role != "default" && role != "researcher" && role != "crafter" && role != "aeron" && role != "memory" {
			continue
		}
		// These are the session-owned role choices, not resolved provider facts.
		for _, field := range []struct {
			name    string
			value   any
			present bool
		}{
			{"account_id", value.AccountID, value.AccountID != ""}, {"Model", value.Model, value.Model != ""},
			{"ThinkingEffort", value.ThinkingEffort, value.ThinkingEffort != ""},
			{"context_compaction_percent", value.ContextCompactionPercent, value.ContextCompactionPercent != nil},
			{"context_retention_tokens", value.ContextRetentionTokens, value.ContextRetentionTokens != nil},
			{"service_tier", value.ServiceTier, value.ServiceTier != ""},
			{"AllowedTools", value.AllowedTools, value.AllowedTools != nil}, {"DeniedTools", value.DeniedTools, value.DeniedTools != nil},
			{"skill_modes", value.SkillModes, value.SkillModes != nil},
		} {
			if !field.present {
				continue
			}
			path := []string{"agents", "Categories", role, field.name}
			if role == "memory" {
				path = []string{"memory", "memory_category", field.name}
			}
			raw, _ := json.Marshal(field.value)
			edits = append(edits, types.SettingEdit{Path: path, After: raw})
		}
	}
	if settings.SessionMultiAgent != nil {
		raw, _ := json.Marshal(*settings.SessionMultiAgent)
		edits = append(edits, types.SettingEdit{Path: []string{"agents", "Enabled"}, After: raw})
	}
	if settings.SessionMaxSubAgents != nil {
		raw, _ := json.Marshal(*settings.SessionMaxSubAgents)
		edits = append(edits, types.SettingEdit{Path: []string{"agents", "MaxConcurrent"}, After: raw})
	}
	return edits
}
