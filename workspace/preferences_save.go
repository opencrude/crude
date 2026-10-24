package workspace

import (
	"context"
	"encoding/json"
	"fmt"

	"crude/types"
)

// PreferencesSaveResult separates I/O from publication on the workspace owner.
type PreferencesSaveResult struct {
	Scope       types.SettingsScope
	AppSettings types.AppSettings
	Effective   types.AppSettings
	Overrides   types.WorkspaceOverrides
	Err         error
}

// PreparePreferencesSave captures the exact persistence owners before a command
// runs. A project switch cannot redirect a late save into its replacement DB.
// The returned work performs no live workspace/session mutation.
func (w *Workspace) PreparePreferencesSave(scope types.SettingsScope, edits []types.SettingEdit) func() PreferencesSaveResult {
	database := w.WorkspaceDB
	persistApplication := w.OnAppPreferencesCommit
	defaults := w.AppDefaults()
	return func() PreferencesSaveResult {
		result := PreferencesSaveResult{Scope: scope, AppSettings: defaults}
		if scope == types.SettingsScopeWorkspace {
			for _, edit := range edits {
				if len(edit.Path) > 0 && edit.Path[0] == "onboarding" {
					result.Err = fmt.Errorf("introduction and reminder choices belong to Application settings")
					return result
				}
			}
			if database != nil {
				overrides, err := saveWorkspacePreferences(database, defaults, edits)
				if err != nil {
					result.Err = err
					return result
				}
				result.Overrides = overrides
				result.Effective = types.MergeAppWorkspace(defaults, overrides)
				return result
			}
			result.Err = types.ApplySettingsEdits(&result.Overrides, edits)
			result.Effective = types.MergeAppWorkspace(defaults, result.Overrides)
			return result
		}
		settings := types.CloneAppSettings(defaults)
		if err := types.ApplySettingsEdits(&settings, edits); err != nil {
			result.Err = err
			return result
		}
		result.AppSettings = settings
		if persistApplication != nil {
			persisted, err := persistApplication(edits)
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
func saveWorkspacePreferences(database *WorkspaceDB, defaults types.AppSettings, edits []types.SettingEdit) (types.WorkspaceOverrides, error) {
	database.settingsWriteMu.Lock()
	defer database.settingsWriteMu.Unlock()
	ctx := context.Background()
	tx, err := database.DB.BeginTx(ctx, nil)
	if err != nil {
		return types.WorkspaceOverrides{}, err
	}
	defer func() { _ = tx.Rollback() }()
	currentOverrides, err := loadWorkspaceOverridesTx(ctx, tx, defaults)
	if err != nil {
		return types.WorkspaceOverrides{}, err
	}
	if err := types.ApplySettingsEdits(&currentOverrides, edits); err != nil {
		return types.WorkspaceOverrides{}, err
	}
	encoded, err := json.Marshal(currentOverrides)
	if err != nil {
		return types.WorkspaceOverrides{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspace_metadata(key,value,updated_at) VALUES(?,?,unixepoch()*1000000000)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at
	`, workspaceSettingsKey, string(encoded)); err != nil {
		return types.WorkspaceOverrides{}, err
	}
	if err := tx.Commit(); err != nil {
		return types.WorkspaceOverrides{}, err
	}
	database.publishSettingsRow(string(encoded))
	return currentOverrides, nil
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
