package core

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"crude/core/client"
	"crude/core/protocol"
	"crude/core/wire"
	"crude/workspace"
)

// Input control follows the window the user works in. A present controller
// keeps control against an automatic claim and yields only to an explicit
// takeover. A window that closed without detaching keeps its session for the
// reconnect grace; typing in another window takes its lease.
func TestTerminalControlPassesFromAwayWindowAndOnlyExplicitlyFromPresentOne(t *testing.T) {
	r, root := resourceRuntime(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var mu sync.Mutex
	var carrier corewire.Conn
	offline := false
	dial := func(ctx context.Context) (corewire.Conn, error) {
		mu.Lock()
		defer mu.Unlock()
		if offline {
			return nil, errors.New("window closed")
		}
		a, b := net.Pipe()
		go func() { _ = ServeConnection(t.Context(), r, b) }()
		carrier = corewire.NewMux(a, corewire.CarrierLocal, true)
		return carrier, nil
	}
	first, err := coreclient.Dial(ctx, dial, "terminal-first-window")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	var attached coreprotocol.AttachResult
	if err := first.Call(ctx, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root}, &attached); err != nil {
		t.Fatal(err)
	}
	var info workspace.TerminalInfo
	if err := first.Call(ctx, attached.WorkspaceID, coreprotocol.TerminalCreate, &workspace.TerminalCreateOpts{Command: "/bin/sh", Args: []string{"-c", "cat"}, Cols: 80, Rows: 4}, &info); err != nil {
		t.Fatal(err)
	}
	firstView := coreclient.WorkspaceView{Client: first, ID: attached.WorkspaceID}
	if _, err := firstView.ControlTerminal(ctx, info.ID, workspace.ClaimFree); err != nil {
		t.Fatal(err)
	}
	second := newResourceWire(t, r, "terminal-second-window")
	var peer coreprotocol.AttachResult
	second.call(t, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root}, &peer)
	secondView := coreclient.WorkspaceView{Client: second.c, ID: peer.WorkspaceID}
	if _, err := secondView.ControlTerminal(ctx, info.ID, workspace.ClaimFree); !errors.Is(err, workspace.ErrTerminalControl) {
		t.Fatal("an automatic claim took control from a present window", err)
	}
	taken, err := secondView.ControlTerminal(ctx, info.ID, workspace.ClaimTakeOver)
	if err != nil || taken.Owner.ClientID != "terminal-second-window" {
		t.Fatal("explicit takeover failed", taken, err)
	}
	if _, err := firstView.ControlTerminal(ctx, info.ID, workspace.ClaimFree); !errors.Is(err, workspace.ErrTerminalControl) {
		t.Fatal("the previous controller reclaimed control without asking", err)
	}
	if _, err := firstView.ControlTerminal(ctx, info.ID, workspace.ClaimTakeOver); err != nil {
		t.Fatal("the first window could not take control back", err)
	}
	// Close the first window's carrier and keep it from reconnecting: its
	// session now waits for the reconnect grace with the lease.
	mu.Lock()
	offline = true
	cut := carrier
	mu.Unlock()
	_ = cut.Close()
	deadline := time.Now().Add(5 * time.Second)
	for !r.ownerAway(coreprotocol.Owner{ClientID: "terminal-first-window", Epoch: taken.Owner.Epoch}) {
		if time.Now().After(deadline) {
			t.Fatal("the closed window never became away")
		}
		time.Sleep(time.Millisecond)
	}
	r.mu.Lock()
	owner := r.workspaces[peer.WorkspaceID]
	r.mu.Unlock()
	if owner == nil {
		t.Fatal("workspace actor is missing")
	}
	if held, ok := owner.ws.Terminals.Controller(info.ID); !ok || held.ClientID != "terminal-first-window" {
		t.Fatal("fixture lost the away window's retained lease", held, ok)
	}
	// A plain claim keeps an absent controller's lease, so a brief network
	// loss never moves control; typing in the window in use takes it.
	if _, err := secondView.ControlTerminal(ctx, info.ID, workspace.ClaimFree); !errors.Is(err, workspace.ErrTerminalControl) {
		t.Fatal("a plain claim took an absent controller's lease", err)
	}
	lease, err := secondView.ControlTerminal(ctx, info.ID, workspace.ClaimFromAbsent)
	if err != nil || lease.Owner.ClientID != "terminal-second-window" {
		t.Fatal("a closed window's retained lease locked out the window in use", lease, err)
	}
}
