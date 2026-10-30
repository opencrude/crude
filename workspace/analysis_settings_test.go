package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/fileio"
	"crude/insight/governor"
	"crude/types"
)

func analysisPreferenceFixture(t *testing.T) (*Workspace, string) {
	t.Helper()
	setTestHome(t)
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "project")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	w := &Workspace{RootPath: root, WorkspaceDB: openTestWorkspaceDB(t), Logger: testLogger{}, LSPMgr: NewLSPManager(nil)}
	t.Cleanup(w.LSPMgr.Close)
	g, err := governor.New(governor.Limits{MaxProcesses: 2, MaxWaiting: 4})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.InitializeAnalysisRuntime(g, "test-workspace", "first-generation"); err != nil {
		t.Fatal(err)
	}
	return w, root
}

func applyAnalysisPreference(t *testing.T, w *Workspace, edits ...types.SettingEdit) PreferencesSaveResult {
	t.Helper()
	work := w.PreparePreferencesSave(types.SettingsScopeWorkspace, edits)
	result := work()
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	w.ApplyPreferencesSave(result)
	return result
}

func grantAnalysisEdit() types.SettingEdit {
	return types.SettingEdit{Path: []string{"analysis", "trust"}, After: json.RawMessage(`"project_execution_allowed"`)}
}

func TestAnalysisPreferenceGrantAndResetOwnTheNativeBinding(t *testing.T) {
	w, root := analysisPreferenceFixture(t)
	if w.LoadAppSettings().Analysis.Trust != types.AnalysisStaticOnly {
		t.Fatal("an unconfigured workspace acquired execution trust")
	}
	result := applyAnalysisPreference(t, w, grantAnalysisEdit())
	actual, err := fileio.DirectoryIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Overrides.AnalysisRootIdentity != actual || w.LoadWorkspaceOverrides().AnalysisRootIdentity != actual ||
		w.LoadAppSettings().Analysis.Trust != types.AnalysisProjectExecutionAllowed {
		t.Fatal("workspace grant did not atomically bind to the native root", result.Overrides.AnalysisRootIdentity)
	}
	if w.AppDefaults().Analysis.Trust != types.AnalysisStaticOnly {
		t.Fatal("workspace grant escaped to application defaults")
	}
	// Code Intelligence reset removes the choice and its owner metadata together,
	// while Language Servers and unrelated preferences stay separately owned.
	applyAnalysisPreference(t, w, types.SettingEdit{Path: []string{"editor", "tab_size"}, After: json.RawMessage("7")})
	applyAnalysisPreference(t, w, types.SettingEdit{Path: []string{"analysis", "trust"}})
	saved := w.LoadWorkspaceOverrides()
	if len(saved.Analysis) != 0 || saved.AnalysisRootIdentity != "" ||
		w.LoadAppSettings().Analysis.Trust != types.AnalysisStaticOnly || w.LoadAppSettings().Editor.TabSize != 7 {
		t.Fatal("reset retained trust/binding or lost unrelated explicit work", saved)
	}
}

