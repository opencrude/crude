package workspace

import (
	"fmt"

	"crude/insight/governor"
	"crude/types/setting"
	"crude/workspace/fileio"
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
		// Assemble the project's code intelligence. Listing file names runs
		// no project code, so it does not wait for Trust; starting any
		// server still does.
		if w.entry.DeferProjectScan() || w.entry.FileOnly {
			w.deferredAnalysis = make(chan struct{})
		}
		w.startAnalysisWhenNeeded(w.EffectiveAppSettings().LSP.Enabled)
	}
	overrides := w.LoadWorkspaceOverrides()
	if setting.MergeAppWorkspace(w.AppDefaults(), overrides).Analysis.Trusted() {
		if identityErr != nil {
			return fmt.Errorf("could not check this folder; project tools will not run: %w", identityErr)
		}
	}
	return nil
}

// boundWorkspaceSettings derives from committed choices and an owned observation,
// not a pathname hash or an application/session grant.
func boundWorkspaceSettings(defaults setting.App, overrides setting.WorkspaceOverrides, observedRoot string) setting.App {
	effective := setting.MergeAppWorkspace(defaults, overrides)
	effective.Analysis = boundAnalysis(effective.Analysis, overrides, observedRoot)
	if effective.Prediction.Mode != setting.PredictionOff &&
		(observedRoot == "" || overrides.PredictionRootIdentity != observedRoot ||
			overrides.PredictionConnectionIdentity == "" || overrides.PredictionGrantID == "") {
		effective.Prediction = setting.DefaultWorkspacePrediction()
	}
	return effective
}

// boundAnalysis keeps Trust only for the folder it was granted to.
func boundAnalysis(analysis setting.WorkspaceAnalysis, overrides setting.WorkspaceOverrides, observedRoot string) setting.WorkspaceAnalysis {
	if analysis.Trusted() &&
		(observedRoot == "" || overrides.AnalysisRootIdentity == "" || observedRoot != overrides.AnalysisRootIdentity) {
		return setting.DefaultWorkspaceAnalysis()
	}
	return analysis
}

func (w *Workspace) publishAnalysisPolicy() {
	if w.LSPMgr == nil {
		return
	}
	overrides := w.LoadWorkspaceOverrides()
	policy := boundWorkspaceSettings(w.AppDefaults(), overrides, w.analysisRootIdentity).Analysis
	w.LSPMgr.SetAnalysisPolicy(policy, overrides.AnalysisRootIdentity)
}

func analysisEdits(edits []setting.Edit) []setting.Edit {
	var result []setting.Edit
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
func prepareAnalysisBinding(root string, edits []setting.Edit) (string, bool, error) {
	choices := analysisEdits(edits)
	if len(choices) == 0 {
		return "", false, nil
	}
	var draft setting.WorkspaceOverrides
	if err := setting.ApplyEdits(&draft, choices); err != nil {
		return "", true, err
	}
	if !setting.MergeAppWorkspace(setting.App{}, draft).Analysis.Trusted() {
		return "", true, nil
	}
	identity, err := fileio.DirectoryIdentity(root)
	if err != nil {
		return "", true, fmt.Errorf("could not trust this folder. Check the folder path and permissions, then try again: %w", err)
	}
	return identity, true, nil
}

// AnalysisDemand is nil in ordinary projects. A deferred actor closes it when
// its effective settings first request project-wide language analysis.
func (w *Workspace) AnalysisDemand() <-chan struct{} { return w.deferredAnalysis }

func (w *Workspace) startAnalysisWhenNeeded(enabled bool) {
	if w.analysisActive || w.deferredAnalysis != nil && !enabled {
		return
	}
	w.analysisActive = true
	if w.deferredAnalysis != nil {
		close(w.deferredAnalysis)
	}
	w.LSPMgr.RefreshAssembly()
}
