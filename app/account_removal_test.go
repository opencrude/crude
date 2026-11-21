package app

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/agent/provider"
	"crude/agent/session"
	"crude/core"
	coreclient "crude/core/client"
	coreprotocol "crude/core/protocol"
	"crude/types"
	captypes "crude/types/capability"
	"crude/workspace"
)

// Removing an account through the real core and file owner also removes every
// setting that named it, in the same write. No setting keeps the account's ID.
func TestRemovingAnAccountReleasesEverySettingThatUsesIt(t *testing.T) {
	defer setTestHome(t, t.TempDir())()
	const removed = "d1d2d3d4e5e6f7f8a9b0c1d2e3f4a5b6"
	on := true
	fc := FileConfig{
		Accounts: map[string]provider.Account{
			removed: {ID: removed, Label: "Removed example", APIKey: "fixture-key", ProviderID: "ollama-cloud", Connection: provider.Template{APIStandard: "ollama", ProviderURL: "https://ollama.example/api"}},
			"keep":  {ID: "keep", Label: "Example account", APIKey: "fixture-key", ProviderID: "custom", Connection: provider.Template{APIStandard: "openai", ProviderURL: "https://example.invalid/v1"}},
		},
		Agents: &session.AgentsConfig{Enabled: true, ActiveProfile: "default", Profiles: map[string]session.ProfileConfig{"default": {Categories: map[string]session.CategoryConfig{
			"default":          {AccountID: "keep", Model: "main-model"},
			"crafter":          {AccountID: removed, Model: "other-model", Enabled: &on, Description: "Builds"},
			types.ErrandRoleID: {AccountID: removed, Model: "errand-model"},
		}}},
			AgentProfiles: map[string]session.AgentProfileConfig{"reviewer": {Category: "researcher", AccountID: removed, Model: "other-model"}},
			Suggestions:   types.SuggestionSettings{AccountID: removed, Model: "fim", TimeoutSeconds: 4},
		},
		Editor: &types.EditorSettings{TabSize: 2},
		Memory: &types.MemorySettings{
			Ranker:         types.MemoryRankerSettings{Enabled: "on", AccountID: removed, Model: "example-decider", TopK: 3},
			MemoryCategory: types.CategorySettings{AccountID: removed, Model: "memory-model", Instructions: "Be brief."},
		},
		Safety: &types.SafetySettings{AutoPermission: types.AutoPermissionSettings{Enabled: "on", AccountID: removed, Provider: "ollama-cloud", Model: "example-decider", ThinkingEffort: "low"}},
		Capabilities: &captypes.Settings{WebSearchRoutes: []captypes.WebSearchRoute{
			{ID: "r1", Account: removed}, {ID: "r2", Account: "keep", Model: "search-model"},
		}},
	}
	if err := SaveFileConfig(fc); err != nil {
		t.Fatal(err)
	}
	app, err := New(Opts{FC: LoadFileConfig()})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := core.New(func(root string) (*workspace.Workspace, error) { return app.newWorkspace(root, &appLogger{}) }, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	peer, transport := net.Pipe()
	go func() { _ = core.ServeConnection(t.Context(), runtime, peer) }()
	client, err := coreclient.Dial(t.Context(), coreclient.SingleCarrier(transport), "account-removal")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var attached coreprotocol.AttachResult
	if err := client.Call(t.Context(), "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: t.TempDir()}, &attached); err != nil {
		t.Fatal(err)
	}
	remove := func(preview string) error {
		return client.Call(t.Context(), attached.WorkspaceID, coreprotocol.AccountChange,
			&coreprotocol.AccountChangeRequest{ID: removed, RemovalPreview: preview}, &coreprotocol.AccountView{})
	}
	stale := FileConfigToAppSettings(fc)
	stale.Memory.Ranker = types.MemoryRankerSettings{}
	if err := remove(types.AccountRemovalPreview(stale, removed)); err == nil || !strings.Contains(err.Error(), "account uses changed") {
		t.Fatalf("stale removal preview was accepted: %v", err)
	}
	if _, ok := LoadFileConfig().Accounts[removed]; !ok {
		t.Fatal("a refused removal mutated the account")
	}
	if err := remove(types.AccountRemovalPreview(FileConfigToAppSettings(fc), removed)); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), removed) {
		t.Fatalf("the saved settings still name the removed account:\n%s", raw)
	}
	after := FileConfigToAppSettings(LoadFileConfig())
	if _, exists := after.Accounts[removed]; exists || after.Accounts["keep"].Label != "Example account" {
		t.Fatalf("accounts = %v", after.Accounts)
	}
	crafter := after.Agents.Categories["crafter"]
	if crafter.AccountID != "" || crafter.Model != "" || crafter.Description != "Builds" || crafter.Enabled == nil || !*crafter.Enabled {
		t.Fatalf("crafter = %+v", crafter)
	}
	if main := after.Agents.Categories["default"]; main.AccountID != "keep" || main.Model != "main-model" {
		t.Fatalf("Main Agent changed: %+v", main)
	}
	if p := after.Agents.AgentProfiles["reviewer"]; p.AccountID != "" || p.Model != "" || p.Category != "researcher" {
		t.Fatalf("reviewer = %+v", p)
	}
	if s := after.Agents.Suggestions; s.AccountID != "" || s.Model != "" || s.TimeoutSeconds != 4 {
		t.Fatalf("suggestions = %+v", s)
	}
	if r := after.Memory.Ranker; r.Enabled != "off" || r.AccountID != "" || r.Model != "" || r.TopK != 3 {
		t.Fatalf("ranker = %+v", r)
	}
	if m := after.Memory.MemoryCategory; m.AccountID != "" || m.Model != "" || m.Instructions != "Be brief." {
		t.Fatalf("memory agent = %+v", m)
	}
	if a := after.Safety.AutoPermission; a.Enabled != "off" || a.AccountID != "" || a.Model != "" || a.ThinkingEffort != "low" {
		t.Fatalf("auto review = %+v", a)
	}
	if routes := after.Capabilities.WebSearchRoutes; len(routes) != 1 || routes[0].ID != "r2" {
		t.Fatalf("search methods = %+v", routes)
	}
	if after.Editor.TabSize != 2 {
		t.Fatalf("an unrelated preference changed: %+v", after.Editor)
	}
}
