package core

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"crude/agent/capability"
	"crude/agent/provider"
	"crude/core/coreprotocol"
	captypes "crude/types/capability"
	"crude/types/control"
	"crude/types/setting"
	"crude/workspace"
)

func runCapabilityControl(t *testing.T, a *actor, id, command string, input any) control.Result {
	t.Helper()
	raw, _ := json.Marshal(input)
	run, apply, handled := a.prepareControlDomain(control.Request{ID: id, CommandID: command, Input: raw}, coreprotocol.Owner{ClientID: "trusted", Epoch: 1})
	if !handled {
		t.Fatalf("%s was not handled", command)
	}
	result := run(context.Background())
	if apply != nil {
		result = apply(result)
	}
	return result
}

func TestControlCapabilitiesManageSecretsAndConnectionsWithoutRevealingValues(t *testing.T) {
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", setting.Settings{}, coreTestLogger{})
	defer ws.Close()
	app := setting.App{}
	app.Capabilities = captypes.WithDefaults(app.Capabilities)
	ws.SetAppDefaults(app)
	service, err := capability.New(ws.RootPath, filepath.Join(t.TempDir(), "artifacts"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ws.Capabilities = service
	if err := service.Configure(context.Background(), ws.LoadAppSettings().Capabilities); err != nil {
		t.Fatal(err)
	}
	ws.OnAppPreferencesCommit = func(edits []setting.Edit) (setting.App, error) {
		next := ws.AppDefaults()
		err := setting.ApplyEdits(&next, edits)
		return next, err
	}
	a := &actor{ws: ws}

	stored := runCapabilityControl(t, a, "secret", "capabilities.set_secret", map[string]any{"name": "EXA_API_KEY", "secret": "exa-live-value"})
	if stored.State != control.Committed {
		t.Fatalf("set_secret=%+v", stored)
	}
	if strings.Contains(string(stored.Output), "exa-live-value") {
		t.Fatalf("secret value leaked into the result: %s", stored.Output)
	}
	var view CapabilityControlView
	inspect := runCapabilityControl(t, a, "inspect", "capabilities.inspect", map[string]any{})
	if err := json.Unmarshal(inspect.Output, &view); err != nil || inspect.State != control.Committed {
		t.Fatalf("inspect=%+v err=%v", inspect, err)
	}
	if !slices.Contains(view.StoredSecrets, "EXA_API_KEY") {
		t.Fatalf("stored secrets=%v", view.StoredSecrets)
	}
	var web *CapabilityConnectionView
	for i := range view.Connections {
		if view.Connections[i].ID == captypes.WebConnectionID {
			web = &view.Connections[i]
		}
	}
	if web == nil || !web.Builtin || !slices.ContainsFunc(web.Secrets, func(s string) bool { return strings.HasPrefix(s, "EXA_API_KEY") && strings.HasSuffix(s, "stored") }) {
		t.Fatalf("web connection view=%+v", web)
	}

	off := runCapabilityControl(t, a, "tool-off", "capabilities.set_tool_enabled", map[string]any{"id": "web", "tool": "arxiv_search", "enabled": false})
	if off.State != control.Committed || !ws.AppDefaults().Capabilities.Connections["web"].Grants["arxiv_search"].Disabled {
		t.Fatalf("tool switch=%+v grants=%+v", off, ws.AppDefaults().Capabilities.Connections["web"].Grants)
	}
	disabled := runCapabilityControl(t, a, "disable", "capabilities.set_enabled", map[string]any{"id": "web", "enabled": false})
	if disabled.State != control.Committed || !ws.AppDefaults().Capabilities.Connections["web"].Disabled {
		t.Fatalf("disable=%+v", disabled)
	}
	removed := runCapabilityControl(t, a, "remove", "capabilities.remove_connection", map[string]any{"id": "web"})
	if removed.Fault == nil || !strings.Contains(removed.Fault.Message, "disabled but not removed") {
		t.Fatalf("built-in removal=%+v", removed)
	}

	saved := runCapabilityControl(t, a, "save", "capabilities.save_connection", map[string]any{"connection": map[string]any{
		"id": "docs", "name": "Docs", "transport": "http", "url": "https://mcp.example.com/mcp",
		"secret_refs": map[string]string{"Authorization": "secret:DOCS_TOKEN"},
	}})
	got := ws.AppDefaults().Capabilities.Connections["docs"]
	if saved.State != control.Committed || got.URL != "https://mcp.example.com/mcp" || got.SecretRefs["Authorization"] != "secret:DOCS_TOKEN" {
		t.Fatalf("save=%+v connection=%+v", saved, got)
	}
	renamed := runCapabilityControl(t, a, "rename", "capabilities.save_connection", map[string]any{"connection": map[string]any{"id": "docs", "name": "Team docs"}})
	got = ws.AppDefaults().Capabilities.Connections["docs"]
	if renamed.State != control.Committed || got.Name != "Team docs" || got.URL != "https://mcp.example.com/mcp" || got.SecretRefs["Authorization"] != "secret:DOCS_TOKEN" {
		t.Fatalf("partial edit lost stored fields: %+v %+v", renamed, got)
	}
}

func TestControlCapabilitiesManageSearchMethods(t *testing.T) {
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", setting.Settings{}, coreTestLogger{})
	defer ws.Close()
	app := setting.App{Accounts: map[string]provider.Account{
		"example-account": {ID: "example-account", Label: "Example account", APIKey: "fixture-key", ProviderID: "ollama-cloud", Connection: provider.Template{APIStandard: "ollama", ProviderURL: "https://ollama.example/api"}},
		"chat":            {ID: "chat", Label: "Chat", APIKey: "fixture-key", ProviderID: "custom", Connection: provider.Template{APIStandard: "openai", ProviderURL: "https://chat.example/v1"}},
	}}
	app.Capabilities = captypes.WithDefaults(app.Capabilities)
	ws.SetAppDefaults(app)
	service, err := capability.New(ws.RootPath, filepath.Join(t.TempDir(), "artifacts"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ws.Capabilities = service
	if err := service.Configure(context.Background(), ws.LoadAppSettings().Capabilities); err != nil {
		t.Fatal(err)
	}
	ws.OnAppPreferencesCommit = func(edits []setting.Edit) (setting.App, error) {
		next := ws.AppDefaults()
		err := setting.ApplyEdits(&next, edits)
		return next, err
	}
	a := &actor{ws: ws}
	if refused := runCapabilityControl(t, a, "chat", "capabilities.save_search_method", map[string]any{"account": "chat", "model": "m"}); refused.Fault == nil ||
		!strings.Contains(refused.Fault.Message, "cannot search the web") {
		t.Fatalf("a protocol without native search became a method: %+v", refused)
	}
	added := runCapabilityControl(t, a, "add", "capabilities.save_search_method", map[string]any{"account": "example-account", "model": "ignored"})
	routes := ws.AppDefaults().Capabilities.WebSearchRoutes
	if added.State != control.Committed || len(routes) != 1 || routes[0].Account != "example-account" || routes[0].Model != "" || routes[0].Disabled || routes[0].ID == "" {
		t.Fatalf("add=%+v routes=%+v", added, routes)
	}
	off := runCapabilityControl(t, a, "off", "capabilities.save_search_method", map[string]any{"id": routes[0].ID, "enabled": false})
	if off.State != control.Committed || !ws.AppDefaults().Capabilities.WebSearchRoutes[0].Disabled || ws.AppDefaults().Capabilities.WebSearchRoutes[0].Account != "example-account" {
		t.Fatalf("partial edit lost the account or did not turn the method off: %+v %+v", off, ws.AppDefaults().Capabilities.WebSearchRoutes)
	}
	var view CapabilityControlView
	inspect := runCapabilityControl(t, a, "inspect", "capabilities.inspect", map[string]any{})
	if err := json.Unmarshal(inspect.Output, &view); err != nil || len(view.SearchMethods) != 1 || view.SearchMethods[0].Enabled ||
		view.SearchMethods[0].LastResult != "not checked yet" {
		t.Fatalf("inspect does not list the method: %+v %v", view.SearchMethods, err)
	}
	if missing := runCapabilityControl(t, a, "missing", "capabilities.remove_search_method", map[string]any{"id": "nope"}); missing.Fault == nil {
		t.Fatal("removing an unknown method succeeded")
	}
	removed := runCapabilityControl(t, a, "remove", "capabilities.remove_search_method", map[string]any{"id": routes[0].ID})
	if removed.State != control.Committed || len(ws.AppDefaults().Capabilities.WebSearchRoutes) != 0 {
		t.Fatalf("remove=%+v routes=%+v", removed, ws.AppDefaults().Capabilities.WebSearchRoutes)
	}
}
