package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
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

func (w *largeDocumentWire) Read(p []byte) (int, error) {
	n, err := w.Conn.Read(p)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buffer = append(w.buffer, p[:n]...)
	for len(w.buffer) >= 4 {
		size := int(binary.BigEndian.Uint32(w.buffer[:4]))
		if size > w.largest {
			w.largest = size
		}
		if size > corewire.MaxFrameSize || len(w.buffer) < size+4 {
			break
		}
		// Count frame metadata without decoding the same 256 KiB base64 data
		// a second time in this observer. The real Framer validates every byte.
		frame := w.buffer[4 : 4+size]
		if bytes.Contains(frame, []byte(`"kind":"response"`)) && bytes.Contains(frame, []byte(`"method":"payload.read"`)) {
			w.chunks++
		}
		w.buffer = w.buffer[size+4:]
	}
	return n, err
}

func TestLargeDocumentAcceptanceRuntimeWireSnapshotReceipt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
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
	c, err := coreclient.Dial(ctx, wire, "large-document-shell")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	call := func(workspaceID, method string, in, out any) error {
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
	openPayload, err := json.Marshal(coreprotocol.DocumentOpenRequest{Path: path})
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
	if err := json.Unmarshal(openResponse.Payload, &opened); err != nil {
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
	payload, err := json.Marshal(coreprotocol.DocumentEditRequest{ID: opened.ID, Revision: opened.Revision, Group: "acceptance", Edits: []document.Edit{{Start: 0, End: 1, Text: "X"}}})
	if err != nil {
		t.Fatal(err)
	}
	request := coreprotocol.Request{ID: "large-edit-exact-receipt", Owner: c.Owner(), WorkspaceID: attached.WorkspaceID, Method: coreprotocol.DocumentEdit, Payload: payload}
	response, err := callRequest(request)
	if err != nil || response.Error != nil {
		t.Fatalf("large mutation response: %v %v", err, response.Error)
	}
	var editReceipt document.Receipt
	if err := json.Unmarshal(response.Payload, &editReceipt); err != nil {
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
	// Large incoming pastes have no upload protocol: reject BEFORE admission,
	// preserve the live document and leave the connection usable.
	oversize, err := json.Marshal(coreprotocol.DocumentEditRequest{ID: edited.ID, Revision: edited.Revision, Edits: []document.Edit{{Text: strings.Repeat("p", 9<<20)}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = callRequest(coreprotocol.Request{ID: "oversize-not-admitted", Owner: c.Owner(), WorkspaceID: attached.WorkspaceID, Method: coreprotocol.DocumentEdit, Payload: oversize})
	if err == nil || err.Error() != "request frame too large" {
		t.Fatalf("large paste did not fail before admission: %v", err)
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
	if chunks < 2 || largest > corewire.MaxFrameSize {
		t.Fatalf("unbounded or unchunked delivery: chunks=%d largest=%d", chunks, largest)
	}
	t.Logf("16 MiB snapshots and receipt delivered in %d bounded chunks; largest encoded frame=%d", chunks, largest)
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
