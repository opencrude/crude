package workspace

import (
	"encoding/json"
	"os"
	"testing"

	"crude/fileio"
	"crude/provider"
	"crude/session"
	"crude/types"
)

func predictionPreferenceFixture(t *testing.T) (*Workspace, string) {
	t.Helper()
	w, root := analysisPreferenceFixture(t)
	account := provider.Account{
		ID: "prediction-fixture", ProviderID: "openai", APIKey: "fixture-only",
		Connection: provider.Template{Name: "Local fixture", ProviderURL: "http://127.0.0.1:1", APIStandard: "openai", AuthType: provider.AuthBearer},
	}
	accounts := map[string]provider.Account{account.ID: account}
	w.sessionCfg = &session.Config{
		Accounts: accounts, AccountManager: provider.NewAccountManager(accounts, nil),
		Agents: session.AgentsConfig{ActiveProfile: "prediction", Profiles: map[string]session.ProfileConfig{
			"prediction": {Categories: map[string]session.CategoryConfig{
				session.CategoryAeron: {AccountID: account.ID, Model: "literal-model", ModelMaxOutput: 32},
			}},
		}},
	}
	if _, err := w.sessionCfg.AeronPredictionCompleter(); err != nil {
		t.Fatalf("fixture did not prepare its local account: %v", err)
	}
	return w, root
}

func predictionGrantEdit() types.SettingEdit {
	return types.SettingEdit{Path: []string{"prediction"}, After: json.RawMessage(`{"mode":"light","hourly_token_limit":4200,"protocol":"chat","endpoint_path":""}`)}
}

func TestPredictionPreferenceGrantBindsRootConnectionAndExactChoice(t *testing.T) {
	w, root := predictionPreferenceFixture(t)
	if w.LoadAppSettings().Prediction.Mode != types.PredictionOff {
		t.Fatal("unconfigured workspace had automatic source egress")
	}
	result := applyAnalysisPreference(t, w, predictionGrantEdit())
	actual, err := fileio.DirectoryIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := w.sessionCfg.AeronPredictionCompleter()
	if err != nil {
		t.Fatal(err)
	}
	if result.Overrides.PredictionRootIdentity != actual ||
		result.Overrides.PredictionConnectionIdentity != connection.ConnectionIdentity ||
		result.Overrides.PredictionGrantID == "" || w.LoadAppSettings().Prediction.Mode != types.PredictionLight ||
		w.LoadAppSettings().Prediction.HourlyTokenLimit != 4200 || w.AppDefaults().Prediction.Mode != types.PredictionOff {
		t.Fatal("explicit consent was not isolated, durably bound and projected", result.Overrides)
	}
	before := result.Overrides
	applyAnalysisPreference(t, w, types.SettingEdit{Path: []string{"editor", "tab_size"}, After: json.RawMessage("7")})
	after := w.LoadWorkspaceOverrides()
	if before.PredictionRootIdentity != after.PredictionRootIdentity ||
		before.PredictionConnectionIdentity != after.PredictionConnectionIdentity ||
		before.PredictionGrantID != after.PredictionGrantID || string(before.Prediction) != string(after.Prediction) {
		t.Fatal("neighbor save rebound prediction consent")
	}
	applyAnalysisPreference(t, w, types.SettingEdit{Path: []string{"prediction"}})
	off := w.LoadWorkspaceOverrides()
	if len(off.Prediction) != 0 || off.PredictionRootIdentity != "" ||
		off.PredictionConnectionIdentity != "" || off.PredictionGrantID != "" ||
		w.LoadAppSettings().Prediction.Mode != types.PredictionOff || w.LoadAppSettings().Editor.TabSize != 7 {
		t.Fatal("reset retained source egress or modified neighboring preferences", off)
	}
	next := applyAnalysisPreference(t, w, predictionGrantEdit())
	if next.Overrides.PredictionGrantID == before.PredictionGrantID {
		t.Fatal("Off/on revived an old prediction authority generation")
	}
}

func TestPredictionPreferenceInvalidFinalStateCannotPartiallySave(t *testing.T) {
	w, _ := predictionPreferenceFixture(t)
	before := w.WorkspaceDB.GetSetting(workspaceSettingsKey)
	result := w.PreparePreferencesSave(types.SettingsScopeWorkspace, []types.SettingEdit{
		{Path: []string{"prediction", "mode"}, After: json.RawMessage(`"light"`)},
		{Path: []string{"editor", "tab_size"}, After: json.RawMessage("9")},
	})()
	w.ApplyPreferencesSave(result)
	if result.Err == nil || w.WorkspaceDB.GetSetting(workspaceSettingsKey) != before ||
		w.LoadAppSettings().Prediction.Mode != types.PredictionOff || w.LoadAppSettings().Editor.TabSize == 9 {
		t.Fatal("invalid enable partially saved consent or a neighbor", result.Err)
	}
	applyAnalysisPreference(t, w, predictionGrantEdit())
	before = w.WorkspaceDB.GetSetting(workspaceSettingsKey)
	result = w.PreparePreferencesSave(types.SettingsScopeWorkspace, []types.SettingEdit{
		{Path: []string{"prediction", "hourly_token_limit"}},
	})()
	if result.Err == nil || w.WorkspaceDB.GetSetting(workspaceSettingsKey) != before {
		t.Fatal("leaf reset left an enabled choice without an explicit positive cap")
	}
}

