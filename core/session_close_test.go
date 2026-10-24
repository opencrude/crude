package core

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"crude/buildinfo"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/document"
	"crude/workspace"
)

func TestSessionCleanCloseReleasesResourcesBeforeReturning(t *testing.T) {
	r, root := resourceRuntime(t)
	first := newResourceWire(t, r, "closing-owner")
	peer := newResourceWire(t, r, "remaining-owner")
	var attached, peerAttached coreprotocol.AttachResult
	first.call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &attached)
	peer.call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &peerAttached)
	var opened document.Snapshot
	first.call(t, attached.WorkspaceID, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: "unsaved.txt"}, &opened)
	var terminal workspace.TerminalInfo
	first.call(t, attached.WorkspaceID, coreprotocol.TerminalCreate, workspace.TerminalCreateOpts{Command: "/bin/sh", Args: []string{"-c", "cat"}, Cols: 40, Rows: 4}, &terminal)
	var lease workspace.TerminalLease
	first.call(t, attached.WorkspaceID, coreprotocol.TerminalControl, coreprotocol.TerminalIDRequest{ID: terminal.ID}, &lease)
	r.mu.Lock()
	retained := r.clients[first.owner.ClientID]
	r.mu.Unlock()
	if err := first.c.Close(); err != nil {
		t.Fatal("clean ending was not acknowledged", err)
	}
	if r.ClientCount() != 1 {
		t.Fatal("clean close retained the closing owner", r.ClientCount())
	}
	retained.mu.Lock()
	attachments := len(retained.attached)
	retained.mu.Unlock()
	if attachments != 0 {
		t.Fatal("cleanup receipt preceded attachment release")
	}
	// Another owner can acquire the lease immediately; no grace expiry, sleep,
	// runtime close or explicit release is needed to recover input authority.
	var next workspace.TerminalLease
	peer.call(t, attached.WorkspaceID, coreprotocol.TerminalControl, coreprotocol.TerminalIDRequest{ID: terminal.ID}, &next)
	if next.Owner.ClientID != peer.owner.ClientID || next.Owner.Epoch != peer.owner.Epoch || next.Generation <= lease.Generation {
		t.Fatalf("closing owner retained terminal authority: old=%+v next=%+v", lease, next)
	}
	var stillOpen document.Snapshot
	peer.call(t, attached.WorkspaceID, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: "unsaved.txt"}, &stillOpen)
	if stillOpen.ID != opened.ID {
		t.Fatal("clean close destroyed the retained document")
	}
}

func TestSessionRawCarrierLossRetainsOwner(t *testing.T) {
	r := transportRuntime(t)
	a, b := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- ServeConnection(t.Context(), r, b) }()
	carrier := corewire.NewMux(a, corewire.CarrierLocal, true)
	c, err := coreclient.Dial(t.Context(), func(context.Context) (corewire.Conn, error) { return carrier, nil }, "crashed-client")
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	retained := r.clients[c.Owner().ClientID]
	r.mu.Unlock()
	_ = carrier.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("carrier server did not join")
	}
	if r.ClientCount() != 1 {
		t.Fatal("carrier loss ended the resumable session")
	}
	select {
	case <-retained.Done():
		t.Fatal("carrier loss revoked its owner")
	default:
	}
	// Stop this deliberately non-resumable SDK without allowing another dial.
	_ = c.Close()
}

func TestSessionCoreCloseIsConsumedAndConcurrentCloseJoins(t *testing.T) {
	r := transportRuntime(t)
	a, b := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- ServeConnection(t.Context(), r, b) }()
	c, err := coreclient.Dial(t.Context(), coreclient.SingleCarrier(a), "core-ended-client")
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	retained := r.clients[c.Owner().ClientID]
	r.mu.Unlock()
	var joined sync.WaitGroup
	for range 8 {
		joined.Add(1)
		go func() { defer joined.Done(); retained.Close() }()
	}
	joined.Wait()
	if err := c.Close(); err != nil || !errors.Is(c.Err(), coreclient.ErrSessionLost) {
		t.Fatalf("core ending was lost: close=%v client=%v", err, c.Err())
	}
	select {
	case <-retained.closeComplete:
	default:
		t.Fatal("concurrent Close returned before cleanup completed")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("control reader did not join")
	}
}

func TestSessionCoreCloseBoundsUnresponsivePeer(t *testing.T) {
	r := transportRuntime(t)
	a, b := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- ServeConnection(t.Context(), r, b) }()
	carrier := corewire.NewMux(a, corewire.CarrierLocal, true)
	defer carrier.Close()
	control, err := carrier.OpenStream(t.Context(), corewire.ClassControl)
	if err != nil {
		t.Fatal(err)
	}
	if err := send(control, &coreprotocol.Hello{Schema: coreprotocol.Schema, Build: buildinfo.ProtocolBuild, ClientID: "unresponsive-peer"}); err != nil {
		t.Fatal(err)
	}
	if _, err := corewire.NewReader(control, 1<<16).Next(); err != nil {
		t.Fatal(err)
	}
	// The live peer never consumes SessionEnd and never acknowledges by closing.
	closed := make(chan error, 1)
	go func() { closed <- r.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("unresponsive peer blocked runtime close")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("unresponsive control reader was not joined")
	}
}
