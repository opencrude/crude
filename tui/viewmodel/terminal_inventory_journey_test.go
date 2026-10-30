package viewmodel

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/coreprotocol"
	"crude/shellport"
	"crude/tui/screens/shellterm"
	"crude/types/control"
)

// Withhold actual replica application, not the create RPC or a synthetic
// terminal manager. An immutable create receipt cannot make Get/List current.
// This also exercises shutdown while the same publication is still uncommitted.
func TestConnectedTerminalOpenWaitsForInventoryApplication(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "publish", true: "cancel"}[cancel], func(t *testing.T) {
			runtime := remoteJourneyRuntime(t, "")
			root := t.TempDir()
			shell := newRemoteJourneyShell(t, runtime, root)
			shells := []remoteJourneyShell{shell}
			inspected := requestShellForTest(t, runtime, shells, shell, "shell.inspect", struct{}{}, nil)
			if inspected.State != control.Applied {
				t.Fatal("shell was not attached", inspected)
			}
			controller, err := runtime.Connect("terminal-inventory-fixture")
			if err != nil {
				t.Fatal(err)
			}
			defer controller.Close()
			target := shell.app.ws.(shellport.ShellControl).ControlIdentity()
			attach, err := coreprotocol.Marshal(&coreprotocol.AttachRequest{Root: root})
			if err != nil {
				t.Fatal(err)
			}
			if reply := controller.Call(t.Context(), coreprotocol.Request{
				ID: "attach", Owner: controller.Owner, Method: coreprotocol.Attach, Payload: attach,
			}); reply.Error != nil {
				t.Fatal(reply.Error)
			}
			command := shell.app.openNewTerminal()
			if command == nil {
				t.Fatal("terminal creation was unavailable")
			}
			t.Cleanup(shell.app.cancelClientIOOperations)
			done := make(chan tea.Msg, 1)
			go func() { done <- command() }()
			// No shell FIFO application occurs until after the negative
			// assertion. Independently inspect the retained core to prove the
			// command already created its real PTY, with exactly one identity.
			var infos coreprotocol.TerminalInfos
			deadline := time.Now().Add(5 * time.Second)
			payload, err := coreprotocol.Marshal(&coreprotocol.Empty{})
			if err != nil {
				t.Fatal(err)
			}
			for sequence := 0; ; sequence++ {
				reply := controller.Call(t.Context(), coreprotocol.Request{
					ID: "list-" + strconv.Itoa(sequence), Owner: controller.Owner,
					WorkspaceID: target.WorkspaceID, Method: coreprotocol.TerminalList, Payload: payload,
				})
				if reply.Error != nil || coreprotocol.Decode(reply.Payload, &infos) != nil {
					t.Fatal("retained terminal inventory read failed", reply.Error)
				}
				if len(infos) != 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("terminal creation never reached the retained core")
				}
				time.Sleep(time.Millisecond)
			}
			if len(infos) != 1 || len(shell.app.termMgr().List()) != 0 {
				t.Fatal("fixture did not separate committed core inventory from unapplied shell inventory")
			}
			id := infos[0].ID
			select {
			case <-done:
				t.Fatal("terminal open completed before its inventory publication was applied")
			case <-time.After(60 * time.Millisecond):
				// A bounded quiet observation is the negative assertion, not
				// a sleep intended to make the command's race disappear.
			}
			if cancel {
				shell.app.cancelClientIOOperations()
				select {
				case msg := <-done:
					created, ok := msg.(terminalCreatedMsg)
					if !ok || created.info.ID != id || created.err == nil || shell.app.termScreens[id] != nil {
						t.Fatal("cancelled publication wait lost its retained resource or mounted stale UI")
					}
				case <-time.After(time.Second):
					t.Fatal("workspace cancellation did not join the unapplied inventory wait")
				}
				return
			}
			finished := false
			var receipt terminalCreatedMsg
			remoteJourneyUntil(t, shells, func() bool {
				if !finished {
					select {
					case msg := <-done:
						created, ok := msg.(terminalCreatedMsg)
						if !ok || created.err != nil || created.info.ID != id {
							t.Fatal("create did not complete with its exact retained resource", msg)
						}
						receipt = created
						_, follow := shell.app.Update(msg)
						runAppCommand(t, shell.app, follow)
						finished = true
					default:
					}
				}
				_, present := shell.app.termMgr().Get(id)
				return finished && present && shell.app.termScreens[id] != nil
			})
			hidden := requestShellForTest(t, runtime, shells, shell, "terminal.hide", struct{}{}, nil)
			shown := requestShellForTest(t, runtime, shells, shell, "terminal.show", map[string]any{"id": id}, nil)
			if hidden.State != control.Applied || shown.State != control.Applied || !shell.app.termDock.Contains(id) {
				data, _ := json.Marshal(shown)
				t.Fatalf("immediate hide/show could not use the completed terminal: %s", data)
			}
			if len(shell.app.termMgr().List()) != 1 {
				t.Fatal("publication wait recreated or duplicated the terminal")
			}
			payload, err = coreprotocol.Marshal(&coreprotocol.TerminalIDRequest{ID: id})
			if err != nil {
				t.Fatal(err)
			}
			if reply := controller.Call(t.Context(), coreprotocol.Request{
				ID: "close", Owner: controller.Owner, WorkspaceID: target.WorkspaceID,
				Method: coreprotocol.TerminalClose, Payload: payload,
			}); reply.Error != nil {
				t.Fatal(reply.Error)
			}
			remoteJourneyUntil(t, shells, func() bool {
				_, present := shell.app.termMgr().Get(id)
				return !present && shell.app.termScreens[id] == nil
			})
			// A delayed/repeated exact create result cannot resurrect a terminal
			// whose newer canonical inventory has already removed it.
			_, follow := shell.app.Update(receipt)
			runAppCommand(t, shell.app, follow)
			if shell.app.termScreens[id] != nil || shell.app.termDock.Contains(id) {
				t.Fatal("late create receipt remounted a terminal removed from inventory")
			}
		})
	}
}

