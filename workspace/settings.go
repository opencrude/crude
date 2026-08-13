package workspace

import (
	"encoding/json"

	"crude/session"
	ttypes "crude/types"
)

const (
	workspaceSettingsKey     = "workspace_settings"
	legacyAppSettingsDBKey   = "app_settings"
)

// SetAppDefaults stores the application-layer snapshot from config.json.
func (w *Workspace) SetAppDefaults(s ttypes.AppSettings) {
	ttypes.ApplyAppDefaults(&s)
	w.appDefaults = s
	if w.LSPMgr != nil {
		w.LSPMgr.SetSettings(w.EffectiveAppSettings().LSP)
	}
}

// AppDefaults returns the application-layer settings (no workspace overlay).
func (w *Workspace) AppDefaults() ttypes.AppSettings {
	s := w.appDefaults
	ttypes.ApplyAppDefaults(&s)
	return s
}

// EffectiveAppSettings merges application defaults with workspace overrides.
func (w *Workspace) EffectiveAppSettings() ttypes.AppSettings {
	return ttypes.MergeAppWorkspace(w.AppDefaults(), w.LoadWorkspaceOverrides())
}

// LoadWorkspaceOverrides reads the sparse workspace overlay from workspace.db.
// Legacy `app_settings` rows are treated as overrides so existing projects
// keep their per-workspace theme/editor/LSP values.
func (w *Workspace) LoadWorkspaceOverrides() ttypes.WorkspaceOverrides {
	if w == nil || w.WorkspaceDB == nil {
		return ttypes.WorkspaceOverrides{}
	}
	if raw := w.WorkspaceDB.GetSetting(workspaceSettingsKey); raw != "" {
		var overrides ttypes.WorkspaceOverrides
		if err := json.Unmarshal([]byte(raw), &overrides); err == nil {
			return overrides
		}
	}
	raw := w.WorkspaceDB.GetSetting(legacyAppSettingsDBKey)
	if raw == "" {
		return ttypes.WorkspaceOverrides{}
	}
	var legacy ttypes.AppSettings
	if err := json.Unmarshal([]byte(raw), &legacy); err != nil {
		return ttypes.WorkspaceOverrides{}
	}
	return ttypes.OverridesFromDiff(w.AppDefaults(), legacy)
}

// SaveWorkspaceSettings persists sparse workspace overrides and reapplies
// the effective layer to live sessions and the LSP manager.
func (w *Workspace) SaveWorkspaceSettings(overrides ttypes.WorkspaceOverrides) error {
	if w.WorkspaceDB != nil {
		data, err := json.Marshal(overrides)
		if err != nil {
			return err
		}
		if err := w.WorkspaceDB.SetSetting(workspaceSettingsKey, string(data)); err != nil {
			w.Logger.Warn("WORKSPACE", "save_workspace_settings_failed", "error", err.Error())
			return err
		}
	}
	w.applyEffectiveSettings()
	return nil
}

// SaveAppSettings persists application-layer settings to config.json via
// OnAppSettingsSave. It does not write workspace.db — workspace overrides
// stay on SaveWorkspaceSettings.
func (w *Workspace) SaveAppSettings(s ttypes.AppSettings) error {
	ttypes.ApplyAppDefaults(&s)
	w.appDefaults = s
	if w.OnAppSettingsSave != nil {
		if err := w.OnAppSettingsSave(s); err != nil {
			w.Logger.Warn("WORKSPACE", "save_app_settings_failed", "error", err.Error())
			return err
		}
	}
	w.applyEffectiveSettings()
	return nil
}

func (w *Workspace) applyEffectiveSettings() {
	effective := w.EffectiveAppSettings()
	if w.LSPMgr != nil {
		w.LSPMgr.SetSettings(effective.LSP)
	}
	if w.sessionCfg != nil && len(effective.Agents.Categories) > 0 {
		session.ApplyWorkspaceAgentDefaults(w.sessionCfg, effective.Agents)
	}
	for _, frontendSession := range w.Sessions {
		if frontendSession == nil || frontendSession.backend == nil {
			continue
		}
		frontendSession.backend.UpdateWorkspaceDefaults(ttypes.AppSettings{
			Agents:         effective.Agents,
			ActiveProvider: effective.ActiveProvider,
			Providers:      effective.Providers,
		})
		if frontendSession.Settings != nil {
			frontendSession.Settings.SetInheritance(
				frontendSession.backend.SettingsSnapshot(),
				frontendSession.backend.WorkspaceSettingsSnapshot(),
				frontendSession.backend.SessionOverridesSnapshot(),
			)
		}
	}
}
