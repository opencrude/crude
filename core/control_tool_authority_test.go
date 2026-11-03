package core

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/types"
	"crude/types/control"
	"crude/workspace"
)

func TestControlGatewayProjectToolDecisionAuthority(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	r, err := New(func(root string) (*workspace.Workspace, error) {
		return workspace.NewWorkspace(root, "", "tester", types.Settings{}, coreTestLogger{}), nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	c := controlWireClient(t, r, "decision-authority")
	ctx := t.Context()
	var attached coreprotocol.AttachResult
	if err := c.Call(ctx, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: t.TempDir()}, &attached); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, attached.WorkspaceID, coreprotocol.ControlClientRegister, &coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Read, control.Write, control.Delete, control.Execute, control.Communication}}, nil); err != nil {
		t.Fatal(err)
	}
	ws := r.actors()[0].ws
	if ws.WorkspaceDB == nil {
		t.Fatal("attached workspace has no persistence")
	}
	// Snapshot predates an independently committed setup decision.
	stale := ws.LoadWorkspaceOverrides()
	const decision = `{ "invalid" : [ 1, 2 ] }`
	const marker = `"project_tool_decisions":` + decision
	if err := ws.WorkspaceDB.SetSetting("workspace_settings", `{"capabilities":{`+marker+`}}`); err != nil {
		t.Fatal(err)
	}
	assertDecision := func() {
		t.Helper()
		var row string
		if err := ws.WorkspaceDB.DB.QueryRow(`SELECT value FROM workspace_metadata WHERE key=?`, "workspace_settings").Scan(&row); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(row, marker) {
			t.Fatalf("persisted decision bytes changed: %s", row)
		}
	}
	n := 0
	call := func(command, scope, path string, value any) (control.Result, error) {
		t.Helper()
		n++
		id := fmt.Sprintf("decision-%d", n)
		input := map[string]any{"scope": scope, "path": path}
		if command == "settings.set" {
			input["value"] = value
		}
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		var out control.Result
		_, err = c.CallWith(ctx, attached.WorkspaceID, coreprotocol.ControlExecute, &control.Request{ID: id, CommandID: command, Input: raw}, &out, coreclient.CallOptions{ID: id})
		return out, err
	}
	for _, scope := range []string{"application", "workspace"} {
		for _, path := range []string{"capabilities", "capabilities.project_tool_decisions", "capabilities.project_tool_decisions.grant", "Capabilities.Project_Tool_Decisions.grant", "project_tool_decisions"} {
			for _, command := range []string{"settings.set", "settings.reset"} {
				out, err := call(command, scope, path, map[string]any{"grant": true})
				if err == nil || !strings.Contains(strings.ToLower(err.Error()), "human-owned authority") {
					t.Errorf("%s %s %s: expected human-only denial, got result %+v error %v", scope, command, path, out, err)
				}
				assertDecision()
			}
		}
	}
	out, err := call("settings.set", "workspace", "editor.tab_size", 6)
	if err != nil || out.Fault != nil {
		t.Fatalf("ordinary preference rejected: %+v %v", out, err)
	}
	if !strings.Contains(string(ws.LoadWorkspaceOverrides().Editor), `"tab_size":6`) {
		t.Fatal("ordinary preference did not persist")
	}
	assertDecision()
	// The legacy whole-snapshot writer must merge against current authority,
	// even when handed an old snapshot with a forged decision.
	stale.Capabilities = json.RawMessage(`{"project_tool_decisions":{"forged":true}}`)
	if err := ws.SaveWorkspaceSettings(stale); err != nil {
		t.Fatal(err)
	}
	assertDecision()
	for _, scope := range []types.SettingsScope{types.SettingsScopeApplication, types.SettingsScopeWorkspace} {
		for _, edit := range []types.SettingEdit{
			{Path: []string{"capabilities"}, After: json.RawMessage(`{}`)},
			{Path: []string{"capabilities", "project_tool_decisions"}},
			{Path: []string{"capabilities", "project_tool_decisions", "grant"}, After: json.RawMessage(`true`)},
		} {
			result := ws.PreparePreferencesSave(scope, []types.SettingEdit{edit})()
			if result.Err == nil || (len(edit.Path) < 3 && !strings.Contains(result.Err.Error(), "project tool decisions")) {
				t.Errorf("%s %v: expected rejection, got %v", scope, edit.Path, result.Err)
			}
			assertDecision()
		}
	}
}