func TestAnalysisSamePathReplacementNeverInheritsOrRebindsGrant(t *testing.T) {
	w, root := analysisPreferenceFixture(t)
	applyAnalysisPreference(t, w, grantAnalysisEdit())
	original := w.LoadWorkspaceOverrides().AnalysisRootIdentity
	if err := os.Rename(root, root+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	reopened := &Workspace{RootPath: root, WorkspaceDB: w.WorkspaceDB, Logger: testLogger{}, LSPMgr: NewLSPManager(nil)}
	t.Cleanup(reopened.LSPMgr.Close)
	g, _ := governor.New(governor.Limits{MaxProcesses: 2, MaxWaiting: 4})
	err := reopened.InitializeAnalysisRuntime(g, "test-workspace", "second-generation")
	if err == nil || !strings.Contains(err.Error(), "identity changed") ||
		reopened.LoadAppSettings().Analysis.Trust != types.AnalysisStaticOnly {
		t.Fatal("same pathname silently inherited a replaced directory's grant", err)
	}
	// Neither an ordinary application update nor a workspace neighbor save can
	// observe a fresh identity and thereby relabel the old accepted choice.
	reopened.SetAppDefaults(types.AppSettings{Analysis: types.WorkspaceAnalysisSettings{Trust: types.AnalysisProjectExecutionAllowed}})
	applyAnalysisPreference(t, reopened, types.SettingEdit{Path: []string{"editor", "tab_size"}, After: json.RawMessage("8")})
	if reopened.LoadWorkspaceOverrides().AnalysisRootIdentity != original ||
		reopened.LoadAppSettings().Analysis.Trust != types.AnalysisStaticOnly {
		t.Fatal("an unrelated save rebound execution trust")
	}
	applyAnalysisPreference(t, reopened, grantAnalysisEdit())
	if reopened.LoadWorkspaceOverrides().AnalysisRootIdentity == original ||
		reopened.LoadAppSettings().Analysis.Trust != types.AnalysisProjectExecutionAllowed {
		t.Fatal("an explicit new human choice did not bind to the replacement root")
	}
}

func TestAnalysisGrantCannotBeForgedBySnapshotOrMetadataEdits(t *testing.T) {
	w, _ := analysisPreferenceFixture(t)
	if err := w.SaveWorkspaceSettings(types.WorkspaceOverrides{
		Analysis: json.RawMessage(`{"trust":"project_execution_allowed"}`), AnalysisRootIdentity: "forged",
	}); err == nil {
		t.Fatal("full-snapshot entity save created a workspace execution grant")
	}
	for _, edit := range []types.SettingEdit{
		{Path: []string{"analysis_root_identity"}, After: json.RawMessage(`"forged"`)},
		{Path: []string{"analysis", "root_identity"}, After: json.RawMessage(`"forged"`)},
	} {
		if err := types.ValidateSettingEdits(types.AppSettings{}, []types.SettingEdit{edit}); err == nil {
			t.Fatal("editable settings schema admitted owner-only binding metadata", edit.Path)
		}
		before := w.WorkspaceDB.GetSetting(workspaceSettingsKey)
		result := w.PreparePreferencesSave(types.SettingsScopeWorkspace, []types.SettingEdit{edit})()
		if result.Err == nil || w.WorkspaceDB.GetSetting(workspaceSettingsKey) != before {
			t.Fatal("workspace preference owner accepted caller-authored binding metadata", edit.Path, result.Err)
		}
	}
	applyAnalysisPreference(t, w, grantAnalysisEdit())
	before := w.LoadWorkspaceOverrides()
	if err := w.SaveWorkspaceSettings(types.WorkspaceOverrides{Editor: json.RawMessage(`{"tab_size":6}`), AnalysisRootIdentity: "forged"}); err != nil {
		t.Fatal(err)
	}
	after := w.LoadWorkspaceOverrides()
	if after.AnalysisRootIdentity != before.AnalysisRootIdentity || string(after.Analysis) != string(before.Analysis) {
		t.Fatal("entity snapshot erased or replaced the explicit grant binding")
	}
}

func TestAnalysisBindingAndNeighborEditsRollbackTogether(t *testing.T) {
	w, _ := analysisPreferenceFixture(t)
	if _, err := w.WorkspaceDB.DB.Exec(`CREATE TRIGGER reject_analysis BEFORE INSERT ON workspace_metadata
		WHEN NEW.key = 'workspace_settings' BEGIN SELECT RAISE(ABORT, 'analysis storage rejection'); END`); err != nil {
		t.Fatal(err)
	}
	result := w.PreparePreferencesSave(types.SettingsScopeWorkspace, []types.SettingEdit{
		grantAnalysisEdit(), {Path: []string{"editor", "tab_size"}, After: json.RawMessage("9")},
	})()
	w.ApplyPreferencesSave(result)
	if result.Err == nil || !strings.Contains(result.Err.Error(), "analysis storage rejection") {
		t.Fatal("grant save did not preserve storage failure", result.Err)
	}
	if overrides := w.LoadWorkspaceOverrides(); !overrides.IsEmpty() || overrides.AnalysisRootIdentity != "" ||
		w.LoadAppSettings().Analysis.Trust != types.AnalysisStaticOnly {
		t.Fatal("failed transaction partially published trust or neighbors", overrides)
	}
}

func TestAnalysisPreparedGrantStaysWithCapturedRootAndStorage(t *testing.T) {
	w, root := analysisPreferenceFixture(t)
	originalDB := w.WorkspaceDB
	originalObservation := w.analysisRootIdentity
	work := w.PreparePreferencesSave(types.SettingsScopeWorkspace, []types.SettingEdit{grantAnalysisEdit()})
	otherRoot := t.TempDir()
	otherDB := openTestWorkspaceDB(t)
	w.RootPath, w.WorkspaceDB = otherRoot, otherDB
	result := work()
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	actual, err := fileio.DirectoryIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	if result.AnalysisRootIdentity != actual || otherDB.GetSetting(workspaceSettingsKey) != "" ||
		!strings.Contains(originalDB.GetSetting(workspaceSettingsKey), actual) {
		t.Fatal("late preference worker redirected a grant to its replacement workspace")
	}
	if w.analysisRootIdentity != originalObservation || w.LSPMgr.AnalysisPolicy().Trust != types.AnalysisStaticOnly {
		t.Fatal("preference I/O mutated the live workspace before its owning publication")
	}
}
