package workspace

import (
	"context"
	"encoding/json"
	"fmt"

	"crude/session"
	"crude/types"
)

// PreferencesSaveResult separates I/O from publication on the workspace owner.
type PreferencesSaveResult struct {
	Scope       types.SettingsScope
	AppSettings types.AppSettings
	Effective   types.AppSettings
	Overrides   types.WorkspaceOverrides
	Err         error
	// Only explicit workspace authority edits supply a new native observation.
	AnalysisRootIdentity   string
	AnalysisObserved       bool
	PredictionRootIdentity string
	PredictionObserved     bool
}

// PreparePreferencesSave captures the exact persistence owners before a command
// runs. A project switch cannot redirect a late save into its replacement DB.
// The returned work performs no live workspace/session mutation.
func (w *Workspace) PreparePreferencesSave(scope types.SettingsScope, edits []types.SettingEdit) func() PreferencesSaveResult {
	database := w.WorkspaceDB
	root, observedRoot := w.RootPath, w.analysisRootIdentity
	persistApplication := w.OnAppPreferencesCommit
	defaults := w.AppDefaults()
	var predictionCompleter session.EditorPredictionCompleter
	var predictionConnectionErr error
	if len(predictionEdits(edits)) != 0 {
		if w.sessionCfg == nil || w.sessionCfg.AccountManager == nil {
			predictionConnectionErr = fmt.Errorf("configure an Aeron connection before enabling prediction")
		} else {
			predictionCompleter, predictionConnectionErr = w.sessionCfg.AeronPredictionCompleter()
		}
	}
	validationErr := types.ValidateSettingEdits(types.AppSettings{}, edits)
	if validationErr == nil {
		validationErr = types.ValidateAnalysisSettingEdits(scope, edits)
	}
	if validationErr == nil {
		validationErr = types.ValidatePredictionSettingEdits(scope, edits)
	}
	return func() PreferencesSaveResult {
		result := PreferencesSaveResult{Scope: scope, AppSettings: defaults}
		resultRoot := observedRoot
		if validationErr != nil {
			result.Err = validationErr
			return result
		}
		if scope == types.SettingsScopeWorkspace {
			for _, edit := range edits {
				if len(edit.Path) > 0 && edit.Path[0] == "onboarding" {
					result.Err = fmt.Errorf("introduction and reminder choices belong to Application settings")
					return result
				}
			}
			identity, touched, err := prepareAnalysisBinding(root, edits)
			if err != nil {
				result.Err = err
				return result
			}
			result.AnalysisRootIdentity, result.AnalysisObserved = identity, touched
			// An Off/reset choice revokes its own stored grant, not the native
			// directory observation shared by the independent authorities.
			if touched && identity != "" {
				resultRoot = identity
			}
			if database != nil {
				binding := predictionPreferenceBinding{root: root, completer: predictionCompleter, connectionErr: predictionConnectionErr}
				overrides, err := saveWorkspacePreferences(database, defaults, edits, identity, binding)
				if err != nil {
					result.Err = err
					return result
				}
				if len(predictionEdits(edits)) != 0 && overrides.PredictionRootIdentity != "" {
					resultRoot = overrides.PredictionRootIdentity
					result.PredictionRootIdentity, result.PredictionObserved = resultRoot, true
				}
				result.Overrides = overrides
				result.Effective = boundWorkspaceSettings(defaults, overrides, resultRoot)
				return result
			}
			if touched || len(predictionEdits(edits)) != 0 {
				result.Err = fmt.Errorf("workspace Trust and prediction consent require durable workspace preference storage")
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
		result.Effective = boundWorkspaceSettings(result.AppSettings, workspaceOverrides, resultRoot)
		return result
	}
}

// Capability cards have independent entity commits. A preference draft opened
// before an agent install must not replace those authoritative registration maps.
func saveWorkspacePreferences(database *WorkspaceDB, defaults types.AppSettings, edits []types.SettingEdit, analysisRoot string, prediction predictionPreferenceBinding) (types.WorkspaceOverrides, error) {
	if err := types.ValidateSettingEdits(types.AppSettings{}, edits); err != nil {
		return types.WorkspaceOverrides{}, err
	}
	if err := types.ValidateAnalysisSettingEdits(types.SettingsScopeWorkspace, edits); err != nil {
		return types.WorkspaceOverrides{}, err
	}
	if err := types.ValidatePredictionSettingEdits(types.SettingsScopeWorkspace, edits); err != nil {
		return types.WorkspaceOverrides{}, err
	}
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
	// Ordinary sparse editing canonicalizes JSON objects. A neighbor save must
	// never repair malformed authority data into a grant. Preserve its original
	// bytes unless this exact transaction explicitly changes prediction.
	storedPrediction := currentOverrides.Prediction
	if len(predictionEdits(edits)) != 0 && len(storedPrediction) != 0 {
		if _, err := types.DecodeWorkspacePredictionSettings(storedPrediction); err != nil {
			// An explicit whole-category reset/replacement can repair a corrupt
			// choice; leaf patches cannot silently resolve ambiguous members.
			replaces := false
			for _, edit := range edits {
				replaces = replaces || len(edit.Path) == 1 && edit.Path[0] == "prediction"
			}
			if !replaces {
				return types.WorkspaceOverrides{}, fmt.Errorf("reset invalid workspace prediction settings before changing individual fields")
			}
		}
	}
	if err := types.ApplySettingsEdits(&currentOverrides, edits); err != nil {
		return types.WorkspaceOverrides{}, err
	}
	if len(predictionEdits(edits)) == 0 {
		currentOverrides.Prediction = storedPrediction
	}
	if len(analysisEdits(edits)) != 0 {
		currentOverrides.AnalysisRootIdentity = ""
		if types.MergeAppWorkspace(defaults, currentOverrides).Analysis.Trust == types.AnalysisProjectExecutionAllowed {
			if analysisRoot == "" {
				return types.WorkspaceOverrides{}, fmt.Errorf("workspace Trust grant has no owned native directory identity")
			}
			currentOverrides.AnalysisRootIdentity = analysisRoot
		}
	}
	if err := bindPredictionPreferences(&currentOverrides, edits, prediction, analysisRoot); err != nil {
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
	if result.AnalysisObserved && result.AnalysisRootIdentity != "" {
		w.analysisRootIdentity = result.AnalysisRootIdentity
	}
	if result.PredictionObserved {
		w.analysisRootIdentity = result.PredictionRootIdentity
	}
	w.applySettingsProjection(result.Effective)
}
