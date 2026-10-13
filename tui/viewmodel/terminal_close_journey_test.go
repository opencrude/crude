package viewmodel

import (
	"encoding/json"
	"fmt"
	"testing"

	"crude/coreprotocol"
	"crude/shellport"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/types/control"
)

// Explicit close emits no manager exit event. It must still publish inventory
// invalidation to every connected viewer through both the RPC and control paths.
func TestConnectedTerminalCloseUnmountsEveryShell(t *testing.T) {
	for _, route := range []string{"rpc", "control"} {
		t.Run(route, func(t *testing.T) {
			runtime := remoteJourneyRuntime(t, "")
			root := t.TempDir()
			one := newRemoteJourneyShell(t, runtime, root)
			two := newRemoteJourneyShell(t, runtime, root)
			shells := []remoteJourneyShell{one, two}
			opened := requestShellForTest(t, runtime, shells, one, "terminal.open", struct{}{}, nil)
			if opened.State != control.Applied || len(opened.ResourceIDs) != 1 {
				t.Fatalf("terminal did not visibly open: %+v", opened)
			}
			id := opened.ResourceIDs[0]
			remoteJourneyUntil(t, shells, func() bool { _, exists := two.app.termMgr().Get(id); return exists })
			shown := requestShellForTest(t, runtime, shells, two, "terminal.show", map[string]any{"id": id}, nil)
			if shown.State != control.Applied || !two.app.termDock.Contains(id) {
				t.Fatalf("peer terminal not mounted: %+v", shown)
			}
			// Both docked and floating viewers must be removed; the peer's unrelated
			// workspace destination must remain mounted.
			two.app.promoteTerminal(id)
			two.app.termDock.ShowPanel(termdock.PanelNotices)
			one.app.View()
			two.app.View()
			if two.app.overlayHost.Window(ui.TerminalOverlayID(id)) == nil {
				t.Fatal("floating viewer was not mounted")
			}
			controller, err := runtime.Connect("terminal-close-" + route)
			if err != nil {
				t.Fatal(err)
			}
			defer controller.Close()
			target := one.app.ws.(shellport.ShellControl).ControlIdentity()
			call := func(requestID, method string, input any) coreprotocol.Response {
				raw, err := json.Marshal(input)
				if err != nil {
					t.Fatal(err)
				}
				return controller.Call(t.Context(), coreprotocol.Request{ID: requestID, Owner: controller.Owner, WorkspaceID: target.WorkspaceID, Method: method, Payload: raw})
			}
			if got := call("attach", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}); got.Error != nil {
				t.Fatal(got.Error)
			}
			if route == "control" {
				if got := call("grant", coreprotocol.ControlClientRegister, coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Delete}}); got.Error != nil {
					t.Fatal(got.Error)
				}
				input, _ := json.Marshal(coreprotocol.TerminalIDRequest{ID: id})
				closed := call("close", coreprotocol.ControlExecute, control.Request{ID: "close", CommandID: "terminal.close", Target: control.Target{WorkspaceID: target.WorkspaceID}, Input: input})
				var result control.Result
				if closed.Error != nil || json.Unmarshal(closed.Payload, &result) != nil || result.State != control.Committed {
					t.Fatalf("close receipt: %+v", closed)
				}
			} else {
				if closed := call("close", coreprotocol.TerminalClose, coreprotocol.TerminalIDRequest{ID: id}); closed.Error != nil {
					t.Fatal(closed.Error)
				}
			}
			// A committed backend close alone is insufficient: run real subscription
			// messages through Update and require the rendered shell to release it.
			remoteJourneyUntil(t, shells, func() bool {
				return one.app.termScreens[id] == nil && two.app.termScreens[id] == nil
			})
			for i, shell := range shells {
				shell.app.View()
				if _, exists := shell.app.termMgr().Get(id); exists || shell.app.termDock.Contains(id) || shell.app.overlayHost.Window(ui.TerminalOverlayID(id)) != nil || shell.app.terminalLeases[id].Generation != 0 {
					t.Fatalf("shell %d retained closed terminal %s", i, id)
				}
				if shell.app.rootHost.HasPointerCapture() {
					t.Fatal("closed viewer retained capture")
				}
			}
			if one.app.termDock.IsOpen() || one.app.terminalOwnsKeys() {
				t.Fatal("last terminal close retained dock or terminal focus")
			}
			if !two.app.termDock.IsOpen() || two.app.termDock.ActivePanel() != termdock.PanelNotices {
				t.Fatal("terminal close disturbed the peer's unrelated panel")
			}
			var inventory []any
			listed := call(fmt.Sprintf("list-%s", route), coreprotocol.TerminalList, struct{}{})
			if listed.Error != nil || json.Unmarshal(listed.Payload, &inventory) != nil || len(inventory) != 0 {
				t.Fatalf("closed PTY retained by core: %+v", listed)
			}
		})
	}
}
