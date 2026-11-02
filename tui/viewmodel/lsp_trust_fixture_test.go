package viewmodel

import (
	"testing"

	"crude/fileio"
	"crude/insight/governor"
	"crude/types"
	"crude/workspace"
)

// grantSemanticFixture is an explicit human-owned, durable workspace grant.
// Only fixtures whose assertions require live semantic providers call it; shared
// shell/workspace constructors and static-only journeys deliberately do not.
func grantSemanticFixture(t *testing.T, w *workspace.Workspace) {
	t.Helper()
	result := w.PreparePreferencesSave(types.SettingsScopeWorkspace, []types.SettingEdit{{
		Path: []string{"analysis", "trust"}, After: []byte(`"trusted"`),
	}})()
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	w.ApplyPreferencesSave(result)
	if w.LoadWorkspaceOverrides().AnalysisRootIdentity == "" ||
		w.LoadAppSettings().Analysis.Trust != types.AnalysisTrusted {
		t.Fatal("semantic fixture did not persist its explicit native-root Trust grant")
	}
}

func restrictFixture(t testing.TB, w *workspace.Workspace) {
	t.Helper()
	result := w.PreparePreferencesSave(types.SettingsScopeWorkspace, []types.SettingEdit{{
		Path: []string{"analysis", "trust"}, After: []byte(`"restricted"`),
	}})()
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	w.ApplyPreferencesSave(result)
	if w.LoadAppSettings().Analysis.DecisionNeeded() {
		t.Fatal("restricted fixture did not retain its trust decision")
	}
}

func restrictSyntheticFixture(t testing.TB, app *App, root string) {
	t.Helper()
	w := wsOf(app)
	w.RootPath = root
	db, err := workspace.OpenWorkspaceDB(root)
	if err != nil {
		t.Fatal(err)
	}
	w.WorkspaceDB = db
	restrictFixture(t, w)
}

// Standalone manager fixtures have no retained core to supply process capacity.
func admitSemanticManager(t *testing.T, m *workspace.LSPManager, root string) {
	t.Helper()
	g, err := governor.New(governor.Limits{MaxProcesses: 2, MaxWaiting: 8})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetAnalysisGovernor(g, "viewmodel-semantic-fixture", "test-generation"); err != nil {
		t.Fatal(err)
	}
	identity, err := fileio.DirectoryIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	m.SetAnalysisPolicy(types.WorkspaceAnalysisSettings{Trust: types.AnalysisTrusted}, identity)
}
