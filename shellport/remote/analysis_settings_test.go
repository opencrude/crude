package remote

import (
	"encoding/json"
	"testing"

	"crude/coreprotocol"
	"crude/types"
)

func TestRemoteEffectiveSettingsPreserveNonInheritedWorkspaceTrust(t *testing.T) {
	w := &Workspace{}
	w.settings = &settingsReplica{w: w}
	application := types.AppSettings{Analysis: types.DefaultWorkspaceAnalysisSettings()}
	effective := types.MergeAppWorkspace(application, types.WorkspaceOverrides{
		Analysis: json.RawMessage(`{"trust":"project_execution_allowed"}`),
	})
	w.settings.install(settingsSnapshot{fetch: coreprotocol.SettingsFetchResult{
		Application: application, Effective: effective,
	}})
	if got := w.LoadAppSettings(); got.Analysis.Trust != types.AnalysisProjectExecutionAllowed {
		t.Fatal("replica re-merged an already-effective workspace choice as application defaults", got.Analysis)
	}
	if got := w.AppDefaults(); got.Analysis.Trust != types.AnalysisStaticOnly {
		t.Fatal("application observation exposed a workspace grant", got.Analysis)
	}
}
