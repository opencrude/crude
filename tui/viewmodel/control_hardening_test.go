package viewmodel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"crude/coreprotocol"
	"crude/internal/wiretest"
	"crude/shellport"
	"crude/tui/screens/perfdash"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/types/control"

	"github.com/charmbracelet/x/ansi"
)

func TestConnectedPanelNavigationCannotLaunchTerminal(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "")
	root := t.TempDir()
	one := newRemoteJourneyShell(t, runtime, root)
	shells := []remoteJourneyShell{one}
	one.app.ws.(shellport.ShellControl).StartShellControl()
	// A successful read is also a barrier for exact shell registration.
	requestShellForTest(t, runtime, shells, one, "shell.inspect", struct{}{}, nil)
	external, err := runtime.Connect("navigation-only-controller")
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close()
	target := one.app.ws.(shellport.ShellControl).ControlIdentity()
	call := func(id, method string, value any) coreprotocol.Response {
		raw, err := wiretest.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return external.Call(t.Context(), coreprotocol.Request{ID: id, Owner: external.Owner, WorkspaceID: target.WorkspaceID, Method: method, Payload: raw})
	}
	if out := call("attach", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}); out.Error != nil {
		t.Fatal(out.Error)
	}
	if out := call("grant", coreprotocol.ControlClientRegister, coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Read, control.Write}, Shell: target.Shell}); out.Error != nil {
		t.Fatal(out.Error)
	}
	execute := func(id, command string, input any) control.Result {
		t.Helper()
		raw, _ := json.Marshal(input)
		request := control.Request{ID: id, CommandID: command, Target: target, Input: raw}
		done := make(chan coreprotocol.Response, 1)
		go func() { done <- call(id, coreprotocol.ControlExecute, request) }()
		var response coreprotocol.Response
		finished := false
		remoteJourneyUntil(t, shells, func() bool {
			select {
			case response = <-done:
				finished = true
			default:
			}
			return finished
		})
		var result control.Result
		if response.Error != nil || coreprotocol.Decode(response.Payload, &result) != nil {
			t.Fatal(response)
		}
		return result
	}
	for _, command := range []string{"panel.show", "terminal.open"} {
		input := any(struct{}{})
		if command == "panel.show" {
			input = map[string]any{"id": termdock.PanelTerminal}
		}
		out := execute(command, command, input)
		if out.State != control.Failed || out.Fault == nil || len(one.app.termMgr().List()) != 0 || !one.app.termDock.Empty() {
			t.Fatalf("navigation grant launched a PTY via %s: %+v", command, out)
		}
		if command == "panel.show" && !strings.Contains(out.Fault.Message, "terminal.open") {
			t.Fatal("empty terminal navigation lacks explicit creation instruction", out)
		}
	}
	out := execute("performance", "panel.show", map[string]any{"id": termdock.PanelPerformance})
	if out.State != control.Applied || one.app.perf.screen == nil || one.app.termDock.Panel(termdock.PanelPerformance) != one.app.perf.screen || !one.app.panelShown(termdock.PanelPerformance) {
		t.Fatal("Performance did not reuse retained surface", out)
	}
	first := one.app.perf.screen
	if paint := ansi.Strip(one.app.View().RenderContent()); !strings.Contains(paint, "Performance") || !strings.Contains(paint, "Pause") {
		t.Fatal("Performance not painted through dock", paint)
	}
	// Do not add broad perfdash.* write authority: clipboard export is a host
	// effect and remains unavailable through generic local controls.
	node := localControlNode(t, one.app, string(perfdash.CopyID))
	if _, _, err := one.app.describeShellControl(shellControlIntent{ComponentID: node.ID, Operation: ui.ControlOperation{Verb: "activate"}}, target); err == nil {
		t.Fatal("Performance navigation broadened generic clipboard authority")
	}
	if out := execute("hide-performance", "panel.hide", struct{}{}); out.State != control.Applied {
		t.Fatal(out)
	}
	if out := execute("show-performance-again", "panel.show", map[string]any{"id": termdock.PanelPerformance}); out.State != control.Applied || one.app.perf.screen != first {
		t.Fatal("Performance recreated instead of retaining product owner", out)
	}
}

func TestShellReplyBoundsBeforeClientValidation(t *testing.T) {
	for _, effect := range []control.Effect{control.Read, control.Write} {
		var sent control.Result
		msg := shellport.ControlRequestMsg{Envelope: coreprotocol.ControlEnvelope{Request: control.Request{ID: "oversize"}, Descriptor: control.Descriptor{Effect: effect}}, Reply: func(_ context.Context, result control.Result) error {
			sent = result
			return coreprotocol.ValidateControlResult(result)
		}}
		m := &App{}
		result := control.Result{State: control.Applied, ResourceIDs: []string{"kept"}, Output: json.RawMessage(`"` + strings.Repeat("x", control.MaxValueBytes) + `"`)}
		out := m.replyShellControl(msg, result)().(shellControlReplyMsg)
		want := control.Failed
		if effect == control.Write {
			want = control.Partial
		}
		if out.err != nil || sent.State != want || sent.Fault == nil || len(sent.ResourceIDs) != 1 {
			t.Fatalf("invalid result stranded before wire: %+v %v", sent, out.err)
		}
	}
}
