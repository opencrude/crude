package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/terminalvt"
	"crude/types"
	"crude/workspace"
)

// Terminals share no state with documents, files or language servers. A slow
// document effect (a large open, a save waiting on a disk lease) must not hold
// a terminal's creation, keystrokes or redraw reads behind it.
func TestTerminalOperationsDoNotWaitBehindDocumentLane(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	root := t.TempDir()
	r, err := New(func(root string) (*workspace.Workspace, error) {
		return workspace.NewWorkspace(root, "", "tester", types.Settings{}, coreTestLogger{}), nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	c, ws := resourceConnection(t, r, root, "terminal-lane")
	actors := r.actors()
	if len(actors) != 1 {
		t.Fatalf("actors = %d", len(actors))
	}
	release := make(chan struct{})
	if !actors[0].resourceLane.submit(func(context.Context) { <-release }) {
		t.Fatal("could not occupy the document lane")
	}
	defer close(release)

	call := func(id, method string, value, out any) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		result := c.Call(ctx, coreprotocol.Request{ID: id, Owner: c.Owner, WorkspaceID: ws, Method: method, Payload: raw})
		if result.Error != nil {
			t.Fatalf("%s waited behind unrelated document work: %v", method, result.Error)
		}
		if out != nil {
			if err := json.Unmarshal(result.Payload, out); err != nil {
				t.Fatal(err)
			}
		}
	}
	opts := workspace.TerminalCreateOpts{Command: "/bin/sh", Args: []string{"-c", "stty -echo; printf READY; cat"}, Cols: 40, Rows: 4}
	if runtime.GOOS == "windows" {
		// A long Windows fixture CWD wraps the prompt across more than four
		// rows, hiding READY above the active grid before the first read.
		opts = workspace.TerminalCreateOpts{Command: "cmd.exe", Args: []string{"/d", "/q", "/k", "prompt $G&echo READY"}, Cols: 40, Rows: 4}
	}
	var info workspace.TerminalInfo
	call("create", coreprotocol.TerminalCreate, opts, &info)
	var lease workspace.TerminalLease
	call("control", coreprotocol.TerminalControl, coreprotocol.TerminalIDRequest{ID: info.ID}, &lease)
	reads := 0
	waitFor := func(want string) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			reads++
			var p workspace.TerminalProjection
			call(fmt.Sprintf("rows-%d", reads), coreprotocol.TerminalRows, coreprotocol.TerminalRowsRequest{ID: info.ID, Start: -1, Count: 4}, &p)
			if strings.Contains(projectionText(p), want) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("terminal never showed %q: %q", want, projectionText(p))
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitFor("READY")
	for i, ch := range "zzmark" {
		call(fmt.Sprintf("key-%d", i), coreprotocol.TerminalInput, coreprotocol.TerminalInputRequest{Lease: lease, Kind: "key", Key: terminalvt.Key{Code: ch, Text: string(ch)}}, nil)
	}
	call("enter", coreprotocol.TerminalInput, coreprotocol.TerminalInputRequest{Lease: lease, Kind: "key", Key: terminalvt.Key{Code: terminalvt.KeyEnter}}, nil)
	waitFor("zzmark")
	call("close", coreprotocol.TerminalClose, coreprotocol.TerminalIDRequest{ID: info.ID}, nil)
}
