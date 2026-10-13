package viewmodel

import (
	"encoding/json"
	"testing"

	"crude/coreprotocol"
	"crude/shellport"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/types/control"
)

func TestConnectedGenericCloseObservesOriginalReceiptAfterOwnerRemoved(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "")
	root := t.TempDir()
	one := newRemoteJourneyShell(t, runtime, root)
	shells := []remoteJourneyShell{one}
	requestShellForTest(t, runtime, shells, one, "settings.open", map[string]any{"scope": "workspace"}, nil)
	target := one.app.ws.(shellport.ShellControl).ControlIdentity()
	external, err := runtime.Connect("receipt-controller")
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close()
	call := func(id, method string, input any) coreprotocol.Response {
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		return external.Call(t.Context(), coreprotocol.Request{ID: id, Owner: external.Owner, WorkspaceID: target.WorkspaceID, Method: method, Payload: raw})
	}
	if result := call("attach", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}); result.Error != nil {
		t.Fatal(result.Error)
	}
	if result := call("grant", coreprotocol.ControlClientRegister, coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Write}, Shell: target.Shell}); result.Error != nil {
		t.Fatal(result.Error)
	}
	node := localControlNode(t, one.app, string(settingsui.WorkspaceSettingsCancelID))
	intent := shellControlIntent{ComponentID: node.ID, Operation: ui.ControlOperation{Verb: "activate"}}
	d, exact, err := one.app.describeShellControl(intent, target)
	if err != nil {
		t.Fatal(err)
	}
	intent.ExpectedAction = d.ID
	raw, _ := json.Marshal(intent)
	target.ResourceID = node.ID
	request := control.Request{ID: "original-close", CommandID: "shell.control", Target: target, Input: raw, Preconditions: control.Preconditions{Revision: &exact.Revision, OpenEpoch: &exact.OpenEpoch}}
	ready := make(chan coreprotocol.Response, 1)
	go func() { ready <- call(request.ID, coreprotocol.ControlExecute, request) }()
	var reply coreprotocol.Response
	finished := false
	remoteJourneyUntil(t, shells, func() bool {
		select {
		case reply = <-ready:
			finished = true
		default:
		}
		return finished
	})
	var outcome control.Result
	if reply.Error != nil || json.Unmarshal(reply.Payload, &outcome) != nil || outcome.State != control.Applied || one.app.overlayHost.Window(ui.OverlayWorkspaceSettings) != nil {
		t.Fatalf("real Close failed: %+v", reply)
	}
	// There is no mounted component left to resolve. The exact receipt must be
	// returned before another description/effect, not require another UI owner.
	repeated := call(request.ID, coreprotocol.ControlExecute, request)
	if repeated.Error != nil || string(repeated.Payload) != string(reply.Payload) {
		t.Fatalf("original Close did not recover its immutable receipt: %+v", repeated)
	}
	request.Input = json.RawMessage(`{"component_id":"foreign","operation":{"verb":"activate"},"expected_action":"settings.close"}`)
	changed := call(request.ID, coreprotocol.ControlExecute, request)
	if changed.Error == nil {
		var denied control.Result
		if json.Unmarshal(changed.Payload, &denied) != nil || denied.State != control.Failed || denied.Fault == nil || denied.Fault.Code != control.Invalid {
			t.Fatalf("changed original intent accepted: %+v", changed)
		}
	}
}
