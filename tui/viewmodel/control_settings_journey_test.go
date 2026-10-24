package viewmodel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/core"
	"crude/coreprotocol"
	"crude/provider"
	"crude/shellport"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
	"crude/workspace"

	"github.com/charmbracelet/x/ansi"
)

func settingsJourneyRuntime(t *testing.T) *core.Runtime {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/models") {
			http.Error(w, "unexpected settings request", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"test-model","object":"model"}]}`))
	}))
	t.Cleanup(server.Close)
	path := filepath.Join(t.TempDir(), "application.json")
	runtime, err := core.New(func(root string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(root, "test-model", "tester", types.Settings{}, nil)
		ws.SetAppDefaults(types.AppSettings{
			Accounts:     map[string]provider.Account{"test": {ID: "test", ProviderID: "openai", APIKey: "test", Connection: provider.Template{Name: "test", ProviderURL: server.URL, APIStandard: "openai", AuthType: provider.AuthProtocol}}},
			Agents:       types.AgentsSettings{Categories: map[string]types.CategorySettings{"default": {AccountID: "test", Model: "test-model"}, "crafter": {AccountID: "test", Model: "test-model"}}},
			LocalHistory: types.LocalHistorySettings{Enabled: "off"},
			Memory:       types.MemorySettings{AutoRecall: "off", AutoExtract: "off", Consolidation: "off"},
		})
		ws.OnAppPreferencesCommit = func(edits []types.SettingEdit) (types.AppSettings, error) {
			after := ws.AppDefaults()
			if err := types.ApplySettingsEdits(&after, edits); err != nil {
				return after, err
			}
			raw, err := json.Marshal(after)
			if err == nil {
				err = os.WriteFile(path, raw, 0600)
			}
			return after, err
		}
		return ws, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Error(err)
		}
	})
	return runtime
}

// Discover the current mounted control and its contextual descriptor through the
// connected shell entry points. Neither labels nor test-owned policy authorize it.
func journeySettingsControl(t *testing.T, runtime *core.Runtime, shells []remoteJourneyShell, shell remoteJourneyShell, suffix string, op ui.ControlOperation) control.Result {
	t.Helper()
	shell.app.View()
	cursor, id := "", ""
	for {
		// Fail with exact transport evidence rather than waiting for a reply the
		// core cannot accept. The actual command below uses this same page size.
		probe, err := shell.app.inspectShellControls(shell.app.ws.(shellport.ShellControl).ControlIdentity(), control.PageRequest{Limit: 128, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(probe)
		if err != nil {
			t.Fatal(err)
		}
		if fault := (control.Result{RequestID: "inventory-probe", OperationID: "inventory-probe", State: control.Applied, Output: raw}).Validate(); fault != nil {
			t.Fatalf("shell.controls cannot return its legal 128-node page: %d output bytes: %v", len(raw), fault)
		}
		result := requestShellForTest(t, runtime, shells, shell, "shell.controls", control.PageRequest{Limit: 128, Cursor: cursor}, nil)
		if result.State != control.Applied {
			t.Fatalf("inventory: %+v", result)
		}
		var page control.Page
		if err := json.Unmarshal(result.Output, &page); err != nil {
			t.Fatal(err)
		}
		for _, node := range page.Nodes {
			if strings.HasSuffix(node.ID, "::"+suffix) {
				id = node.ID
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if id == "" {
		t.Fatalf("missing mounted control %s", suffix)
	}
	intent := shellControlIntent{ComponentID: id, Operation: op}
	result := requestShellForTest(t, runtime, shells, shell, "shell.describe_control", intent, nil)
	if result.State != control.Applied {
		return result
	}
	var described struct {
		Descriptor control.Descriptor `json:"descriptor"`
		Node       control.Node       `json:"node"`
	}
	if err := json.Unmarshal(result.Output, &described); err != nil {
		t.Fatal(err)
	}
	intent.ExpectedAction = described.Descriptor.ID
	return requestShellForTest(t, runtime, shells, shell, "shell.control", intent, func(e *coreprotocol.ControlEnvelope) {
		e.Descriptor = described.Descriptor
		e.Descriptor.ID = "shell.control"
		e.Request.Target.ResourceID = id
		e.Request.Preconditions = control.Preconditions{Revision: &described.Node.Revision, OpenEpoch: &described.Node.OpenEpoch}
	})
}

func TestConnectedAgentsInstructionsApplyStageAndSharedSave(t *testing.T) {
	for _, role := range []string{"default", "crafter"} {
		t.Run(role, func(t *testing.T) {
			runtime := settingsJourneyRuntime(t)
			root := t.TempDir()
			one, peer := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
			shells := []remoteJourneyShell{one, peer}
			peer.app.Input.SetInput("peer private instructions draft")
			invoke := func(id string, op ui.ControlOperation) {
				t.Helper()
				if result := journeySettingsControl(t, runtime, shells, one, id, op); result.State != control.Applied {
					t.Fatalf("%s: %+v", id, result)
				}
			}
			requestShellForTest(t, runtime, shells, one, "settings.open", map[string]any{"scope": "application"}, nil)
			invoke(string(settingsui.WorkspaceSettingsSearchID), ui.ControlOperation{Verb: "set_value", Value: json.RawMessage(`"Agents"`)})
			invoke(string(settingsui.WorkspaceSettingsSearchID), ui.ControlOperation{Verb: "activate"})
			if result := journeySettingsControl(t, runtime, shells, one, "settings/agents/table", ui.ControlOperation{Verb: "activate", ItemID: "role/" + role}); result.State != control.Applied && result.State != control.Admitted {
				t.Fatalf("open Agent editor: %+v", result)
			}
			invoke("settings/agents/tabs", ui.ControlOperation{Verb: "activate", ItemID: "instructions"})
			text := "Connected " + role + " instructions acceptance"
			value, _ := json.Marshal(text)
			invoke("settings/agents/form/agent-instructions", ui.ControlOperation{Verb: "set_value", Value: value})
			if !strings.Contains(ansi.Strip(one.app.View().RenderContent()), text) {
				t.Fatal("edited instructions not painted")
			}
			// The editor's Save applies the agent and persists in one step.
			invoke("settings/agents/actions/Save", ui.ControlOperation{Verb: "activate"})
			body := one.app.settingsSurface(one.app.overlayHost.Window(ui.OverlayWorkspaceSettings))
			if body == nil || one.app.ws.LoadAppSettings().Agents.Categories[role].Instructions != text {
				t.Fatal("agent Save did not persist the instructions")
			}
			if body.Model().AppSettings().Agents.Categories[role].Instructions != text || len(body.Model().Edits()) != 0 {
				t.Fatal("agent Save left the form stale or dirty")
			}
			remoteJourneyUntil(t, shells, func() bool {
				return peer.app.ws.LoadAppSettings().Agents.Categories[role].Instructions == text
			})
			if peer.app.Input.Value() != "peer private instructions draft" || peer.app.overlayHost.Window(ui.OverlayWorkspaceSettings) != nil {
				t.Fatal("shared settings changed peer private state")
			}
		})
	}
}

func TestConnectedAgentsOffPageAuthorityDeniesApplyAndSave(t *testing.T) {
	for _, tc := range []struct{ role, field, value string }{
		{"crafter", "agent-tool-mode", "Custom selection"},
		{"default", "multi-agent", "on"},
		{"crafter", "agent-enabled", "off"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			runtime := settingsJourneyRuntime(t)
			one := newRemoteJourneyShell(t, runtime, t.TempDir())
			shells := []remoteJourneyShell{one}
			invoke := func(id string, op ui.ControlOperation) control.Result {
				return journeySettingsControl(t, runtime, shells, one, id, op)
			}
			requestShellForTest(t, runtime, shells, one, "settings.open", map[string]any{"scope": "application"}, nil)
			for _, op := range []ui.ControlOperation{{Verb: "set_value", Value: json.RawMessage(`"Agents"`)}, {Verb: "activate"}} {
				if r := invoke(string(settingsui.WorkspaceSettingsSearchID), op); r.State != control.Applied {
					t.Fatal(r)
				}
			}
			if r := invoke("settings/agents/table", ui.ControlOperation{Verb: "activate", ItemID: "role/" + tc.role}); r.State != control.Applied && r.State != control.Admitted {
				t.Fatal(r)
			}
			body := one.app.settingsSurface(one.app.overlayHost.Window(ui.OverlayWorkspaceSettings))
			model := body.Model()
			index := -1
			for i, category := range model.Categories() {
				if category.Name == "Agents" {
					index = i
				}
			}
			if index < 0 || model.Categories()[index].Form.Field(tc.field) == nil {
				t.Fatalf("missing authority fixture %s", tc.field)
			}
			// A human-owned edit on the full draft; navigate away before automation.
			if model.ValueForTest(index, tc.field) == tc.value {
				t.Fatalf("authority fixture must change %s", tc.field)
			}
			model.SetValueForTest(index, tc.field, tc.value)
			if r := invoke("settings/agents/tabs", ui.ControlOperation{Verb: "activate", ItemID: "instructions"}); r.State != control.Applied {
				t.Fatal(r)
			}
			if strings.Contains(ansi.Strip(one.app.View().RenderContent()), "Tool access") {
				t.Fatal("authority control remained on-page")
			}
			if r := invoke("settings/agents/actions/Save", ui.ControlOperation{Verb: "activate"}); r.State != control.Failed || r.Fault == nil || r.Fault.Code != control.Denied {
				t.Fatalf("offpage Apply: %+v", r)
			}
			// Human Apply stages it; generic Save must still inspect the full preimage.
			for _, child := range body.Children() {
				if child.ID() == "settings/agents" {
					child.(ui.ActionHandler).HandleAction(ui.EventContext{}, ui.Action{ID: "agents.save"})
				}
			}
			if model.Categories()[index].Form.Field(tc.field) != nil {
				t.Fatal("human Apply did not close the valid authority draft")
			}
			if r := invoke(string(settingsui.WorkspaceSettingsSaveID), ui.ControlOperation{Verb: "activate"}); r.State != control.Failed || r.Fault == nil || r.Fault.Code != control.Denied {
				t.Fatalf("authority Save: %+v", r)
			}
			if one.app.overlayHost.Window(ui.OverlayWorkspaceSettings) == nil {
				t.Fatal("denied Save closed draft")
			}
		})
	}
}

func TestConnectedAgentsInvalidApplyFailsAndRetainsDraft(t *testing.T) {
	runtime := settingsJourneyRuntime(t)
	one := newRemoteJourneyShell(t, runtime, t.TempDir())
	shells := []remoteJourneyShell{one}
	invoke := func(id string, op ui.ControlOperation) control.Result {
		return journeySettingsControl(t, runtime, shells, one, id, op)
	}
	requestShellForTest(t, runtime, shells, one, "settings.open", map[string]any{"scope": "application"}, nil)
	for _, op := range []ui.ControlOperation{{Verb: "set_value", Value: json.RawMessage(`"Agents"`)}, {Verb: "activate"}} {
		if r := invoke(string(settingsui.WorkspaceSettingsSearchID), op); r.State != control.Applied {
			t.Fatal(r)
		}
	}
	if r := invoke("settings/agents/table", ui.ControlOperation{Verb: "activate", ItemID: "role/crafter"}); r.State != control.Applied && r.State != control.Admitted {
		t.Fatal(r)
	}
	body := one.app.settingsSurface(one.app.overlayHost.Window(ui.OverlayWorkspaceSettings))
	index := body.Model().SelectedCategory()
	if r := invoke("settings/agents/form/model-context-window", ui.ControlOperation{Verb: "set_value", Value: json.RawMessage(`"not-a-number"`)}); r.State != control.Applied {
		t.Fatal(r)
	}
	if r := invoke("settings/agents/actions/Save", ui.ControlOperation{Verb: "activate"}); r.State != control.Failed || r.Fault == nil {
		t.Fatalf("invalid Apply acknowledged success: %+v", r)
	}
	field := body.Model().Categories()[index].Form.Field("model-context-window")
	if field == nil || field.Value.String() != "not-a-number" || !field.HasErrors() {
		t.Fatal("validation failure lost the editable draft")
	}
	if !strings.Contains(ansi.Strip(one.app.View().RenderContent()), "not-a-number") {
		t.Fatalf("invalid draft not painted: %s", ansi.Strip(one.app.View().RenderContent()))
	}
}