func TestPredictionPreferenceOwnerRejectsSnapshotAndGeneratedMetadata(t *testing.T) {
	w, _ := predictionPreferenceFixture(t)
	if err := w.SaveWorkspaceSettings(types.WorkspaceOverrides{
		Prediction:             json.RawMessage(`{"mode":"light","hourly_token_limit":4200}`),
		PredictionRootIdentity: "forged", PredictionConnectionIdentity: "forged", PredictionGrantID: "forged",
	}); err == nil {
		t.Fatal("snapshot operation manufactured source egress consent")
	}
	for _, edit := range []types.SettingEdit{
		{Path: []string{"prediction_root_identity"}, After: json.RawMessage(`"forged"`)},
		{Path: []string{"prediction_connection_identity"}, After: json.RawMessage(`"forged"`)},
		{Path: []string{"prediction_grant_id"}, After: json.RawMessage(`"forged"`)},
	} {
		before := w.WorkspaceDB.GetSetting(workspaceSettingsKey)
		if got := w.PreparePreferencesSave(types.SettingsScopeWorkspace, []types.SettingEdit{edit})(); got.Err == nil ||
			w.WorkspaceDB.GetSetting(workspaceSettingsKey) != before {
			t.Fatal("caller supplied generated prediction grant material", edit.Path)
		}
	}
	for _, edit := range []types.SettingEdit{
		predictionGrantEdit(), {Path: []string{"prediction"}}, {Path: []string{"prediction", "mode"}, After: json.RawMessage(`"off"`)},
	} {
		if got := w.PreparePreferencesSave(types.SettingsScopeApplication, []types.SettingEdit{edit})(); got.Err == nil {
			t.Fatal("application operation changed workspace consent", edit.Path)
		}
	}
	valid := applyAnalysisPreference(t, w, predictionGrantEdit()).Overrides
	if err := w.SaveWorkspaceSettings(types.WorkspaceOverrides{Editor: json.RawMessage(`{"tab_size":6}`),
		PredictionRootIdentity: "forged", PredictionConnectionIdentity: "forged", PredictionGrantID: "forged"}); err != nil {
		t.Fatal(err)
	}
	after := w.LoadWorkspaceOverrides()
	if after.PredictionRootIdentity != valid.PredictionRootIdentity ||
		after.PredictionConnectionIdentity != valid.PredictionConnectionIdentity || after.PredictionGrantID != valid.PredictionGrantID {
		t.Fatal("entity snapshot replaced the existing exact grant")
	}
}

func TestPredictionPreferenceBindingAndNeighborRollbackTogether(t *testing.T) {
	w, _ := predictionPreferenceFixture(t)
	if _, err := w.WorkspaceDB.DB.Exec(`CREATE TRIGGER reject_prediction BEFORE INSERT ON workspace_metadata
		WHEN NEW.key = 'workspace_settings' BEGIN SELECT RAISE(ABORT, 'prediction rejection'); END`); err != nil {
		t.Fatal(err)
	}
	result := w.PreparePreferencesSave(types.SettingsScopeWorkspace, []types.SettingEdit{
		predictionGrantEdit(), {Path: []string{"editor", "tab_size"}, After: json.RawMessage("9")},
	})()
	w.ApplyPreferencesSave(result)
	if result.Err == nil || !w.LoadWorkspaceOverrides().IsEmpty() || w.LoadAppSettings().Prediction.Mode != types.PredictionOff {
		t.Fatal("failed transaction partially published source egress consent or neighbors", result.Err)
	}
}

func TestPredictionPreferencePreparedSaveKeepsCapturedRootAndStorage(t *testing.T) {
	w, root := predictionPreferenceFixture(t)
	database, observed := w.WorkspaceDB, w.analysisRootIdentity
	work := w.PreparePreferencesSave(types.SettingsScopeWorkspace, []types.SettingEdit{predictionGrantEdit()})
	w.RootPath, w.WorkspaceDB = t.TempDir(), openTestWorkspaceDB(t)
	result := work()
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	rootIdentity, err := fileio.DirectoryIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Overrides.PredictionRootIdentity != rootIdentity || !result.PredictionObserved ||
		w.WorkspaceDB.GetSetting(workspaceSettingsKey) != "" || database.GetSetting(workspaceSettingsKey) == "" ||
		w.analysisRootIdentity != observed {
		t.Fatal("late worker followed replacement workspace or published live state before its owner")
	}
}

