package workspace

import (
	"encoding/json"

	"crude/types"
)

// PreferencesSaveResult separates I/O from publication on the workspace owner.
type PreferencesSaveResult struct {
	Scope       types.SettingsScope
	AppSettings types.AppSettings
	Effective   types.AppSettings
	Err         error
}

// PreparePreferencesSave captures the exact persistence owners before a command
// runs. A project switch cannot redirect a late save into its replacement DB.
// The returned work performs no live workspace/session mutation.
func (w *Workspace) PreparePreferencesSave(scope types.SettingsScope, settings types.AppSettings, overrides types.WorkspaceOverrides) func() PreferencesSaveResult {
	database := w.WorkspaceDB
	persistApplication := w.OnAppSettingsSave
	defaults := w.AppDefaults()
	return func() PreferencesSaveResult {
		result := PreferencesSaveResult{Scope: scope, AppSettings: defaults}
		if scope == types.SettingsScopeWorkspace {
			if database != nil {
				encoded, err := json.Marshal(overrides)
				if err == nil {
					err = database.SetSetting(workspaceSettingsKey, string(encoded))
				}
				if err != nil {
					result.Err = err
					return result
				}
			}
			result.Effective = types.MergeAppWorkspace(defaults, overrides)
			return result
		}
		types.ApplyAppDefaults(&settings)
		result.AppSettings = settings
		if persistApplication != nil {
			persisted, err := persistApplication(settings)
			if err != nil {
				result.Err = err
				return result
			}
			result.AppSettings = persisted
		}
		var workspaceOverrides types.WorkspaceOverrides
		if database != nil {
			if encoded := database.GetSetting(workspaceSettingsKey); encoded != "" {
				_ = json.Unmarshal([]byte(encoded), &workspaceOverrides)
			}
		}
		result.Effective = types.MergeAppWorkspace(result.AppSettings, workspaceOverrides)
		return result
	}
}

// ApplyPreferencesSave publishes a successful command snapshot, with no file or
// database reads. The frontend rejects stale workspace/overlay generations first.
func (w *Workspace) ApplyPreferencesSave(result PreferencesSaveResult) {
	if result.Err != nil {
		return
	}
	if result.Scope == types.SettingsScopeApplication {
		w.appDefaults = result.AppSettings
		if w.sessionCfg != nil && w.sessionCfg.AccountManager != nil {
			w.appDefaults.Accounts = w.sessionCfg.AccountManager.Snapshot()
		}
	}
	w.applySettingsProjection(result.Effective)
}
