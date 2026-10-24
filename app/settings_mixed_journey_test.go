package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/core"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/network"
	"crude/provider"
	"crude/session"
	"crude/sessiondb"
	"crude/types"
	captypes "crude/types/capability"
	"crude/workspace"
)

// Exercise the real file owner, workspace transactions, sessions and framed RPC
// in one interleaved journey. No mock save callback can stand in for durability.
func TestSettingsMixedScopesExplicitChoicesRestartAndRepair(t *testing.T) {
	defer setTestHome(t, t.TempDir())()
	oldNetwork := network.Current()
	defer func() { _ = network.ConfigureDefault(oldNetwork) }()
	var proxyRequests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyRequests.Add(1)
		_, _ = io.WriteString(w, "fixture proxy")
	}))
	defer proxy.Close()
	agents := session.DefaultAgentsConfig()
	profile := agents.Profiles[agents.ActiveProfile]
	on := true
	profile.MultiAgent = &on
	profile.Categories["default"] = session.CategoryConfig{AccountID: "fixture", Model: "app-main"}
	agents.Profiles[agents.ActiveProfile] = profile
	config := FileConfig{Agents: &agents, Accounts: map[string]provider.Account{"fixture": {ID: "fixture", Label: "Fixture", ProviderID: "openai", Connection: provider.Template{Name: "Fixture", ProviderURL: "http://127.0.0.1:1/v1", APIStandard: "openai", AuthType: provider.AuthNone}}},
		Capabilities: &captypes.Settings{Connections: map[string]captypes.ConnectionConfig{"fixture:mcp.one": {ID: "fixture:mcp.one", Name: "Fixture MCP", Transport: "http", URL: "http://127.0.0.1:1/mcp", Scope: captypes.ScopeApplication}}},
		Memory:       &types.MemorySettings{AutoRecall: "off", AutoExtract: "off", Consolidation: "off"}, LocalHistory: &types.LocalHistorySettings{Enabled: "off"}}
	if err := SaveFileConfig(config); err != nil {
		t.Fatal(err)
	}
	root, other := t.TempDir(), t.TempDir()
	start := func() (*core.Runtime, *coreclient.Client, *App, string) {
		a, err := New(Opts{FC: LoadFileConfig()})
		if err != nil {
			t.Fatal(err)
		}
		r, err := core.New(func(path string) (*workspace.Workspace, error) { return a.newWorkspace(path, &appLogger{}) }, filepath.Join(t.TempDir(), "receipts.db"))
		if err != nil {
			t.Fatal(err)
		}
		s, c := net.Pipe()
		go func() { _ = core.ServeConnection(t.Context(), r, s) }()
		client, err := coreclient.Dial(t.Context(), coreclient.SingleCarrier(c), "mixed-settings")
		if err != nil {
			t.Fatal(err)
		}
		var attached coreprotocol.AttachResult
		if err := client.Call(t.Context(), "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &attached); err != nil {
			t.Fatal(err)
		}
		return r, client, a, attached.WorkspaceID
	}
	r, client, application, id := start()
	defer func() { _ = client.Close(); _ = r.Close() }()
	call := func(method string, input coreprotocol.Value, output coreprotocol.Target) {
		t.Helper()
		if err := client.Call(t.Context(), id, method, input, output); err != nil {
			t.Fatal(method, err)
		}
	}
	fetch := func() coreprotocol.SettingsFetchResult {
		t.Helper()
		var out coreprotocol.SettingsFetchResult
		call(coreprotocol.SettingsFetch, &coreprotocol.Empty{}, &out)
		return out
	}
	edit := func(path []string, value string) types.SettingEdit {
		return types.SettingEdit{Path: path, After: json.RawMessage(value)}
	}
	var created workspace.NewSessionResult
	call(coreprotocol.NewSession, &coreprotocol.InvocationSessionRequest{}, &created)
	if created.Error != "" {
		t.Fatal(created.Error)
	}
	sid := created.SessionID
	// Keep real, attributed history without making a provider request.
	for _, w := range r.Workspaces() {
		if b := w.BackendSession(sid); b != nil {
			if err := b.AppendWorkspaceHistoryEvent("settings acceptance history", "settings acceptance history"); err != nil {
				t.Fatal(err)
			}
		}
	}
	readSession := func(sessionID string) coreprotocol.SessionSettingsResult {
		t.Helper()
		var out coreprotocol.SessionSettingsResult
		call(coreprotocol.SessionSettingsRead, coreprotocol.SessionSettingsRequest{SessionID: sessionID}, &out)
		return out
	}
	saveSession := func(edits ...types.SettingEdit) {
		t.Helper()
		call(coreprotocol.SessionSettingsSave, &coreprotocol.SessionSettingsMutationRequest{SessionID: sid, Edits: edits}, &coreprotocol.SessionSettingsResult{})
	}
	saveWorkspace := func(edits ...types.SettingEdit) {
		t.Helper()
		call(coreprotocol.WorkspaceOverridesSet, &coreprotocol.WorkspacePreferencesRequest{Edits: edits}, &coreprotocol.WorkspacePreferencesResult{})
	}
	saveApp := func(edits ...types.SettingEdit) {
		t.Helper()
		call(coreprotocol.ApplicationPreferencesSave, &coreprotocol.ApplicationPreferencesRequest{Edits: edits, ProxyURL: coreprotocol.SecretString{Keep: true}}, &coreprotocol.ApplicationPreferencesResult{})
	}
	// Stage a later independent session field before changing any parent scope.
	staged := edit([]string{"overrides", "default", "ThinkingEffort"}, `"high"`)
	saveSession(edit([]string{"overrides", "default", "Model"}, `"session-model"`), edit([]string{"overrides", "default", "context_compaction_percent"}, "63"))
	saveWorkspace(edit([]string{"agents", "Enabled"}, "false"), edit([]string{"editor", "tab_size"}, "8"), edit([]string{"terminal", "scrollback_lines"}, "4000"))
	proxyURL := strings.Replace(proxy.URL, "http://", "http://fixture:private-proxy@", 1)
	call(coreprotocol.ApplicationPreferencesSave, &coreprotocol.ApplicationPreferencesRequest{Edits: []types.SettingEdit{edit([]string{"network", "proxy_mode"}, `"custom"`)}, ProxyURL: coreprotocol.SecretString{Value: &proxyURL}}, &coreprotocol.ApplicationPreferencesResult{})
	response, err := network.NewClient(time.Second).Get("http://settings.invalid/proof")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if proxyRequests.Load() != 1 {
		t.Fatal("saved proxy was not used by the next client")
	}
	call(coreprotocol.CapabilityAction, &coreprotocol.CapabilityActionRequest{Action: captypes.Action{Operation: "disable", Kind: captypes.KindMCP, ID: "fixture:mcp.one", Scope: captypes.ScopeWorkspace}}, &coreprotocol.CapabilityResult{})
	saveApp(edit([]string{"agents", "Categories", "default", "Model"}, `"app-next"`), edit([]string{"ui", "context_help"}, `"expanded"`), edit([]string{"memory", "recall_limit"}, "12"))
	saveSession(staged)
	call(coreprotocol.SessionCapabilityToggle, &coreprotocol.SessionSettingsMutationRequest{SessionID: sid, Capability: &coreprotocol.SessionCapabilityChange{ID: "fixture:mcp.one", Enabled: false}}, &coreprotocol.SessionSettingsResult{})
	assert := func() {
		t.Helper()
		all, s := fetch(), readSession(sid)
		main := s.Effective.Value.Agents.Categories["default"]
		if s.Effective.Value.Model != "session-model" || main.ContextCompactionPercent == nil || *main.ContextCompactionPercent != 63 || main.ThinkingEffort != "high" || s.Effective.Value.Agents.Enabled || s.Effective.MultiAgent != nil {
			t.Fatal("session choices or live inherited delegation lost")
		}
		if all.Application.Agents.Categories["default"].Model != "app-next" || all.Effective.Editor.TabSize != 8 || all.Effective.Terminal.ScrollbackLines != 4000 || all.Application.UI.ContextHelp != "expanded" || all.Application.Memory.RecallLimit != 12 {
			t.Fatal("mixed preferences crossed scopes")
		}
		if !all.Effective.Capabilities.Connections["fixture:mcp.one"].Disabled || all.Application.Capabilities.Connections["fixture:mcp.one"].Disabled || len(s.Effective.DisabledCapabilities) != 1 {
			t.Fatal("MCP registration/session gates crossed scopes")
		}
		if strings.Contains(string(all.Overrides.Agents), "Model") || strings.Contains(string(all.Overrides.Capabilities), "http://") || strings.Contains(fmt.Sprint(all), "private-proxy") {
			t.Fatal("derived model/connection/secret copied into another layer")
		}
		if FileConfigToAppSettings(LoadFileConfig()).Network.ProxyURL != proxyURL {
			t.Fatal("unrelated save replaced proxy credentials")
		}
	}
	assert()
	// A second session follows the parent, not the first session's local choices.
	call(coreprotocol.NewSession, &coreprotocol.InvocationSessionRequest{}, &created)
	if second := readSession(created.SessionID); second.Effective.Value.Model != "app-next" || len(second.Overrides) != 0 || len(second.Effective.DisabledCapabilities) != 0 || second.Effective.Value.Agents.Enabled {
		t.Fatal("session settings leaked into a new session")
	}
	var attachment coreprotocol.AttachResult
	if err := client.Call(t.Context(), "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: other}, &attachment); err != nil {
		t.Fatal(err)
	}
	if err := r.ApplyAppDefaults(t.Context(), application.committedDefaults()); err != nil {
		t.Fatal(err)
	}
	var otherSettings coreprotocol.SettingsFetchResult
	if err := client.Call(t.Context(), attachment.WorkspaceID, coreprotocol.SettingsFetch, &coreprotocol.Empty{}, &otherSettings); err != nil {
		t.Fatal(err)
	}
	if !otherSettings.Overrides.IsEmpty() || !otherSettings.Effective.Agents.Enabled || otherSettings.Effective.Editor.TabSize != 4 || otherSettings.Effective.Capabilities.Connections["fixture:mcp.one"].Disabled {
		t.Fatal("workspace override leaked into another workspace")
	}
	_ = client.Close()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	// Ordinary schema leftovers are inert, not migrated or rewritten on read.
	path, err := workspace.SessionDBPath(root, sid)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sessiondb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE session_agent_settings SET thinking_effort='removed-level',capability_selection='{"AllowedTools":"obsolete","context_retention_tokens":9000,"removed_leaf":true}' WHERE session_id=? AND category='default'`, sid); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	r, client, _, id = start()
	call(coreprotocol.OpenSession, &coreprotocol.InvocationSessionRequest{SessionID: sid}, &created)
	if created.Error != "" {
		t.Fatal(created.Error)
	}
	saveSession(staged) // explicit reconfiguration, without schema migration
	assert()
	// Last same-field write wins; a stale-looking form is not a banking lock.
	saveWorkspace(edit([]string{"editor", "tab_size"}, "2"))
	saveWorkspace(edit([]string{"editor", "tab_size"}, "8"))
	assert()
	beforeBytes, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	bad := client.Call(t.Context(), id, coreprotocol.ApplicationPreferencesSave, &coreprotocol.ApplicationPreferencesRequest{Edits: []types.SettingEdit{edit([]string{"network", "ca_file"}, `"missing-fixture-ca.pem"`)}, ProxyURL: coreprotocol.SecretString{Keep: true}}, &coreprotocol.ApplicationPreferencesResult{})
	afterBytes, _ := os.ReadFile(ConfigPath())
	if bad == nil || string(beforeBytes) != string(afterBytes) {
		t.Fatal("invalid trust was accepted or partly persisted")
	}
	saveApp(edit([]string{"editor", "word_wrap"}, "true"))
	assert()
	// Session Reset changes no parent choice, transcript or separate opt-out.
	call(coreprotocol.SessionSettingsReset, &coreprotocol.SessionSettingsMutationRequest{SessionID: sid}, &coreprotocol.SessionSettingsResult{})
	s := readSession(sid)
	if len(s.Overrides) != 0 || s.Effective.Value.Model != "app-next" || s.Effective.Value.Agents.Enabled || len(s.Effective.DisabledCapabilities) != 1 {
		t.Fatal("session reset did not restore only its preference layer")
	}
	saveWorkspace(types.SettingEdit{Path: []string{"agents"}})
	if !readSession(sid).Effective.Value.Agents.Enabled || !fetch().Effective.Capabilities.Connections["fixture:mcp.one"].Disabled || fetch().Effective.Editor.TabSize != 8 {
		t.Fatal("workspace section reset changed neighbors")
	}
}
