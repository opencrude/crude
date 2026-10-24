package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/document"
	"crude/types"
	"crude/workspace"
)

// Inspect actual server frames without replacing either endpoint or its dispatch.
type largeDocumentWire struct {
	net.Conn
	mu              sync.Mutex
	buffer          []byte
	chunks, largest int
}

// Read observes the carrier's frames as the client receives them: every
// payload must arrive in bounded data frames, never as one giant write.
func (w *largeDocumentWire) Read(p []byte) (int, error) {
	n, err := w.Conn.Read(p)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buffer = append(w.buffer, p[:n]...)
	for {
		_, a := binary.Uvarint(w.buffer)
		if a <= 0 || len(w.buffer) < a+1 {
			break
		}
		kind := w.buffer[a]
		size, b := binary.Uvarint(w.buffer[a+1:])
		if b <= 0 || len(w.buffer) < a+1+b+int(size) {
			break
		}
		if kind == 2 { // data
			w.chunks++
			w.largest = max(w.largest, int(size))
		}
		w.buffer = w.buffer[a+1+b+int(size):]
	}
	return n, err
}

func TestLargeDocumentAcceptanceRuntimeWireSnapshotReceipt(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	root, journal := t.TempDir(), filepath.Join(t.TempDir(), "receipts.db")
	factory := func(root string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(root, "", "acceptance", types.Settings{}, coreTestLogger{})
		ws.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
		return ws, nil
	}
	r, err := New(factory, journal)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	want := bytes.Repeat([]byte("0123456789abcdef\n"), (16<<20)/17+1)[:16<<20]
	path := filepath.Join(root, "large.txt")
	if err := os.WriteFile(path, want, 0600); err != nil {
		t.Fatal(err)
	}
	// Deadline each request independently. One shared clock made race-instrumented
	// inspection of an earlier 44 MiB snapshot consume later requests' budgets.
	// This is protocol/correctness coverage, not a normal-build latency ceiling.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := time.Now()
	peer, server := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- ServeConnection(ctx, r, server) }()
	wire := &largeDocumentWire{Conn: peer}
	c, err := coreclient.Dial(ctx, coreclient.SingleCarrier(wire), "large-document-shell")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	call := func(workspaceID, method string, in coreprotocol.Value, out coreprotocol.Target) error {
		requestCtx, stop := context.WithTimeout(ctx, 60*time.Second)
		defer stop()
		return c.Call(requestCtx, workspaceID, method, in, out)
	}
	callRequest := func(request coreprotocol.Request) (coreprotocol.Response, error) {
		requestCtx, stop := context.WithTimeout(ctx, 60*time.Second)
		defer stop()
		return c.CallRequest(requestCtx, request)
	}
	var attached coreprotocol.AttachResult
	if err := call("", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &attached); err != nil {
		t.Fatal(err)
	}
	openPayload, err := coreprotocol.Marshal(coreprotocol.Ptr(coreprotocol.DocumentOpenRequest{Path: path}))
	if err != nil {
		t.Fatal(err)
	}
	openRequest := coreprotocol.Request{ID: "large-open-exact-receipt", Owner: c.Owner(), WorkspaceID: attached.WorkspaceID, Method: coreprotocol.DocumentOpen, Payload: openPayload}
	openResponse, err := callRequest(openRequest)
	if err != nil || openResponse.Error != nil {
		t.Fatalf("open: %v %v", err, openResponse.Error)
	}
	t.Logf("large open completed in %v", time.Since(started))
	var opened document.Snapshot
	if err := coreprotocol.Decode(openResponse.Payload, &opened); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened.Text, want) || !bytes.Equal(opened.Saved, want) || opened.Dirty {
		t.Fatal("16 MiB open snapshot lost byte identity")
	}
	var synced document.SyncResult
	if err := call(attached.WorkspaceID, coreprotocol.DocumentSync, coreprotocol.DocumentSyncRequest{ID: opened.ID}, &synced); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(synced.Snapshot.Text, want) || !bytes.Equal(synced.Snapshot.Saved, want) {
		t.Fatal("16 MiB sync lost byte identity")
	}
	payload, err := coreprotocol.Marshal(coreprotocol.Ptr(coreprotocol.DocumentEditRequest{ID: opened.ID, Revision: opened.Revision, Group: "acceptance", Edits: []document.Edit{{Start: 0, End: 1, Text: "X"}}}))
	if err != nil {
		t.Fatal(err)
	}
	request := coreprotocol.Request{ID: "large-edit-exact-receipt", Owner: c.Owner(), WorkspaceID: attached.WorkspaceID, Method: coreprotocol.DocumentEdit, Payload: payload}
	response, err := callRequest(request)
	if err != nil || response.Error != nil {
		t.Fatalf("large mutation response: %v %v", err, response.Error)
	}
	var editReceipt document.Receipt
	if err := coreprotocol.Decode(response.Payload, &editReceipt); err != nil {
		t.Fatal(err)
	}
	if !editReceipt.Accepted || editReceipt.RequestID != request.ID || editReceipt.DocumentID != opened.ID {
		t.Fatal("edit not accepted under original identity")
	}
	if err := call(attached.WorkspaceID, coreprotocol.DocumentSync, coreprotocol.DocumentSyncRequest{ID: opened.ID}, &synced); err != nil {
		t.Fatal(err)
	}
	edited := synced.Snapshot
	editedWant := append([]byte(nil), want...)
	editedWant[0] = 'X'
	if !bytes.Equal(edited.Text, editedWant) || !bytes.Equal(edited.Saved, want) || !edited.Dirty {
		t.Fatal("large mutation snapshot corrupted")
	}
	var receipt coreprotocol.Receipt
	if err := call("", coreprotocol.GetReceipt, coreprotocol.ReceiptRequest{RequestID: request.ID}, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.State != "completed" || !bytes.Equal(receipt.Response.Payload, response.Payload) {
		t.Fatal("large receipt differs from exact response")
	}
	if err := call("", coreprotocol.GetReceipt, coreprotocol.ReceiptRequest{RequestID: openRequest.ID}, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.State != "completed" || !bytes.Equal(receipt.Response.Payload, openResponse.Payload) {
		t.Fatal("giant open receipt differs from exact snapshot")
	}
	// A request beyond the message limit fails in the client BEFORE anything
	// is sent, preserving the live document and leaving the session usable.
	oversize, err := coreprotocol.Marshal(coreprotocol.Ptr(coreprotocol.DocumentEditRequest{ID: edited.ID, Revision: edited.Revision, Edits: []document.Edit{{Text: strings.Repeat("p", corewire.MaxMessage)}}}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = callRequest(coreprotocol.Request{ID: "oversize-not-admitted", Owner: c.Owner(), WorkspaceID: attached.WorkspaceID, Method: coreprotocol.DocumentEdit, Payload: oversize})
	if !errors.Is(err, corewire.ErrMessageTooLarge) {
		t.Fatalf("oversized paste did not fail before admission: %v", err)
	}
	if err := call(attached.WorkspaceID, coreprotocol.DocumentSync, coreprotocol.DocumentSyncRequest{ID: edited.ID}, &synced); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(synced.Snapshot.Text, editedWant) || synced.Snapshot.Revision != edited.Revision {
		t.Fatal("rejected paste had effects")
	}
	disk, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(disk, want) {
		t.Fatal("unsaved acceptance mutation wrote disk", err)
	}
	wire.mu.Lock()
	chunks, largest := wire.chunks, wire.largest
	wire.mu.Unlock()
	if chunks < 1000 || largest > 16<<10 {
		t.Fatalf("unbounded or unchunked delivery: chunks=%d largest=%d", chunks, largest)
	}
	t.Logf("16 MiB snapshots and receipt delivered in %d bounded data frames; largest=%d bytes", chunks, largest)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	// Reopen the actual SQLite journal; a giant successful response is durable,
	// not just retained in the running actor or client.
	r, err = New(factory, journal)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := r.receipts.lookup(ctx, openRequest.Owner.ClientID, openRequest.ID)
	if err != nil || persisted.State != "completed" || !bytes.Equal(persisted.Response.Payload, openResponse.Payload) {
		t.Fatal("large response receipt did not survive journal reopen", err)
	}
}
