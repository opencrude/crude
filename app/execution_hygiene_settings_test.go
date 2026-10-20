package app

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"

	"crude/core"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/provider"
	"crude/session"
	"crude/types"
	"crude/workspace"
)

func TestExecutionHygienePreferencesPersistAndInheritAcrossRestart(t *testing.T) {
	defer setTestHome(t, t.TempDir())()
	agents := session.DefaultAgentsConfig()
	profile := agents.Profiles[agents.ActiveProfile]
	profile.Categories[session.CategoryDefault] = session.CategoryConfig{AccountID: "fixture", Model: "fixture-model"}
	agents.Profiles[agents.ActiveProfile] = profile
	if err := SaveFileConfig(FileConfig{Agents: &agents, Accounts: map[string]provider.Account{"fixture": {ID: "fixture", ProviderID: "openai", Connection: provider.DefaultTemplates()["openai"]}}}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	start := func() (*core.Runtime, *coreclient.Client, string) {
		t.Helper()
		application, err := New(Opts{FC: LoadFileConfig()})
		if err != nil {
			t.Fatal(err)
		}
		runtime, err := core.New(func(root string) (*workspace.Workspace, error) { return application.newWorkspace(root, &appLogger{}) }, filepath.Join(t.TempDir(), "receipts.db"))
		if err != nil {
			t.Fatal(err)
		}
		server, wire := net.Pipe()
		go func() { _ = core.ServeConnection(t.Context(), runtime, server) }()
		client, err := coreclient.Dial(t.Context(), wire, "hygiene-settings")
		if err != nil {
			t.Fatal(err)
		}
		var attached coreprotocol.AttachResult
		if err := client.Call(t.Context(), "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &attached); err != nil {
			t.Fatal(err)
		}
		return runtime, client, attached.WorkspaceID
	}
	runtime, client, id := start()
	fetch := func() coreprotocol.SettingsFetchResult {
		t.Helper()
		var got coreprotocol.SettingsFetchResult
		if err := client.Call(t.Context(), id, coreprotocol.SettingsFetch, struct{}{}, &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	before := fetch()
	after := types.CloneAppSettings(before.Application)
	zero := 0
	after.Agents.ExecutionHygiene.RetainedCount = &zero
	if err := client.Call(t.Context(), id, coreprotocol.ApplicationPreferencesSave, coreprotocol.ApplicationPreferencesRequest{Edits: types.SettingsEdits(before.Application, after), ProxyURL: coreprotocol.SecretUpdate[string]{Keep: true}}, &coreprotocol.ApplicationPreferencesResult{}); err != nil {
		t.Fatal(err)
	}
	if err := client.Call(t.Context(), id, coreprotocol.WorkspaceOverridesSet, coreprotocol.WorkspacePreferencesRequest{Edits: []types.SettingEdit{{Path: []string{"agents", "execution_hygiene", "age_hours"}, After: json.RawMessage("48")}}}, &coreprotocol.WorkspacePreferencesResult{}); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	runtime, client, id = start()
	defer func() { _ = client.Close(); _ = runtime.Close() }()
	got := fetch()
	appEnabled, appCount, appAge, appStorage := got.Application.Agents.ExecutionHygiene.Values()
	enabled, count, age, storage := got.Effective.Agents.ExecutionHygiene.Values()
	if !appEnabled || appCount != 0 || appAge != 24 || appStorage != 1024 || !enabled || count != 0 || age != 48 || storage != 1024 {
		t.Fatal("hygiene choices did not round trip/inherit", got.Application.Agents.ExecutionHygiene, got.Effective.Agents.ExecutionHygiene)
	}
	file := LoadFileConfig()
	if file.Agents.ExecutionHygiene.RetainedCount == nil || *file.Agents.ExecutionHygiene.RetainedCount != 0 || file.Agents.ExecutionHygiene.AgeHours != nil {
		t.Fatal("workspace choice polluted application or explicit zero lost")
	}
}
