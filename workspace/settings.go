package workspace

import (
	"bytes"
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

const workspaceSettingsKey = "workspace_settings"

// SetAppDefaults stores the application-layer snapshot from config.json.
func (w *Workspace) SetAppDefaults(s ttypes.AppSettings) {
	ttypes.ApplyAppDefaults(&s)
	s.Analysis = ttypes.DefaultWorkspaceAnalysisSettings()
	s.Prediction = ttypes.DefaultWorkspacePredictionSettings()
	w.appDefaults = ttypes.CloneAppSettings(s)
	w.applyEffectiveSettings()
}

// AppDefaults returns the application-layer settings (no workspace overlay).
func (w *Workspace) AppDefaults() ttypes.AppSettings {
	s := ttypes.CloneAppSettings(w.appDefaults)
	s.Accounts = w.AllAccounts()
	ttypes.ApplyAppDefaults(&s)
	s.Analysis = ttypes.DefaultWorkspaceAnalysisSettings()
	s.Prediction = ttypes.DefaultWorkspacePredictionSettings()
	return s
}

// EffectiveAppSettings merges application defaults with workspace overrides.
func (w *Workspace) EffectiveAppSettings() ttypes.AppSettings {
	effective := boundWorkspaceSettings(w.AppDefaults(), w.LoadWorkspaceOverrides(), w.analysisRootIdentity)
	if effective.Analysis.Trusted() && w.LSPMgr != nil {
		effective.Analysis = w.LSPMgr.AnalysisPolicy()
	}
	return effective
}

// LoadWorkspaceOverrides detaches the committed sparse overlay from the DB's
// immutable memory projection. It performs no storage I/O or writer locking.
// Saves store only explicit choices; the stored overlay is used as is,
// including explicit values equal to their parent.
func (w *Workspace) LoadWorkspaceOverrides() ttypes.WorkspaceOverrides {
	if w == nil || w.WorkspaceDB == nil {
		return ttypes.WorkspaceOverrides{}
	}
	var overrides ttypes.WorkspaceOverrides
	if raw := w.WorkspaceDB.GetSetting(workspaceSettingsKey); raw != "" {
		if err := json.Unmarshal([]byte(raw), &overrides); err != nil {
			// A projection read must remain memory-only, including malformed rows.
			// Do not synchronously write a log from the actor on every observation.
			return ttypes.WorkspaceOverrides{}
		}
	}
	return overrides
}

// SaveWorkspaceSettings persists sparse workspace overrides and reapplies
// the effective layer to live sessions and the LSP manager.
func (w *Workspace) SaveWorkspaceSettings(overrides ttypes.WorkspaceOverrides) error {
	if w.WorkspaceDB != nil {
		w.WorkspaceDB.settingsWriteMu.Lock()
		defer w.WorkspaceDB.settingsWriteMu.Unlock()
	}
	// Entity/snapshot saves never grant, reset or rebind execution trust. The
	// exact workspace preference transaction is its single mutation owner.
	current := w.LoadWorkspaceOverrides()
	if w.WorkspaceDB != nil {
		if row := w.WorkspaceDB.committedSettingsRow(); row != "" {
			var checked ttypes.WorkspaceOverrides
			if err := json.Unmarshal([]byte(row), &checked); err != nil {
				return fmt.Errorf("invalid stored workspace settings: %w", err)
			}
		}
	}
	if overrides.Analysis != nil && !bytes.Equal(overrides.Analysis, current.Analysis) {
		return fmt.Errorf("workspace Trust must be changed through workspace preferences")
	}
	if overrides.Prediction != nil && !bytes.Equal(overrides.Prediction, current.Prediction) {
		return fmt.Errorf("prediction consent must be changed through workspace preferences")
	}
	overrides.Prediction = current.Prediction
	overrides.PredictionRootIdentity, overrides.PredictionConnectionIdentity, overrides.PredictionGrantID = current.PredictionRootIdentity, current.PredictionConnectionIdentity, current.PredictionGrantID
	overrides.Analysis, overrides.AnalysisRootIdentity = current.Analysis, current.AnalysisRootIdentity
	if overrides.Analysis != nil {
		if err := ttypes.ValidateAnalysisSettingEdits(ttypes.SettingsScopeWorkspace,
			[]ttypes.SettingEdit{{Path: []string{"analysis"}, After: overrides.Analysis}}); err != nil {
			return err
		}
	}
	if w.WorkspaceDB != nil {
		data, err := ttypes.MarshalWorkspaceOverridesPreservingToolDecisions(overrides, current.Capabilities)
		if err != nil {
			return err
		}
		_, err = w.WorkspaceDB.DB.Exec(`INSERT INTO workspace_metadata(key,value,updated_at) VALUES(?,?,unixepoch()*1000000000) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, workspaceSettingsKey, string(data))
		if err != nil {
			w.Logger.Warn("WORKSPACE", "save_workspace_settings_failed", "error", err.Error())
			return err
		}
		w.WorkspaceDB.publishSettingsRow(string(data))
	}
	w.applyEffectiveSettings()
	return nil
}

// SaveAppSettings persists application-layer settings to config.json via
// OnAppSettingsSave. It does not write workspace.db; workspace overrides
// stay on SaveWorkspaceSettings.
func (w *Workspace) SaveAppSettings(s ttypes.AppSettings) error {
	if len(s.Capabilities.ProjectToolDecisions) != 0 {
		return fmt.Errorf("project tool decisions belong to Workspace setup")
	}
	if s.Analysis.Trust != ttypes.AnalysisUndecided {
		return fmt.Errorf("analysis trust belongs to Workspace settings")
	}
	if s.Prediction != (ttypes.WorkspacePredictionSettings{}) && s.Prediction != ttypes.DefaultWorkspacePredictionSettings() {
		return fmt.Errorf("prediction consent belongs to Workspace settings")
	}
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
		w.publishAnalysisPolicy()
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