// The inventory barrier and the newer local Close are separate owners. The
// first actual create completion is held while a picker mount closes the same
// retained resource, then delivered before canonical removal can be installed.
func TestConnectedDelayedTerminalCreateCannotReopenLocallyClosedInventory(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "")
	shell := newRemoteJourneyShell(t, runtime, t.TempDir())
	shells := []remoteJourneyShell{shell}
	attached := requestShellForTest(t, runtime, shells, shell, "shell.inspect", struct{}{}, nil)
	if attached.State != control.Applied {
		t.Fatal("shell was not attached", attached)
	}
	command := shell.app.openNewTerminal()
	if command == nil {
		t.Fatal("terminal creation unavailable")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- command() }()
	var receipt terminalCreatedMsg
	received := false
	remoteJourneyUntil(t, shells, func() bool {
		select {
		case msg := <-done:
			var ok bool
			receipt, ok = msg.(terminalCreatedMsg)
			if !ok || receipt.err != nil || receipt.info.ID == "" || receipt.lease.Generation == 0 {
				t.Fatal("creation did not acquire an exact retained terminal", msg)
			}
			received = true
		default:
		}
		return received
	})
	id := receipt.info.ID
	if shell.app.termScreens[id] != nil {
		t.Fatal("delayed create message was already mounted")
	}
	shown := requestShellForTest(t, runtime, shells, shell, "terminal.show", map[string]any{"id": id}, nil)
	if shown.State != control.Applied || shell.app.termScreens[id] == nil {
		t.Fatal("inventory-backed picker path could not mount the retained terminal", shown)
	}
	shell.app.closeTerminal(id)
	// Deliberately do not pump removal publications after local Close.
	if _, known := shell.app.termMgr().Get(id); !known || !shell.app.terminalClosed[id] {
		t.Fatal("fixture missed the local-close/unapplied-removal interval")
	}
	shell.app.dockTerminal(id)
	shell.app.promoteTerminal(id)
	shell.app.focusTerminal(id)
	if shell.app.termScreens[id] != nil || shell.app.termDock.Contains(id) {
		t.Fatal("stale inventory placement reopened a locally closed terminal")
	}
	shell.app.openTerminalPicker()
	if menu := shell.app.LastContextMenu; menu == nil || len(menu.Sections) != 1 || len(menu.Sections[0].Items) != 1 || menu.Sections[0].Items[0].Label != "New Terminal" {
		t.Fatal("closed terminal remained selectable before removal inventory")
	}
	shell.app.closeContextMenu()
	_, follow := shell.app.Update(receipt)
	runAppCommand(t, shell.app, follow)
	if shell.app.termScreens[id] != nil || shell.app.termDock.Contains(id) || shell.app.terminalLeases[id].Generation != 0 {
		t.Fatal("delayed create completion reopened a locally closed terminal before removal inventory")
	}
	// A take-control completion shares the same close fence, including the
	// streaming fast path that otherwise installs its lease before row handling.
	_, follow = shell.app.Update(terminalProjectionMsg{
		service: receipt.service, generation: receipt.generation, lease: receipt.lease,
		command: shellterm.Command{ID: id, Kind: shellterm.CommandTakeControl},
	})
	runAppCommand(t, shell.app, follow)
	if shell.app.termScreens[id] != nil || shell.app.terminalLeases[id].Generation != 0 {
		t.Fatal("delayed control completion restored a locally closed terminal lease")
	}
	remoteJourneyUntil(t, shells, func() bool {
		_, known := shell.app.termMgr().Get(id)
		return !known && shell.app.termScreens[id] == nil
	})
}
