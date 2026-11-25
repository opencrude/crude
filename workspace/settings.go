package workspace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"

	"crude/agent/session"
	"crude/types/setting"
)

// SaveAccountChange writes one explicit account operation through the app's
// atomic config owner. An unwired workspace must fail, never claim success.
func (w *Workspace) SaveAccountChange(change setting.AccountChange) error {
	if w.OnAppAccountChange == nil {
		return fmt.Errorf("application account persistence is unavailable")
	}
	return w.OnAppAccountChange(change)
}

const workspaceSettingsKey = "workspace_settings"

// SetAppDefaults stores the application-layer snapshot from config.json.
func (w *Workspace) SetAppDefaults(s setting.App) {
	setting.ApplyAppDefaults(&s)
	s.Analysis = setting.DefaultWorkspaceAnalysis()
	s.Prediction = setting.DefaultWorkspacePrediction()
	routesChanged := !slices.Equal(w.appDefaults.Capabilities.WebSearchRoutes, s.Capabilities.WebSearchRoutes)
	w.appDefaults = setting.CloneApp(s)
	if routesChanged {
		// The application publishes its search methods to every retained
		// workspace. Updating them must not reconfigure transports or inspect
		// skill files.
		w.publishWebSearchRoutes(s.Capabilities.WebSearchRoutes)
	}
	w.applyEffectiveSettings()
}

// AppDefaults returns the application-layer settings (no workspace overlay).
func (w *Workspace) AppDefaults() setting.App {
	s := setting.CloneApp(w.appDefaults)
	s.Accounts = w.AllAccounts()
	setting.ApplyAppDefaults(&s)
	s.Analysis = setting.DefaultWorkspaceAnalysis()
	s.Prediction = setting.DefaultWorkspacePrediction()
	return s
}

// EffectiveAppSettings merges application defaults with workspace overrides.
func (w *Workspace) EffectiveAppSettings() setting.App {
	defaults := w.AppDefaults()
	defaults.Entry = w.entry
	effective := boundWorkspaceSettings(defaults, w.LoadWorkspaceOverrides(), w.analysisRootIdentity)
	if effective.Analysis.Trusted() && w.LSPMgr != nil {
		effective.Analysis = w.LSPMgr.AnalysisPolicy()
	}
	return effective
}

// EffectiveAnalysisSettings is EffectiveAppSettings().Analysis without
// merging every other setting: UI ticks read Trust, and the full merge clones
// the language catalog.
func (w *Workspace) EffectiveAnalysisSettings() setting.WorkspaceAnalysis {
	overrides := w.LoadWorkspaceOverrides()
	analysis := boundAnalysis(setting.WorkspaceAnalysisChoice(overrides), overrides, w.analysisRootIdentity)
	if analysis.Trusted() && w.LSPMgr != nil {
		analysis = w.LSPMgr.AnalysisPolicy()
	}
	return analysis
}

// LoadWorkspaceOverrides detaches the committed sparse overlay from the DB's
// immutable memory projection. It performs no storage I/O or writer locking.
// Saves store only explicit choices; the stored overlay is used as is,
// including explicit values equal to their parent.
func (w *Workspace) LoadWorkspaceOverrides() setting.WorkspaceOverrides {
	if w == nil || w.WorkspaceDB == nil {
		return setting.WorkspaceOverrides{}
	}
	var overrides setting.WorkspaceOverrides
	if raw := w.WorkspaceDB.Setting(workspaceSettingsKey); raw != "" {
		if err := json.Unmarshal([]byte(raw), &overrides); err != nil {
			// A projection read must remain memory-only, including malformed rows.
			// Do not synchronously write a log from the actor on every observation.
			return setting.WorkspaceOverrides{}
		}
	}
	return overrides
}

// SaveWorkspaceSettings persists sparse workspace overrides and reapplies
// the effective layer to live sessions and the LSP manager.
func (w *Workspace) SaveWorkspaceSettings(overrides setting.WorkspaceOverrides) error {
	if w.WorkspaceDB != nil {
		w.WorkspaceDB.settingsWriteMu.Lock()
		defer w.WorkspaceDB.settingsWriteMu.Unlock()
	}
	// Entity/snapshot saves never grant, reset or rebind execution trust. The
	// exact workspace preference transaction is its single mutation owner.
	current := w.LoadWorkspaceOverrides()
	if w.WorkspaceDB != nil {
		if row := w.WorkspaceDB.committedSettingsRow(); row != "" {
			var checked setting.WorkspaceOverrides
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
		if err := setting.ValidateAnalysisEdits(setting.ScopeWorkspace,
			[]setting.Edit{{Path: []string{"analysis"}, After: overrides.Analysis}}); err != nil {
			return err
		}
	}
	if w.WorkspaceDB != nil {
		data, err := setting.MarshalWorkspaceOverridesPreservingToolDecisions(overrides, current.Capabilities)
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
func (w *Workspace) SaveAppSettings(s setting.App) error {
	if len(s.Capabilities.ProjectToolDecisions) != 0 {
		return fmt.Errorf("project tool decisions belong to Workspace setup")
	}
	if s.Analysis.Trust != setting.AnalysisUndecided {
		return fmt.Errorf("analysis trust belongs to Workspace settings")
	}
	if s.Prediction != (setting.WorkspacePrediction{}) && s.Prediction != setting.DefaultWorkspacePrediction() {
		return fmt.Errorf("prediction consent belongs to Workspace settings")
	}
	setting.ApplyAppDefaults(&s)
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

func (w *Workspace) applySettingsProjection(effective setting.App) {
	w.applyProviderSettings(effective)
	defer w.configureSuggestions(effective)
	if w.LSPMgr != nil {
		w.publishAnalysisPolicy()
		w.LSPMgr.SetSettings(effective.LSP)
		if w.deferredAnalysis != nil {
			w.startAnalysisWhenNeeded(effective.LSP.Enabled)
		}
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
		frontendSession.backend.UpdateWorkspaceDefaults(setting.App{
			Agents:       effective.Agents,
			Accounts:     effective.Accounts,
			Memory:       effective.Memory,
			Capabilities: effective.Capabilities,
			Safety:       effective.Safety,
		})
		frontendSession.installSettings(frontendSession.backend.SettingsSnapshots())
	}
}

// SetFileOnly changes the retained language-service demand. The factory sets it
// before analysis starts. A project attachment can promote the same actor.
func (w *Workspace) SetFileOnly(only bool) bool {
	if w.entry.FileOnly == only {
		return false
	}
	w.entry.FileOnly = only
	if w.LSPMgr != nil {
		effective := w.EffectiveAppSettings()
		w.LSPMgr.SetSettings(effective.LSP)
		if w.deferredAnalysis != nil {
			w.startAnalysisWhenNeeded(effective.LSP.Enabled)
		}
	}
	return true
}
