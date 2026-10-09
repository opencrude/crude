package core

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/workspace"
)

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
