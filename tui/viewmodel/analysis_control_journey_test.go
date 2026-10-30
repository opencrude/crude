package viewmodel

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/core"
	"crude/coreprotocol"
	"crude/internal/wiretest"
	"crude/shellport"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
)

// Exercise the authenticated connected gateway, not only the shell's metadata
// helper or a preauthorized RequestShellControl envelope.
type analysisControlOutcome struct {
	control.Result
	RPCFault *coreprotocol.Fault
}

func analysisControlGateway(t *testing.T, runtime *core.Runtime, one remoteJourneyShell, root string, effects []control.Effect) func(control.Node, ui.ControlOperation, string) analysisControlOutcome {
	t.Helper()
	client, err := runtime.Connect(fmt.Sprintf("analysis-controller-%d", shellControlTestSequence.Add(1)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	target := one.app.ws.(shellport.ShellControl).ControlIdentity()
	call := func(method string, value any) coreprotocol.Response {
		t.Helper()
		raw, err := wiretest.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		id := fmt.Sprintf("analysis-gateway-%d", shellControlTestSequence.Add(1))
		if request, ok := value.(control.Request); ok {
			id = request.ID
		}
		return client.Call(t.Context(), coreprotocol.Request{ID: id, Owner: client.Owner, WorkspaceID: target.WorkspaceID, Method: method, Payload: raw})
	}
	if response := call(coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}); response.Error != nil {
		t.Fatal(response.Error)
	}
	if response := call(coreprotocol.ControlClientRegister, coreprotocol.ControlClientGrant{Effects: effects, Shell: target.Shell}); response.Error != nil {
		t.Fatal(response.Error)
	}
	return func(node control.Node, op ui.ControlOperation, action string) analysisControlOutcome {
		t.Helper()
		input, err := json.Marshal(shellControlIntent{ComponentID: node.ID, Operation: op, ExpectedAction: action})
		if err != nil {
			t.Fatal(err)
		}
		exact := target.Clone()
		exact.ResourceID, exact.SessionID = node.ID, node.Target.SessionID
		request := control.Request{
			ID: fmt.Sprintf("analysis-control-%d", shellControlTestSequence.Add(1)), CommandID: "shell.control",
			Caller: control.Caller{Kind: control.ExternalCaller, ClientID: client.Owner.ClientID},
			Target: exact, Input: input, Preconditions: control.Preconditions{Revision: &node.Revision, OpenEpoch: &node.OpenEpoch},
		}
		done := make(chan coreprotocol.Response, 1)
		go func() { done <- call(coreprotocol.ControlExecute, request) }()
		var response coreprotocol.Response
		finished := false
		remoteJourneyUntil(t, []remoteJourneyShell{one}, func() bool {
			select {
			case response = <-done:
				finished = true
			default:
			}
			return finished
		})
		var result analysisControlOutcome
		if response.Error != nil {
			result.RPCFault = response.Error
			return result
		}
		if err := coreprotocol.Decode(response.Payload, &result.Result); err != nil {
			t.Fatal(err)
		}
		return result
	}
}

func TestConnectedAnalysisTrustGenericControlsCannotAuthorizeOrDiscard(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "")
	root := t.TempDir()
	one := newRemoteJourneyShell(t, runtime, root)
	shells := []remoteJourneyShell{one}
	if result := requestShellForTest(t, runtime, shells, one, "settings.open", map[string]any{"scope": "workspace"}, nil); result.State != control.Applied {
		t.Fatal(result)
	}
	writer := analysisControlGateway(t, runtime, one, root, []control.Effect{control.Read, control.Write})
	reader := analysisControlGateway(t, runtime, one, root, []control.Effect{control.Read})
	navigate := func(page string) {
		t.Helper()
		value, _ := json.Marshal(page)
		for _, op := range []ui.ControlOperation{{Verb: "set_value", Value: value}, {Verb: "activate"}} {
			_, desc, node := invokeDeckControl(t, one, string(settingsui.WorkspaceSettingsSearchID), op)
			if result := writer(node, op, desc.ID); result.State != control.Applied {
				t.Fatalf("ordinary navigation: %+v", result)
			}
		}
	}
	describe := func(id string, op ui.ControlOperation, authority bool) (control.Descriptor, control.Node) {
		t.Helper()
		_, desc, node := invokeDeckControl(t, one, id, op)
		if desc.HumanOnly != authority || authority && desc.Effect != control.Authority {
			t.Fatalf("%s lost actual authority metadata: %+v", id, desc)
		}
		return desc, node
	}
	denied := func(result analysisControlOutcome) {
		t.Helper()
		if result.RPCFault != nil && result.RPCFault.Code == "control_failed" &&
			strings.Contains(result.RPCFault.Message, "human-owned authority") {
			return
		}
		if result.State != control.Failed || result.Fault == nil || result.Fault.Code != control.Denied {
			t.Fatalf("ordinary control acquired human authority: %+v", result)
		}
	}
	navigate("Code Intelligence")
	body := one.app.settingsSurface(one.app.overlayHost.Window(ui.OverlayWorkspaceSettings))
	if body == nil || body.Model().AppSettings().Analysis.Trust != types.AnalysisStaticOnly {
		t.Fatal("opening the mounted form acquired Trust")
	}
	if paint := ansi.Strip(one.app.View().RenderContent()); !strings.Contains(paint, "Trust") || !strings.Contains(paint, "Static only") {
		t.Fatalf("workspace Trust was not rendered:\n%s", paint)
	}
	trustID := string(ui.NewID(settingsui.WorkspaceSettingsFormID, "analysis.trust"))
	for _, op := range []ui.ControlOperation{
		{Verb: "select", ItemID: "project_execution_allowed"},
		{Verb: "set_value", Value: json.RawMessage(`"project_execution_allowed"`)},
	} {
		desc, node := describe(trustID, op, true)
		denied(writer(node, op, desc.ID))
		denied(reader(node, op, desc.ID))
	}
	// There is no generic input-injection escape hatch, even when addressed to
	// an ordinary parent form, a Trust field, or an authority-bearing button.
	_, trustNode := describe(trustID, ui.ControlOperation{Verb: "select", ItemID: "project_execution_allowed"}, true)
	_, formNode := describe(string(settingsui.WorkspaceSettingsFormID), ui.ControlOperation{Verb: "scroll"}, false)
	unsupported := func(result analysisControlOutcome) bool {
		return result.RPCFault != nil && result.RPCFault.Code == "control_failed" &&
			strings.Contains(result.RPCFault.Message, "unsupported semantic operation") ||
			result.State == control.Failed && result.Fault != nil && result.Fault.Code == control.Invalid
	}
	for _, verb := range []string{"key", "focus", "pointer"} {
		for _, node := range []control.Node{trustNode, formNode} {
			result := writer(node, ui.ControlOperation{Verb: verb, Value: json.RawMessage(`"right"`)}, "form.changed")
			if !unsupported(result) {
				t.Fatalf("generic %s synthesized human input or failed for an unrelated reason: %+v", verb, result)
			}
		}
	}
	if len(body.Model().Edits()) != 0 || one.app.ws.LoadAppSettings().Analysis.Trust != types.AnalysisStaticOnly {
		t.Fatal("denied generic input changed a draft or committed trust")
	}
	nativeChoice := func(code rune, want types.WorkspaceAnalysisTrust) {
		t.Helper()
		for _, child := range body.Children() {
			if form, ok := child.(*ui.Form); ok && form.ID() == settingsui.WorkspaceSettingsFormID {
				form.FocusFirst()
			}
		}
		body.FocusForm()
		_, cmd := one.app.Update(tea.KeyPressMsg{Code: code})
		runAppCommand(t, one.app, cmd)
		if body.Model().AppSettings().Analysis.Trust != want {
			t.Fatalf("human native keyboard cannot choose Trust: %+v", body.Model().Edits())
		}
	}
	nativeChoice(tea.KeyRight, types.AnalysisProjectExecutionAllowed)
	save := ui.ControlOperation{Verb: "activate"}
	for _, id := range []ui.ComponentID{settingsui.WorkspaceSettingsSaveID, settingsui.WorkspaceSettingsResetID} {
		desc, node := describe(string(id), save, true)
		denied(writer(node, save, desc.ID))
	}
	if one.app.ws.LoadAppSettings().Analysis.Trust != types.AnalysisStaticOnly {
		t.Fatal("generic Save committed the human's pending grant")
	}
	// The native focused footer + Enter is a human save and owns the durable
	// root binding; generic automation cannot synthesize this key path.
	body.FocusActions()
	_, cmd := one.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, one.app, cmd)
	body = one.app.settingsSurface(one.app.overlayHost.Window(ui.OverlayWorkspaceSettings))
	if one.app.ws.LoadAppSettings().Analysis.Trust != types.AnalysisProjectExecutionAllowed || len(body.Model().Edits()) != 0 ||
		one.app.ws.LoadWorkspaceOverrides().AnalysisRootIdentity == "" {
		t.Fatal("native Save did not persist and rebase the root-bound grant")
	}
	nativeChoice(tea.KeyLeft, types.AnalysisStaticOnly)
	navigate("Editor")
	if paint := ansi.Strip(one.app.View().RenderContent()); strings.Contains(paint, "Project execution allowed") || !strings.Contains(paint, "Word wrap") {
		t.Fatalf("Trust draft did not leave the visible page:\n%s", paint)
	}
	value := `"on"`
	if body.Model().AppSettings().Editor.WordWrap {
		value = `"off"`
	}
	ordinary := ui.ControlOperation{Verb: "set_value", Value: json.RawMessage(value)}
	desc, node := describe("workspace-settings/form/word-wrap", ordinary, false)
	if result := writer(node, ordinary, desc.ID); result.State != control.Applied {
		t.Fatalf("pending Trust unnecessarily blocks ordinary editing: %+v", result)
	}
	for _, id := range []ui.ComponentID{settingsui.WorkspaceSettingsSaveID, settingsui.WorkspaceSettingsResetID} {
		desc, node := describe(string(id), save, true)
		denied(writer(node, save, desc.ID))
		for _, verb := range []string{"key", "focus", "pointer"} {
			if result := writer(node, ui.ControlOperation{Verb: verb}, desc.ID); !unsupported(result) {
				t.Fatalf("off-page %s bypassed Save/Reset authority: %+v", verb, result)
			}
		}
	}
	if body.Model().AppSettings().Analysis.Trust != types.AnalysisStaticOnly || len(body.Model().Edits()) < 2 ||
		one.app.ws.LoadAppSettings().Analysis.Trust != types.AnalysisProjectExecutionAllowed {
		t.Fatalf("generic Save/Reset lost the off-page draft or changed committed trust: edits=%+v draft=%s committed=%s",
			body.Model().Edits(), body.Model().AppSettings().Analysis.Trust, one.app.ws.LoadAppSettings().Analysis.Trust)
	}
}
