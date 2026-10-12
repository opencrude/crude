package workspace

import (
	"encoding/json"
	"fmt"

	"crude/session"
	ttypes "crude/types"
)

// SaveAccountChange writes one explicit account operation through the app's
// atomic config owner. An unwired workspace must fail, never claim success.
func (w *Workspace) SaveAccountChange(change ttypes.AccountChange) error {
	if w.OnAppAccountChange == nil {
		return fmt.Errorf("application account persistence is unavailable")
	}
	return w.OnAppAccountChange(change)
}

const (
	workspaceSettingsKey   = "workspace_settings"
	legacyAppSettingsDBKey = "app_settings"
)

// SetAppDefaults stores the application-layer snapshot from config.json.
func (w *Workspace) SetAppDefaults(s ttypes.AppSettings) {
	ttypes.ApplyAppDefaults(&s)
	w.appDefaults = s
	w.applyEffectiveSettings()
}

// AppDefaults returns the application-layer settings (no workspace overlay).
func (w *Workspace) AppDefaults() ttypes.AppSettings {
	s := w.appDefaults
	s.Accounts = w.AllAccounts()
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
//
// Stored overlays are re-diffed against the current application layer before
// use. Older releases wrote whole categories, which froze every sibling
// field at the application value of that day; re-diffing keeps only the
// leaves that genuinely differ so untouched fields inherit again.
func (w *Workspace) LoadWorkspaceOverrides() ttypes.WorkspaceOverrides {
	if w == nil || w.WorkspaceDB == nil {
		return ttypes.WorkspaceOverrides{}
	}
	defaults := w.AppDefaults()
	if raw := w.WorkspaceDB.GetSetting(workspaceSettingsKey); raw != "" {
		var overrides ttypes.WorkspaceOverrides
		if err := json.Unmarshal([]byte(raw), &overrides); err == nil {
			return ttypes.OverridesFromDiff(defaults, ttypes.MergeAppWorkspace(defaults, overrides))
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
	return ttypes.OverridesFromDiff(defaults, legacy)
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
	if w.OnAppSettingsSave != nil {
		persisted, err := w.OnAppSettingsSave(s)
		if err != nil {
			w.Logger.Warn("WORKSPACE", "save_app_settings_failed", "error", err.Error())
			return err
		}
		// Account and provider-template operations commit independently of
		// preference drafts. Keep the complete live provider snapshot published
		// by the app owner, rather than re-installing stale templates/defaults.
		s = persisted
	}
	w.appDefaults = s
	if w.sessionCfg != nil && w.sessionCfg.AccountManager != nil {
		// The app callback publishes its normalized, durably saved snapshot.
		// Never replace it again with the stale form's pre-refresh credentials.
		if w.OnAppSettingsSave == nil {
			w.sessionCfg.AccountManager.Replace(s.Accounts)
		}
		w.appDefaults.Accounts = w.sessionCfg.AccountManager.Snapshot()
	}
	w.applyEffectiveSettings()
	return nil
}

func (w *Workspace) applyEffectiveSettings() {
	effective := w.EffectiveAppSettings()
	w.applySettingsProjection(effective)
}

func (w *Workspace) applySettingsProjection(effective ttypes.AppSettings) {
	w.applyProviderSettings(effective)
	if w.LSPMgr != nil {
		w.LSPMgr.SetSettings(effective.LSP)
	}
	if w.sessionCfg != nil && len(effective.Agents.Categories) > 0 {
		session.ApplyWorkspaceAgentDefaults(w.sessionCfg, effective.Agents)
	}
	if w.sessionCfg != nil {
		w.sessionCfg.Memory = effective.Memory
		w.sessionCfg.Capabilities = effective.Capabilities
		w.sessionCfg.Safety = effective.Safety
	}
	if w.HistoryService != nil {
		w.HistoryService.UpdateSettings(effective.LocalHistory)
	}
	w.TermMgr.SetSettings(effective.Terminal)
	for _, frontendSession := range w.Sessions {
		if frontendSession == nil || frontendSession.backend == nil {
			continue
		}
		frontendSession.backend.UpdateWorkspaceDefaults(ttypes.AppSettings{
			Agents:       effective.Agents,
			Accounts:     effective.Accounts,
			Memory:       effective.Memory,
			Capabilities: effective.Capabilities,
			Safety:       effective.Safety,
		})
		frontendSession.installSettings(frontendSession.backend.SettingsSnapshots())
	}
}