func TestPredictionPreferenceSamePathReplacementRequiresNewExplicitChoice(t *testing.T) {
	w, root := predictionPreferenceFixture(t)
	before := applyAnalysisPreference(t, w, predictionGrantEdit()).Overrides
	if err := os.Rename(root, root+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	observed, err := fileio.DirectoryIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	if observed == before.PredictionRootIdentity {
		t.Fatal("fixture did not replace the native directory")
	}
	w.analysisRootIdentity = observed // Factory observation on re-open, not a grant.
	applyAnalysisPreference(t, w, types.SettingEdit{Path: []string{"editor", "tab_size"}, After: json.RawMessage("8")})
	if w.LoadAppSettings().Prediction.Mode != types.PredictionOff ||
		w.LoadWorkspaceOverrides().PredictionRootIdentity != before.PredictionRootIdentity {
		t.Fatal("neighbor save rebound a prior directory's source egress consent")
	}
	after := applyAnalysisPreference(t, w, predictionGrantEdit()).Overrides
	if after.PredictionRootIdentity != observed || after.PredictionGrantID == before.PredictionGrantID {
		t.Fatal("explicit choice did not bind a fresh authority to the replacement directory")
	}
}

func TestPredictionPreferenceNeighborDoesNotRepairMalformedAuthority(t *testing.T) {
	w, _ := predictionPreferenceFixture(t)
	overrides := applyAnalysisPreference(t, w, predictionGrantEdit()).Overrides
	overrides.Prediction = json.RawMessage(`{"mode":"off","mode":"light","hourly_token_limit":4200}`)
	encoded, _ := json.Marshal(overrides)
	if err := w.WorkspaceDB.SetSetting(workspaceSettingsKey, string(encoded)); err != nil {
		t.Fatal(err)
	}
	applyAnalysisPreference(t, w, types.SettingEdit{Path: []string{"editor", "tab_size"}, After: json.RawMessage("8")})
	if string(w.LoadWorkspaceOverrides().Prediction) != string(overrides.Prediction) ||
		w.LoadAppSettings().Prediction.Mode != types.PredictionOff {
		t.Fatal("ordinary neighbor save repaired ambiguous data into a source egress grant")
	}
	before := w.WorkspaceDB.GetSetting(workspaceSettingsKey)
	result := w.PreparePreferencesSave(types.SettingsScopeWorkspace, []types.SettingEdit{
		{Path: []string{"prediction", "hourly_token_limit"}, After: json.RawMessage("4300")},
	})()
	if result.Err == nil || w.WorkspaceDB.GetSetting(workspaceSettingsKey) != before {
		t.Fatal("leaf patch silently resolved an ambiguous authority object")
	}
	applyAnalysisPreference(t, w, types.SettingEdit{Path: []string{"prediction"}})
	if w.LoadWorkspaceOverrides().Prediction != nil || w.LoadWorkspaceOverrides().PredictionGrantID != "" {
		t.Fatal("explicit reset could not retire invalid stored authority")
	}
}

func TestPredictionPreferenceRevokedConnectionAndPrivateManagerCannotGrant(t *testing.T) {
	for _, change := range []string{"removed", "transport", "private-manager"} {
		t.Run(change, func(t *testing.T) {
			w, _ := predictionPreferenceFixture(t)
			if change == "private-manager" {
				w.sessionCfg.AccountManager = nil
			}
			work := w.PreparePreferencesSave(types.SettingsScopeWorkspace, []types.SettingEdit{predictionGrantEdit()})
			switch change {
			case "removed":
				w.sessionCfg.AccountManager.Replace(nil)
			case "transport":
				accounts := w.sessionCfg.AccountManager.Snapshot()
				account := accounts["prediction-fixture"]
				account.Connection.Headers = map[string]string{"X-New": "another connection"}
				accounts[account.ID] = account
				w.sessionCfg.AccountManager.Replace(accounts)
			}
			result := work()
			if result.Err == nil || w.WorkspaceDB.GetSetting(workspaceSettingsKey) != "" {
				t.Fatal("unobservable or revoked account granted source egress", result.Err)
			}
		})
	}
}

// Source-egress consent and project-execution Trust are separate authorities,
// even though both are bound to the same observed native directory.
func TestPredictionPreferenceAuthorityResetsAreIndependent(t *testing.T) {
	for _, order := range []string{"trust-first", "prediction-first"} {
		t.Run(order, func(t *testing.T) {
			w, _ := predictionPreferenceFixture(t)
			applyAnalysisPreference(t, w, grantAnalysisEdit())
			before := applyAnalysisPreference(t, w, predictionGrantEdit()).Overrides
			if order == "trust-first" {
				applyAnalysisPreference(t, w, types.SettingEdit{Path: []string{"analysis"}})
				if w.LoadAppSettings().Analysis.Trust != types.AnalysisUndecided ||
					w.LoadAppSettings().Prediction.Mode != types.PredictionLight ||
					w.LoadWorkspaceOverrides().PredictionGrantID != before.PredictionGrantID {
					t.Fatal("revoking project-execution Trust revoked unrelated source-egress consent")
				}
			} else {
				applyAnalysisPreference(t, w, types.SettingEdit{Path: []string{"prediction"}})
				if w.LoadAppSettings().Prediction.Mode != types.PredictionOff ||
					w.LoadAppSettings().Analysis.Trust != types.AnalysisTrusted ||
					w.LoadWorkspaceOverrides().AnalysisRootIdentity != before.AnalysisRootIdentity {
					t.Fatal("revoking source-egress consent revoked unrelated project-execution Trust")
				}
			}
		})
	}
}
