package remote

import (
	"encoding/json"
	"fmt"
	"testing"

	"crude/core/protocol"
	"crude/types"
)

func TestRemoteEffectiveSettingsPreserveNonInheritedWorkspaceTrust(t *testing.T) {
	w := &Workspace{}
	w.settings = &settingsReplica{w: w}
	application := types.AppSettings{Analysis: types.DefaultWorkspaceAnalysisSettings()}
	effective := types.MergeAppWorkspace(application, types.WorkspaceOverrides{
		Analysis: json.RawMessage(`{"trust":"trusted"}`),
	})
	w.settings.install(settingsSnapshot{fetch: coreprotocol.SettingsFetchResult{
		Application: application, Effective: effective,
	}})
	if got := w.LoadAppSettings(); got.Analysis.Trust != types.AnalysisTrusted {
		t.Fatal("replica re-merged an already-effective workspace choice as application defaults", got.Analysis)
	}
	if got := w.AppDefaults(); got.Analysis.Trust != types.AnalysisUndecided {
		t.Fatal("application observation exposed a workspace grant", got.Analysis)
	}
	if got := w.AnalysisSettings(); got != w.LoadAppSettings().Analysis {
		t.Fatal("tick read differs from the effective settings", got)
	}
	off := false
	effective.Analysis.Trust = types.AnalysisRestricted
	effective.UI.DesktopNotifications = types.DesktopNotificationSettings{AwayMinutes: 7, Enabled: &off}
	w.settings.install(settingsSnapshot{fetch: coreprotocol.SettingsFetchResult{Application: application, Effective: effective}})
	if away, active := w.DesktopPresencePreferences(); away != 7 || active || w.AnalysisSettings().Trusted() {
		t.Fatal("passive reads retained an old preference or trust grant", away, active)
	}
}

func BenchmarkTickPreferenceReads(b *testing.B) {
	w := &Workspace{}
	w.settings = &settingsReplica{w: w}
	var effective types.AppSettings
	types.ApplyAppDefaults(&effective)
	accounts := map[string]coreprotocol.AccountView{}
	for i := range 12 {
		id := fmt.Sprint(i)
		accounts[id] = coreprotocol.AccountView{ID: id, APIKeyPresent: true}
	}
	w.settings.install(settingsSnapshot{fetch: coreprotocol.SettingsFetchResult{Accounts: accounts, Effective: effective}})
	b.ReportAllocs()
	for b.Loop() {
		_ = w.AnalysisSettings().Trusted()
		_, _ = w.DesktopPresencePreferences()
		_, _ = w.DesktopPresencePreferences()
	}
}
