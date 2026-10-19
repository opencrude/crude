package core

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"crude/capability"
	"crude/coreprotocol"
	"crude/types"
	captypes "crude/types/capability"
	"crude/types/control"
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
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, coreTestLogger{})
	defer ws.Close()
	app := types.AppSettings{}
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
	ws.OnAppPreferencesCommit = func(edits []types.SettingEdit) (types.AppSettings, error) {
		next := ws.AppDefaults()
		err := types.ApplySettingsEdits(&next, edits)
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
