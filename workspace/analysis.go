package workspace

import (
	"fmt"

	"crude/fileio"
	"crude/insight/governor"
	"crude/types"
)

// InitializeAnalysisRuntime runs on the retained factory worker, before actor
// publication. The core owns the injected governor. Native metadata is observed
// here, never while a shell/actor is deriving settings.
func (w *Workspace) InitializeAnalysisRuntime(g *governor.Governor, workspaceID, generation string) error {
	identity, identityErr := fileio.DirectoryIdentity(w.RootPath)
	w.analysisRootIdentity = identity
	if w.LSPMgr != nil {
		w.LSPMgr.SetWorkspaceRoot(w.RootPath)
		if err := w.LSPMgr.SetAnalysisGovernor(g, workspaceID, generation); err != nil {
			return err
		}
		w.publishAnalysisPolicy()
	}
	overrides := w.LoadWorkspaceOverrides()
	if types.MergeAppWorkspace(w.AppDefaults(), overrides).Analysis.Trusted() {
		if identityErr != nil {
			return fmt.Errorf("could not check this folder; project tools will not run: %w", identityErr)
		}
	}
	return nil
}

// boundWorkspaceSettings derives from committed choices and an owned observation,
// not a pathname hash or an application/session grant.
func boundWorkspaceSettings(defaults types.AppSettings, overrides types.WorkspaceOverrides, observedRoot string) types.AppSettings {
	effective := types.MergeAppWorkspace(defaults, overrides)
	if effective.Analysis.Trusted() &&
		(observedRoot == "" || overrides.AnalysisRootIdentity == "" || observedRoot != overrides.AnalysisRootIdentity) {
		effective.Analysis = types.DefaultWorkspaceAnalysisSettings()
	}
	if effective.Prediction.Mode != types.PredictionOff &&
		(observedRoot == "" || overrides.PredictionRootIdentity != observedRoot ||
			overrides.PredictionConnectionIdentity == "" || overrides.PredictionGrantID == "") {
		effective.Prediction = types.DefaultWorkspacePredictionSettings()
	}
	return effective
}

func (w *Workspace) publishAnalysisPolicy() {
	if w.LSPMgr == nil {
		return
	}
	overrides := w.LoadWorkspaceOverrides()
	policy := boundWorkspaceSettings(w.AppDefaults(), overrides, w.analysisRootIdentity).Analysis
	w.LSPMgr.SetAnalysisPolicy(policy, overrides.AnalysisRootIdentity)
}

func analysisEdits(edits []types.SettingEdit) []types.SettingEdit {
	var result []types.SettingEdit
	for _, edit := range edits {
		if len(edit.Path) != 0 && edit.Path[0] == "analysis" {
			result = append(result, edit)
		}
	}
	return result
}

// prepareAnalysisBinding observes a new native binding only for an explicit
// workspace Trust grant. Unrelated saves must never rebind an old grant after
// same-path replacement. Returned I/O runs on the preference worker.
func prepareAnalysisBinding(root string, edits []types.SettingEdit) (string, bool, error) {
	choices := analysisEdits(edits)
	if len(choices) == 0 {
		return "", false, nil
	}
	var draft types.WorkspaceOverrides
	if err := types.ApplySettingsEdits(&draft, choices); err != nil {
		return "", true, err
	}
	if !types.MergeAppWorkspace(types.AppSettings{}, draft).Analysis.Trusted() {
		return "", true, nil
	}
	identity, err := fileio.DirectoryIdentity(root)
	if err != nil {
		return "", true, fmt.Errorf("could not trust this folder. Check the folder path and permissions, then try again: %w", err)
	}
	return identity, true, nil
}
