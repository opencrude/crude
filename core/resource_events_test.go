package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/types"
	"crude/types/control"
	"crude/workspace"
)

// The real PTY child deliberately produces no output: lifecycle publication must
// not depend on a prompt, title, first read or natural exit waking subscribers.
func TestResourceEventsSilentTerminalChild(t *testing.T) {
	if os.Getenv("CRUDE_TEST_SILENT_TERMINAL_CHILD") != "1" {
		t.Skip("native silent-PTY subprocess fixture")
	}
	time.Sleep(5 * time.Minute)
	os.Exit(0)
}

func TestResourceEventsSilentTerminalCreateAndCloseReachAllSubscribers(t *testing.T) {
	for _, route := range []string{"RPC", "semantic control"} {
		t.Run(route, func(t *testing.T) { testSilentTerminalLifecycle(t, route == "semantic control") })
	}
}

func testSilentTerminalLifecycle(t *testing.T, semantic bool) {
	t.Helper()
	setCoreTestHome(t, t.TempDir())
	t.Setenv("CRUDE_TEST_SILENT_TERMINAL_CHILD", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(func(root string) (*workspace.Workspace, error) {
		return workspace.NewWorkspace(root, "", "tester", types.Settings{}, coreTestLogger{}), nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	root := t.TempDir()
	creator, ws := resourceConnection(t, r, root, "silent-creator")
	peer, _ := resourceConnection(t, r, root, "silent-peer")
	if semantic {
		callCore(t, creator, "control-grant", ws, coreprotocol.ControlClientRegister, coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Execute, control.Delete}})
	}
	lifecycle := func(id, method string, input, out any) {
		t.Helper()
		result := callCore(t, creator, id, ws, method, input)
		if out != nil {
			decodeResource(t, result, out)
		}
	}
	if semantic {
		lifecycle = func(id, method string, input, out any) {
			t.Helper()
			raw, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			var result control.Result
			decodeResource(t, callCore(t, creator, id, ws, coreprotocol.ControlExecute, control.Request{ID: id, CommandID: method, Input: raw}), &result)
			if result.Fault != nil || result.State != control.Committed {
				t.Fatalf("%s did not commit: %+v", method, result)
			}
			if out != nil {
				if err := json.Unmarshal(result.Output, out); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	var info workspace.TerminalInfo
	lifecycle("silent-create", coreprotocol.TerminalCreate, workspace.TerminalCreateOpts{
		Command: executable, Args: []string{"-test.run=^TestResourceEventsSilentTerminalChild$"}, CWD: root, Cols: 40, Rows: 4,
	}, &info)
	if info.ID == "" || info.PID <= 0 || !info.Alive {
		t.Fatalf("silent child was not created alive: %+v", info)
	}
	for _, subscriber := range []*Connection{creator, peer} {
		nextResourceChange(t, subscriber, ws, func(ch coreprotocol.Change) bool { return slices.Contains(ch.Terminals, info.ID) })
		var inventory []workspace.TerminalInfo
		decodeResource(t, callCore(t, subscriber, "silent-inventory", ws, coreprotocol.TerminalList, struct{}{}), &inventory)
		if len(inventory) != 1 || inventory[0].ID != info.ID || inventory[0].PID != info.PID || !inventory[0].Alive {
			t.Fatalf("subscriber did not discover the same silent PTY: %+v", inventory)
		}
	}
	var projection workspace.TerminalProjection
	decodeResource(t, callCore(t, peer, "silent-rows", ws, coreprotocol.TerminalRows, coreprotocol.TerminalRowsRequest{ID: info.ID, Start: -1, Count: 4}), &projection)
	if !projection.Info.Alive || projection.Info.PID != info.PID || strings.TrimSpace(projectionText(projection)) != "" {
		t.Fatalf("fixture must remain alive and silent: %+v text=%q", projection.Info, projectionText(projection))
	}
	lifecycle("silent-close", coreprotocol.TerminalClose, coreprotocol.TerminalIDRequest{ID: info.ID}, nil)
	for _, subscriber := range []*Connection{creator, peer} {
		nextResourceChange(t, subscriber, ws, func(ch coreprotocol.Change) bool { return slices.Contains(ch.Terminals, info.ID) })
		var inventory []workspace.TerminalInfo
		decodeResource(t, callCore(t, subscriber, "closed-inventory", ws, coreprotocol.TerminalList, struct{}{}), &inventory)
		if len(inventory) != 0 {
			t.Fatalf("subscriber retained closed PTY: %+v", inventory)
		}
	}
}

func resourceConnection(t *testing.T, r *Runtime, root, id string) (*Connection, string) {
	t.Helper()
	c, err := r.Connect(id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	var attached coreprotocol.AttachResult
	decodeResource(t, callCore(t, c, "attach", "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}), &attached)
	return c, attached.WorkspaceID
}

func decodeResource(t *testing.T, response coreprotocol.Response, out any) {
	t.Helper()
	if err := json.Unmarshal(response.Payload, out); err != nil {
		t.Fatal(err)
	}
}

func nextResourceChange(t *testing.T, c *Connection, ws string, accept func(coreprotocol.Change) bool) coreprotocol.Change {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case change := <-c.Events():
			if change.Resync || change.WorkspaceID != ws || change.Revision <= change.BaseRevision {
				t.Fatalf("invalid resource delta: %+v", change)
			}
			if change.TerminalEffects != nil && len(change.TerminalEffects.Effects.Replies) != 0 {
				t.Fatalf("query reply bytes leaked to subscriber: %+v", change)
			}
			if accept(change) {
				return change
			}
		case <-timer.C:
			t.Fatal("no unsolicited resource change arrived")
		}
	}
}

func TestResourceEventsUnsolicitedOutputAndNaturalExit(t *testing.T) {
	r, root := resourceRuntime(t)
	c, ws := resourceConnection(t, r, root, "terminal-events")
	var info workspace.TerminalInfo
	decodeResource(t, callCore(t, c, "create", ws, coreprotocol.TerminalCreate, workspace.TerminalCreateOpts{
		Command: "/bin/sh", Args: []string{"-c", "stty -echo; printf '\033]2;ready\007'; read line; printf 'FINAL-GRID'; exit 7"}, Cols: 40, Rows: 4,
	}), &info)
	// The readiness event must arrive without fetching Rows or polling the core.
	ready := nextResourceChange(t, c, ws, func(ch coreprotocol.Change) bool {
		return ch.TerminalEffects != nil && ch.TerminalEffects.ID == info.ID && ch.TerminalEffects.Effects.Title == "ready"
	})
	if !slices.Contains(ready.Terminals, info.ID) {
		t.Fatal("effects delta omitted terminal identity")
	}
	// Consume the initial output invalidation before admitting the exit trigger.
	nextResourceChange(t, c, ws, func(ch coreprotocol.Change) bool {
		return slices.Contains(ch.Terminals, info.ID) && ch.TerminalEffects == nil
	})
	var lease workspace.TerminalLease
	decodeResource(t, callCore(t, c, "control", ws, coreprotocol.TerminalControl, coreprotocol.TerminalIDRequest{ID: info.ID}), &lease)
	callCore(t, c, "finish", ws, coreprotocol.TerminalInput, coreprotocol.TerminalInputRequest{Lease: lease, Kind: "paste", Data: []byte("\n")})
	// Output invalidation and the subsequent natural-exit invalidation are
	// separate service facts. Fetch the final grid only after both arrive.
	var last coreprotocol.Change
	for i := 0; i < 2; i++ {
		ch := nextResourceChange(t, c, ws, func(ch coreprotocol.Change) bool { return slices.Contains(ch.Terminals, info.ID) })
		if i > 0 && ch.BaseRevision < last.Revision {
			t.Fatalf("terminal cursor regressed: %+v -> %+v", last, ch)
		}
		last = ch
	}
	var p workspace.TerminalProjection
	decodeResource(t, callCore(t, c, "final-rows", ws, coreprotocol.TerminalRows, coreprotocol.TerminalRowsRequest{ID: info.ID, Start: -1, Count: 4}), &p)
	if p.Info.Alive || p.Info.ExitCode != 7 || !strings.Contains(projectionText(p), "FINAL-GRID") {
		t.Fatalf("exit notification preceded final readable grid: %+v text=%q", p.Info, projectionText(p))
	}
}

func TestResourceEventsClipboardOnlyExactControllerAndObserverMetadata(t *testing.T) {
	r, root := resourceRuntime(t)
	a, ws := resourceConnection(t, r, root, "clipboard-controller")
	b, _ := resourceConnection(t, r, root, "clipboard-observer")
	var info workspace.TerminalInfo
	decodeResource(t, callCore(t, a, "create", ws, coreprotocol.TerminalCreate, workspace.TerminalCreateOpts{
		Command: "/bin/sh", Args: []string{"-c", "stty -echo; printf '\033]2;ready\007'; while read line; do printf '\033]2;offered\007\033]52;c;c2VjcmV0\007\033[5n'; done"}, Cols: 40, Rows: 4,
	}), &info)
	for _, c := range []*Connection{a, b} {
		nextResourceChange(t, c, ws, func(ch coreprotocol.Change) bool {
			return ch.TerminalEffects != nil && ch.TerminalEffects.Effects.Title == "ready"
		})
	}
	var lease workspace.TerminalLease
	decodeResource(t, callCore(t, a, "control", ws, coreprotocol.TerminalControl, coreprotocol.TerminalIDRequest{ID: info.ID}), &lease)
	check := func(controller, observer *Connection, inputID string, titleChanged bool) {
		callCore(t, controller, inputID, ws, coreprotocol.TerminalInput, coreprotocol.TerminalInputRequest{Lease: lease, Kind: "paste", Data: []byte("offer\n")})
		for _, c := range []*Connection{controller, observer} {
			change := nextResourceChange(t, c, ws, func(ch coreprotocol.Change) bool {
				return ch.TerminalEffects != nil && ch.TerminalEffects.Effects.Title == "offered" && len(ch.Terminals) > 0
			})
			effects := change.TerminalEffects.Effects
			if change.TerminalEffects.ID != info.ID || !slices.Contains(change.Terminals, info.ID) || effects.TitleChanged != titleChanged {
				t.Fatalf("observer lost metadata: %+v", change)
			}
			if c == controller {
				if len(effects.Clipboard) != 1 || string(effects.Clipboard[0]) != "secret" {
					t.Fatalf("controller lost clipboard offer: %+v", effects)
				}
			} else if len(effects.Clipboard) != 0 {
				t.Fatalf("observer received clipboard bytes: %+v", effects)
			}
		}
	}
	check(a, b, "offer-first", true)
	callCore(t, a, "release", ws, coreprotocol.TerminalRelease, lease)
	decodeResource(t, callCore(t, b, "takeover", ws, coreprotocol.TerminalControl, coreprotocol.TerminalIDRequest{ID: info.ID}), &lease)
	check(b, a, "offer-second", false)
}
