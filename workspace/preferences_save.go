package workspace

import (
	"context"
	"encoding/json"
	"fmt"

	"crude/agent/session"
	"crude/types/setting"
	"crude/workspace/localhistory"
)

// PreferencesSaveResult separates I/O from publication on the workspace owner.
type PreferencesSaveResult struct {
	history     *localhistory.Store
	Scope       setting.Scope
	AppSettings setting.App
	Effective   setting.App
	Overrides   setting.WorkspaceOverrides
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
func (w *Workspace) PreparePreferencesSave(scope setting.Scope, edits []setting.Edit) func() PreferencesSaveResult {
	database := w.WorkspaceDB
	root, observedRoot := w.RootPath, w.analysisRootIdentity
	persistApplication := w.OnAppPreferencesCommit
	defaults := w.AppDefaults()
	entry := w.entry
	workspaceDefaults := defaults
	workspaceDefaults.Entry = entry
	needHistory := entry.FolderKind != "" && w.LocalHistory == nil
	workspaceDir := w.WorkspaceDir
	overridesAtOpen := w.LoadWorkspaceOverrides()
	var predictionCompleter session.EditorPredictionCompleter
	var predictionConnectionErr error
	if len(predictionEdits(edits)) != 0 {
		if w.sessionCfg == nil || w.sessionCfg.AccountManager == nil {
			predictionConnectionErr = fmt.Errorf("configure an Errand connection before enabling prediction")
		} else {
			predictionCompleter, predictionConnectionErr = w.sessionCfg.SuggestionCompleter()
		}
	}
	validationErr := setting.ValidateEdits(setting.App{}, edits)
	if validationErr == nil {
		validationErr = setting.ValidateProjectToolDecisionEdits(edits)
	}
	if validationErr == nil {
		validationErr = setting.ValidateAnalysisEdits(scope, edits)
	}
	if validationErr == nil {
		validationErr = setting.ValidatePredictionEdits(scope, edits)
	}
	return func() PreferencesSaveResult {
		result := PreferencesSaveResult{Scope: scope, AppSettings: defaults}
		resultRoot := observedRoot
		if validationErr != nil {
			result.Err = validationErr
			return result
		}
		if scope == setting.ScopeWorkspace {
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
				if needHistory {
					candidate := overridesAtOpen
					if err := setting.ApplyEdits(&candidate, edits); err != nil {
						result.Err = err
						return result
					}
					if setting.MergeAppWorkspace(workspaceDefaults, candidate).LocalHistory.EnabledValue() {
						result.history, err = openLocalHistory(root, workspaceDir)
						if err != nil {
							result.Err = fmt.Errorf("cannot turn on Local History: %w", err)
							return result
						}
					}
				}
				overrides, err := saveWorkspacePreferences(database, defaults, edits, identity, binding)
				if err != nil {
					if result.history != nil {
						_ = result.history.Close()
					}
					result.Err = err
					return result
				}
				if len(predictionEdits(edits)) != 0 && overrides.PredictionRootIdentity != "" {
					resultRoot = overrides.PredictionRootIdentity
					result.PredictionRootIdentity, result.PredictionObserved = resultRoot, true
				}
				result.Overrides = overrides
				result.Effective = boundWorkspaceSettings(workspaceDefaults, overrides, resultRoot)
				return result
			}
			if touched || len(predictionEdits(edits)) != 0 {
				result.Err = fmt.Errorf("workspace Trust and prediction consent require durable workspace preference storage")
				return result
			}
			result.Err = setting.ApplyEdits(&result.Overrides, edits)
			result.Effective = setting.MergeAppWorkspace(workspaceDefaults, result.Overrides)
			return result
		}
		settings := setting.CloneApp(defaults)
		if err := setting.ApplyEdits(&settings, edits); err != nil {
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
		var workspaceOverrides setting.WorkspaceOverrides
		if database != nil {
			if encoded := database.Setting(workspaceSettingsKey); encoded != "" {
				_ = json.Unmarshal([]byte(encoded), &workspaceOverrides)
			}
		}
		workspaceDefaults = result.AppSettings
		workspaceDefaults.Entry = entry
		result.Effective = boundWorkspaceSettings(workspaceDefaults, workspaceOverrides, resultRoot)
		return result
	}
}

// Capability cards have independent entity commits. A preference draft opened
// before an agent install must not replace those authoritative registration maps.
func saveWorkspacePreferences(database *DB, defaults setting.App, edits []setting.Edit, analysisRoot string, prediction predictionPreferenceBinding) (setting.WorkspaceOverrides, error) {
	if err := setting.ValidateProjectToolDecisionEdits(edits); err != nil {
		return setting.WorkspaceOverrides{}, err
	}
	if err := setting.ValidateEdits(setting.App{}, edits); err != nil {
		return setting.WorkspaceOverrides{}, err
	}
	if err := setting.ValidateAnalysisEdits(setting.ScopeWorkspace, edits); err != nil {
		return setting.WorkspaceOverrides{}, err
	}
	if err := setting.ValidatePredictionEdits(setting.ScopeWorkspace, edits); err != nil {
		return setting.WorkspaceOverrides{}, err
	}
	database.settingsWriteMu.Lock()
	defer database.settingsWriteMu.Unlock()
	ctx := context.Background()
	tx, err := database.DB.BeginTx(ctx, nil)
	if err != nil {
		return setting.WorkspaceOverrides{}, err
	}
	defer func() { _ = tx.Rollback() }()
	currentOverrides, err := loadWorkspaceOverridesTx(ctx, tx, defaults)
	if err != nil {
		return setting.WorkspaceOverrides{}, err
	}
	storedCapabilities := append(json.RawMessage(nil), currentOverrides.Capabilities...)
	// Ordinary sparse editing canonicalizes JSON objects. A neighbor save must
	// never repair malformed authority data into a grant. Preserve its original
	// bytes unless this exact transaction explicitly changes prediction.
	storedPrediction := currentOverrides.Prediction
	if len(predictionEdits(edits)) != 0 && len(storedPrediction) != 0 {
		if _, err := setting.DecodeWorkspacePrediction(storedPrediction); err != nil {
			// An explicit whole-category reset/replacement can repair a corrupt
			// choice; leaf patches cannot silently resolve ambiguous members.
			replaces := false
			for _, edit := range edits {
				replaces = replaces || len(edit.Path) == 1 && edit.Path[0] == "prediction"
			}
			if !replaces {
				return setting.WorkspaceOverrides{}, fmt.Errorf("reset invalid workspace prediction settings before changing individual fields")
			}
		}
	}
	if err := setting.ApplyEdits(&currentOverrides, edits); err != nil {
		return setting.WorkspaceOverrides{}, err
	}
	if len(predictionEdits(edits)) == 0 {
		currentOverrides.Prediction = storedPrediction
	}
	if len(analysisEdits(edits)) != 0 {
		currentOverrides.AnalysisRootIdentity = ""
		if setting.MergeAppWorkspace(defaults, currentOverrides).Analysis.Trusted() {
			if analysisRoot == "" {
				return setting.WorkspaceOverrides{}, fmt.Errorf("workspace Trust grant has no owned native directory identity")
			}
			currentOverrides.AnalysisRootIdentity = analysisRoot
		}
	}
	if err := bindPredictionPreferences(&currentOverrides, edits, prediction, analysisRoot); err != nil {
		return setting.WorkspaceOverrides{}, err
	}
	encoded, err := setting.MarshalWorkspaceOverridesPreservingToolDecisions(currentOverrides, storedCapabilities)
	if err != nil {
		return setting.WorkspaceOverrides{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspace_metadata(key,value,updated_at) VALUES(?,?,unixepoch()*1000000000)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at
	`, workspaceSettingsKey, string(encoded)); err != nil {
		return setting.WorkspaceOverrides{}, err
	}
	if err := tx.Commit(); err != nil {
		return setting.WorkspaceOverrides{}, err
	}
	database.publishSettingsRow(string(encoded))
	if err := json.Unmarshal(encoded, &currentOverrides); err != nil {
		return setting.WorkspaceOverrides{}, err
	}
	return currentOverrides, nil
}

// ApplyPreferencesSave publishes a successful command snapshot, with no file or
// database reads. The frontend rejects stale workspace/overlay generations first.
func (w *Workspace) ApplyPreferencesSave(result PreferencesSaveResult) {
	if result.Err != nil {
		return
	}
	if result.Scope == setting.ScopeApplication {
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
	if result.history != nil {
		w.LocalHistory = result.history
		w.HistoryService = NewLocalHistoryService(w.RootPath, result.history, w.Logger)
		w.HistoryService.SetEventBus(w.eventBus)
		for _, s := range w.Sessions {
			if s != nil && s.backend != nil {
				w.bindSessionHistory(s.backend)
			}
		}
	}
	// Re-read current runtime demand: a project attachment may have arrived
	// while the preference worker was writing its captured choices.
	w.applyEffectiveSettings()
}
