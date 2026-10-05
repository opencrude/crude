package workspace

import (
	"context"
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
				effective, err := saveWorkspacePreferences(database, defaults, overrides)
				if err != nil {
					result.Err = err
					return result
				}
				result.Effective = effective
				return result
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

// Capability cards have independent entity commits. A preference draft opened
// before an agent install must not replace those authoritative registration maps.
func saveWorkspacePreferences(database *WorkspaceDB, defaults types.AppSettings, overrides types.WorkspaceOverrides) (types.AppSettings, error) {
	ctx := context.Background()
	tx, err := database.DB.BeginTx(ctx, nil)
	if err != nil {
		return types.AppSettings{}, err
	}
	defer func() { _ = tx.Rollback() }()
	currentOverrides, err := loadWorkspaceOverridesTx(ctx, tx, defaults)
	if err != nil {
		return types.AppSettings{}, err
	}
	current := types.MergeAppWorkspace(defaults, currentOverrides)
	desired := types.MergeAppWorkspace(defaults, overrides)
	desired.Capabilities.Skills = current.Capabilities.Skills
	desired.Capabilities.Connections = current.Capabilities.Connections
	desired.Capabilities.Sources = current.Capabilities.Sources
	encoded, err := json.Marshal(types.OverridesFromDiff(defaults, desired))
	if err != nil {
		return types.AppSettings{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspace_metadata(key,value,updated_at) VALUES(?,?,unixepoch()*1000000000)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at
	`, workspaceSettingsKey, string(encoded)); err != nil {
		return types.AppSettings{}, err
	}
	if err := tx.Commit(); err != nil {
		return types.AppSettings{}, err
	}
	return desired, nil
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
