package app

import (
	"encoding/json"
	"net"
	"os"
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

// This reaches the actual application disk owner through framed core RPC. A
// mocked preferences callback cannot catch Application/workspace map aliasing.
func TestSettingsLayerIsolationRealCoreSaveAndRestart(t *testing.T) {
	defer setTestHome(t, t.TempDir())()
	agents := session.DefaultAgentsConfig()
	p := agents.Profiles[agents.ActiveProfile]
	p.Categories[session.CategoryDefault] = session.CategoryConfig{AccountID: "test", Model: "app-main", ThinkingEffort: "high"}
	agents.Profiles[agents.ActiveProfile] = p
	file := FileConfig{Agents: &agents, Accounts: map[string]provider.Account{"test": {ID: "test", Label: "Test", ProviderID: "openai", APIKey: "fixture-not-a-secret", Connection: provider.DefaultTemplates()["openai"]}}}
	if err := SaveFileConfig(file); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	start := func() (*core.Runtime, *coreclient.Client, string) {
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
		client, err := coreclient.Dial(t.Context(), coreclient.SingleCarrier(wire), "settings-layers-test")
		if err != nil {
			t.Fatal(err)
		}
		var out coreprotocol.AttachResult
		if err := client.Call(t.Context(), "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &out); err != nil {
			t.Fatal(err)
		}
		return runtime, client, out.WorkspaceID
	}
	runtime, client, id := start()
	fetch := func() coreprotocol.SettingsFetchResult {
		var out coreprotocol.SettingsFetchResult
		if err := client.Call(t.Context(), id, coreprotocol.SettingsFetch, &coreprotocol.Empty{}, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := fetch()
	first := types.CloneAppSettings(before.Application)
	first.LSP.UpdateDebounceMS = 450
	if err := client.Call(t.Context(), id, coreprotocol.ApplicationPreferencesSave, &coreprotocol.ApplicationPreferencesRequest{Edits: types.SettingsEdits(before.Application, first), ProxyURL: coreprotocol.SecretString{Keep: true}}, &coreprotocol.ApplicationPreferencesResult{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["lsp"]) != `{"update_debounce_ms":450}` {
		t.Fatalf("one field expanded into defaults: %s", raw["lsp"])
	}
	before = fetch()
	if !before.Application.LSP.Enabled || before.Application.LSP.UpdateDebounceMS != 450 {
		t.Fatal("sparse Application LSP lost inherited neighbors")
	}
	app := types.CloneAppSettings(before.Application)
	app.Agents.Categories["default"] = types.CategorySettings{AccountID: "test", Model: "app-next", ThinkingEffort: "high"}
	if err := client.Call(t.Context(), id, coreprotocol.ApplicationPreferencesSave, &coreprotocol.ApplicationPreferencesRequest{Edits: types.SettingsEdits(before.Application, app), ProxyURL: coreprotocol.SecretString{Keep: true}}, &coreprotocol.ApplicationPreferencesResult{}); err != nil {
		t.Fatal(err)
	}
	if err := client.Call(t.Context(), id, coreprotocol.WorkspaceOverridesSet, &coreprotocol.WorkspacePreferencesRequest{Edits: []types.SettingEdit{{Path: []string{"lsp", "enabled"}, After: json.RawMessage("false")}}}, &coreprotocol.WorkspacePreferencesResult{}); err != nil {
		t.Fatal(err)
	}
	current := fetch()
	if len(current.Overrides.Agents) != 0 || current.Effective.Agents.Categories["default"].Model != "app-next" || current.Application.Agents.Categories["default"].Model != "app-next" {
		t.Fatal("LSP-only edit polluted agents or Application")
	}
	if err := client.Call(t.Context(), id, coreprotocol.WorkspaceOverridesSet, &coreprotocol.WorkspacePreferencesRequest{Edits: []types.SettingEdit{{Path: []string{"agents", "Categories", "default", "Model"}, After: json.RawMessage(`"workspace-main"`)}}}, &coreprotocol.WorkspacePreferencesResult{}); err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	runtime, client, id = start()
	defer func() { _ = client.Close(); _ = runtime.Close() }()
	current = fetch()
	if current.Application.Agents.Categories["default"].Model != "app-next" || current.Effective.Agents.Categories["default"].Model != "workspace-main" {
		t.Fatal("cold restart mixed Application and workspace")
	}
	next := types.CloneAppSettings(current.Application)
	next.Editor.TabSize = 7
	if err := client.Call(t.Context(), id, coreprotocol.ApplicationPreferencesSave, &coreprotocol.ApplicationPreferencesRequest{Edits: types.SettingsEdits(current.Application, next), ProxyURL: coreprotocol.SecretString{Keep: true}}, &coreprotocol.ApplicationPreferencesResult{}); err != nil {
		t.Fatal("fresh Application save with workspace override", err)
	}
	durable := LoadFileConfig()
	if durable.Agents.Profiles[durable.Agents.ActiveProfile].Categories["researcher"].Model != "" {
		t.Fatal("unrelated preference save pinned an inherited role")
	}
}
