package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/insight/governor"
	"crude/types/setting"
	"crude/workspace/fileio"
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

func applyAnalysisPreference(t *testing.T, w *Workspace, edits ...setting.Edit) PreferencesSaveResult {
	t.Helper()
	work := w.PreparePreferencesSave(setting.ScopeWorkspace, edits)
	result := work()
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	w.ApplyPreferencesSave(result)
	return result
}

func grantAnalysisEdit() setting.Edit {
	return setting.Edit{Path: []string{"analysis", "trust"}, After: json.RawMessage(`"trusted"`)}
}

func TestAnalysisPreferenceGrantAndResetOwnTheNativeBinding(t *testing.T) {
	w, root := analysisPreferenceFixture(t)
	if w.LoadAppSettings().Analysis.Trust != setting.AnalysisUndecided {
		t.Fatal("an unconfigured workspace acquired execution trust")
	}
	result := applyAnalysisPreference(t, w, grantAnalysisEdit())
	actual, err := fileio.DirectoryIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Overrides.AnalysisRootIdentity != actual || w.LoadWorkspaceOverrides().AnalysisRootIdentity != actual ||
		w.LoadAppSettings().Analysis.Trust != setting.AnalysisTrusted {
		t.Fatal("workspace grant did not atomically bind to the native root", result.Overrides.AnalysisRootIdentity)
	}
	if w.AppDefaults().Analysis.Trust != setting.AnalysisUndecided {
		t.Fatal("workspace grant escaped to application defaults")
	}
	// Code Intelligence reset removes the choice and its owner metadata together,
	// while Language Servers and unrelated preferences stay separately owned.
	applyAnalysisPreference(t, w, setting.Edit{Path: []string{"editor", "tab_size"}, After: json.RawMessage("7")})
	applyAnalysisPreference(t, w, setting.Edit{Path: []string{"analysis", "trust"}})
	saved := w.LoadWorkspaceOverrides()
	if len(saved.Analysis) != 0 || saved.AnalysisRootIdentity != "" ||
		w.LoadAppSettings().Analysis.Trust != setting.AnalysisUndecided || w.LoadAppSettings().Editor.TabSize != 7 {
		t.Fatal("reset retained trust/binding or lost unrelated explicit work", saved)
	}
}

func TestAnalysisRestrictedChoiceDoesNotBindIdentity(t *testing.T) {
	w, _ := analysisPreferenceFixture(t)
	applyAnalysisPreference(t, w, grantAnalysisEdit())
	applyAnalysisPreference(t, w, setting.Edit{Path: []string{"analysis", "trust"}, After: json.RawMessage(`"restricted"`)})
	saved := w.LoadWorkspaceOverrides()
	if string(saved.Analysis) != `{"trust":"restricted"}` || saved.AnalysisRootIdentity != "" ||
		w.LoadAppSettings().Analysis.DecisionNeeded() || w.LoadAppSettings().Analysis.Trusted() {
		t.Fatalf("restricted choice lost or retained identity: %+v", saved)
	}
}

func TestAnalysisTrustedAskTrustedRebindsOnlyTheNewChoice(t *testing.T) {
	for _, replaced := range []bool{false, true} {
		t.Run(map[bool]string{false: "same-folder", true: "replacement-folder"}[replaced], func(t *testing.T) {
			w, root := analysisPreferenceFixture(t)
			applyAnalysisPreference(t, w, grantAnalysisEdit(),
				setting.Edit{Path: []string{"editor", "tab_size"}, After: json.RawMessage("7")})
			original := w.LoadWorkspaceOverrides().AnalysisRootIdentity
			if original == "" {
				t.Fatal("initial Trusted choice has no native binding")
			}

			result := applyAnalysisPreference(t, w, setting.Edit{Path: []string{"analysis", "trust"}})
			if !result.Effective.Analysis.DecisionNeeded() || result.Effective.Analysis.Trusted() ||
				result.Overrides.AnalysisRootIdentity != "" || w.LSPMgr.AnalysisPolicy().Trusted() {
				t.Fatal("Ask retained project execution or its old binding", result)
			}
			// Inspect the durable row, not only the owner's read projection. Ask
			// removes the choice and metadata rather than storing an empty choice.
			var encoded string
			if err := w.WorkspaceDB.DB.QueryRow("SELECT value FROM workspace_metadata WHERE key = ?", workspaceSettingsKey).Scan(&encoded); err != nil {
				t.Fatal(err)
			}
			var stored map[string]json.RawMessage
			if err := json.Unmarshal([]byte(encoded), &stored); err != nil {
				t.Fatal(err)
			}
			if _, present := stored["analysis"]; present {
				t.Fatal("Ask persisted an analysis decision", encoded)
			}
			if _, present := stored["analysis_root_identity"]; present {
				t.Fatal("Ask persisted the old native binding", encoded)
			}

			if replaced {
				if err := os.Rename(root, root+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
			}
			actual, err := fileio.DirectoryIdentity(root)
			if err != nil {
				t.Fatal(err)
			}
			if replaced && actual == original {
				t.Fatal("replacement fixture reused the original native directory")
			}
			// A neighboring save after replacement must not turn Ask back into
			// Trusted or restore the cleared identity.
			applyAnalysisPreference(t, w, setting.Edit{Path: []string{"ui", "sidebar_width"}, After: json.RawMessage("42")})
			if !w.LoadAppSettings().Analysis.DecisionNeeded() || w.LoadWorkspaceOverrides().AnalysisRootIdentity != "" {
				t.Fatal("a neighboring save revived the old Trusted choice")
			}
			result = applyAnalysisPreference(t, w, grantAnalysisEdit())
			if !result.Effective.Analysis.Trusted() || !w.LSPMgr.AnalysisPolicy().Trusted() ||
				result.Overrides.AnalysisRootIdentity != actual ||
				w.LoadWorkspaceOverrides().AnalysisRootIdentity != actual ||
				w.LoadAppSettings().Editor.TabSize != 7 || w.LoadAppSettings().UI.SidebarWidth != 42 {
				t.Fatal("the new Trusted choice lost its current root or neighboring preferences", result)
			}
		})
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
	if err != nil ||
		reopened.LoadAppSettings().Analysis.Trust != setting.AnalysisUndecided {
		t.Fatal("same pathname silently inherited a replaced directory's grant", err)
	}
	// Neither an ordinary application update nor a workspace neighbor save can
	// observe a fresh identity and thereby relabel the old accepted choice.
	reopened.SetAppDefaults(setting.App{Analysis: setting.WorkspaceAnalysis{Trust: setting.AnalysisTrusted}})
	applyAnalysisPreference(t, reopened, setting.Edit{Path: []string{"editor", "tab_size"}, After: json.RawMessage("8")})
	if reopened.LoadWorkspaceOverrides().AnalysisRootIdentity != original ||
		reopened.LoadAppSettings().Analysis.Trust != setting.AnalysisUndecided {
		t.Fatal("an unrelated save rebound execution trust")
	}
	applyAnalysisPreference(t, reopened, grantAnalysisEdit())
	if reopened.LoadWorkspaceOverrides().AnalysisRootIdentity == original ||
		reopened.LoadAppSettings().Analysis.Trust != setting.AnalysisTrusted {
		t.Fatal("an explicit new human choice did not bind to the replacement root")
	}
}

func TestAnalysisGrantCannotBeForgedBySnapshotOrMetadataEdits(t *testing.T) {
	w, _ := analysisPreferenceFixture(t)
	if err := w.SaveWorkspaceSettings(setting.WorkspaceOverrides{
		Analysis: json.RawMessage(`{"trust":"trusted"}`), AnalysisRootIdentity: "forged",
	}); err == nil {
		t.Fatal("full-snapshot entity save created a workspace execution grant")
	}
	for _, edit := range []setting.Edit{
		{Path: []string{"analysis_root_identity"}, After: json.RawMessage(`"forged"`)},
		{Path: []string{"analysis", "root_identity"}, After: json.RawMessage(`"forged"`)},
	} {
		if err := setting.ValidateEdits(setting.App{}, []setting.Edit{edit}); err == nil {
			t.Fatal("editable settings schema admitted owner-only binding metadata", edit.Path)
		}
		before := w.WorkspaceDB.Setting(workspaceSettingsKey)
		result := w.PreparePreferencesSave(setting.ScopeWorkspace, []setting.Edit{edit})()
		if result.Err == nil || w.WorkspaceDB.Setting(workspaceSettingsKey) != before {
			t.Fatal("workspace preference owner accepted caller-authored binding metadata", edit.Path, result.Err)
		}
	}
	applyAnalysisPreference(t, w, grantAnalysisEdit())
	before := w.LoadWorkspaceOverrides()
	if err := w.SaveWorkspaceSettings(setting.WorkspaceOverrides{Editor: json.RawMessage(`{"tab_size":6}`), AnalysisRootIdentity: "forged"}); err != nil {
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
	result := w.PreparePreferencesSave(setting.ScopeWorkspace, []setting.Edit{
		grantAnalysisEdit(), {Path: []string{"editor", "tab_size"}, After: json.RawMessage("9")},
	})()
	w.ApplyPreferencesSave(result)
	if result.Err == nil || !strings.Contains(result.Err.Error(), "analysis storage rejection") {
		t.Fatal("grant save did not preserve storage failure", result.Err)
	}
	if overrides := w.LoadWorkspaceOverrides(); !overrides.IsEmpty() || overrides.AnalysisRootIdentity != "" ||
		w.LoadAppSettings().Analysis.Trust != setting.AnalysisUndecided {
		t.Fatal("failed transaction partially published trust or neighbors", overrides)
	}
}

func TestAnalysisPreparedGrantStaysWithCapturedRootAndStorage(t *testing.T) {
	w, root := analysisPreferenceFixture(t)
	originalDB := w.WorkspaceDB
	originalObservation := w.analysisRootIdentity
	work := w.PreparePreferencesSave(setting.ScopeWorkspace, []setting.Edit{grantAnalysisEdit()})
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
	if result.AnalysisRootIdentity != actual || otherDB.Setting(workspaceSettingsKey) != "" ||
		!strings.Contains(originalDB.Setting(workspaceSettingsKey), actual) {
		t.Fatal("late preference worker redirected a grant to its replacement workspace")
	}
	if w.analysisRootIdentity != originalObservation || w.LSPMgr.AnalysisPolicy().Trust != setting.AnalysisUndecided {
		t.Fatal("preference I/O mutated the live workspace before its owning publication")
	}
}

// UI ticks read Trust through EffectiveAnalysisSettings. It must agree with
// the complete merge in every state, including a grant for another folder.
func TestEffectiveAnalysisSettingsMatchesTheCompleteMerge(t *testing.T) {
	w, _ := analysisPreferenceFixture(t)
	check := func(state string) {
		t.Helper()
		if got, want := w.EffectiveAnalysisSettings(), w.EffectiveAppSettings().Analysis; got != want {
			t.Fatalf("%s: analysis read %+v, complete merge %+v", state, got, want)
		}
	}
	check("undecided")
	applyAnalysisPreference(t, w, grantAnalysisEdit())
	check("trusted")
	if !w.EffectiveAnalysisSettings().Trusted() {
		t.Fatal("trusted folder read as untrusted")
	}
	w.analysisRootIdentity = "another-folder"
	check("trusted elsewhere")
	if w.EffectiveAnalysisSettings().Trusted() {
		t.Fatal("a grant for another folder was honored")
	}
	applyAnalysisPreference(t, w, setting.Edit{Path: []string{"analysis", "trust"}, After: json.RawMessage(`"restricted"`)})
	check("restricted")
}
