package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/document"
	"crude/types"
	"crude/workspace"
)

func sharedDocumentRuntime(t *testing.T) (*Runtime, string) {
	t.Helper()
	setCoreTestHome(t, t.TempDir())
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
	return r, t.TempDir()
}

func TestSharedDocumentsAuthoritativeBytesReceiptReplayAndDetach(t *testing.T) {
	r, root := sharedDocumentRuntime(t)
	path := filepath.Join(root, "shared.txt")
	if err := os.WriteFile(path, []byte("alpha\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a, ws := resourceConnection(t, r, root, "document-author")
	b, _ := resourceConnection(t, r, root, "document-survivor")
	var first, other document.Snapshot
	decodeResource(t, callCore(t, a, "open", ws, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: "shared.txt"}), &first)
	decodeResource(t, callCore(t, b, "open", ws, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: path}), &other)
	if first.ID != other.ID || string(first.Text) != "alpha\n" || string(other.Text) != "alpha\n" || first.Revision != other.Revision {
		t.Fatalf("not one authoritative document: %+v %+v", first, other)
	}
	// The subscriber follows actual document bytes/revisions, not the removed
	// workspace invalidation cursor followed by a read request.
	observer := newResourceWire(t, r, "document-topic-observer")
	var observerAttach coreprotocol.AttachResult
	observer.call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &observerAttach)
	var observerDocument document.Snapshot
	observer.call(t, ws, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: path}, &observerDocument)
	updates := make(chan coreprotocol.DocumentUpdate, 8)
	sub := observer.c.Subscribe(t.Context(), ws, coreprotocol.TopicDocument, first.ID, func(_ coreprotocol.TopicHeader, body []byte) error {
		var update coreprotocol.DocumentUpdate
		if err := coreprotocol.Decode(body, &update); err != nil {
			return err
		}
		select {
		case updates <- update:
			return nil
		case <-t.Context().Done():
			return t.Context().Err()
		}
	})
	defer sub.Close()
	next := func() coreprotocol.DocumentUpdate {
		t.Helper()
		select {
		case update := <-updates:
			return update
		case <-time.After(5 * time.Second):
			t.Fatal("document topic did not publish")
			return coreprotocol.DocumentUpdate{}
		}
	}
	if initial := next(); initial.Snapshot == nil || string(initial.Snapshot.Text) != "alpha\n" {
		t.Fatalf("invalid initial document: %+v", initial)
	}
	input := coreprotocol.DocumentEditRequest{ID: first.ID, Revision: first.Revision, Edits: []document.Edit{{Start: 0, End: 5, Text: "βeta"}}}
	response := callCore(t, a, "edit-once", ws, coreprotocol.DocumentEdit, input)
	var receipt document.Receipt
	decodeResource(t, response, &receipt)
	if !receipt.Accepted || receipt.RequestID != "edit-once" || receipt.DocumentID != first.ID || receipt.Before != first.Revision || receipt.Revision != first.Revision+1 {
		t.Fatalf("edit receipt lost identity: %+v", receipt)
	}
	change := next()
	if change.Snapshot != nil || change.Base != first.Revision || change.State.Revision != receipt.Revision || len(change.Changes) != 1 || len(change.Changes[0].Edits) != 1 || change.Changes[0].Edits[0].Text != "βeta" {
		t.Fatalf("pushed edit did not carry exact committed operation: %+v receipt=%+v", change, receipt)
	}
	read := func(c *Connection, id string) document.Snapshot {
		var snapshot document.Snapshot
		decodeResource(t, callCore(t, c, id, ws, coreprotocol.DocumentRead, coreprotocol.DocumentReadRequest{ID: first.ID}), &snapshot)
		return snapshot
	}
	if got := read(b, "read-edit"); string(got.Text) != "βeta\n" || !got.Dirty || got.Revision != receipt.Revision {
		t.Fatalf("survivor did not see real edited bytes: %+v", got)
	}
	raw, err := testPayload(input)
	if err != nil {
		t.Fatal(err)
	}
	stale := b.Call(context.Background(), coreprotocol.Request{ID: "stale-edit", Owner: b.Owner, WorkspaceID: ws, Method: coreprotocol.DocumentEdit, Payload: raw})
	if stale.Error == nil || !strings.Contains(stale.Error.Message, document.ErrRevision.Error()) {
		t.Fatalf("stale write was not rejected: %+v", stale)
	}
	var durable coreprotocol.Receipt
	decodeResource(t, callCore(t, a, "receipt", "", coreprotocol.GetReceipt, coreprotocol.ReceiptRequest{RequestID: "edit-once"}), &durable)
	if durable.ClientID != a.Owner.ClientID || durable.RequestID != "edit-once" || durable.WorkspaceID != ws || durable.Method != coreprotocol.DocumentEdit || durable.State != "completed" || string(durable.Response.Payload) != string(response.Payload) {
		t.Fatalf("durable source identity lost: %+v", durable)
	}
	// A reconnect recovers the original result; it must not execute the edit
	// again against the new epoch or mutate another owner's live bytes.
	a.Close()
	replacement, _ := resourceConnection(t, r, root, "document-author")
	if replacement.Owner.Epoch <= a.Owner.Epoch {
		t.Fatal("reconnect reused owner epoch")
	}
	replayed := callCore(t, replacement, "edit-once", ws, coreprotocol.DocumentEdit, input)
	if string(replayed.Payload) != string(response.Payload) || replayed.Revision != response.Revision {
		t.Fatalf("receipt replay changed result: %+v", replayed)
	}
	got := read(b, "after-detach")
	if string(got.Text) != "βeta\n" || got.Revision != receipt.Revision || !got.Dirty {
		t.Fatalf("detach/replay discarded dirty text: %+v", got)
	}
	callCore(t, b, "survivor-edit", ws, coreprotocol.DocumentEdit, coreprotocol.DocumentEditRequest{ID: first.ID, Revision: got.Revision, Edits: []document.Edit{{Start: len(got.Text), End: len(got.Text), Text: "survivor\n"}}})
	if got := read(b, "survivor-read"); string(got.Text) != "βeta\nsurvivor\n" || !got.Dirty {
		t.Fatalf("detached origin broke survivor editing: %+v", got)
	}
	disk, err := os.ReadFile(path)
	if err != nil || string(disk) != "alpha\n" {
		t.Fatalf("unsaved edits unexpectedly changed disk: %q %v", disk, err)
	}
}

func TestSharedDocumentsPendingFirstOpenRejectedAfterDisconnect(t *testing.T) {
	r, root := sharedDocumentRuntime(t)
	a, ws := resourceConnection(t, r, root, "pending-document-owner")
	b, _ := resourceConnection(t, r, root, "document-barrier")
	r.mu.Lock()
	actor := r.workspaces[ws]
	r.mu.Unlock()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	if !actor.resourceLane.submit(func(context.Context) { close(entered); <-release }) {
		t.Fatal("could not hold resource lane")
	}
	<-entered
	raw, err := coreprotocol.Marshal(coreprotocol.Ptr(coreprotocol.DocumentOpenRequest{Path: "pending.txt"}))
	if err != nil {
		t.Fatal(err)
	}
	result := a.Dispatch(context.Background(), coreprotocol.Request{ID: "pending-first-open", Owner: a.Owner, WorkspaceID: ws, Method: coreprotocol.DocumentOpen, Payload: raw})
	// Same-connection snapshot passes the queued resource job on the actor,
	// proving admission before disconnect without inspecting race-prone state.
	callCore(t, a, "admission-barrier", ws, coreprotocol.Snapshot, coreprotocol.Empty{})
	a.Close()
	callCore(t, b, "detach-barrier", ws, coreprotocol.Snapshot, coreprotocol.Empty{})
	unblock()
	select {
	case response := <-result:
		if response.Error == nil || !strings.Contains(response.Error.Message, document.ErrOwner.Error()) {
			t.Fatalf("pending first open revived disconnected document owner: error=%v payload=%s", response.Error, response.Payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pending document operation never settled")
	}
	var opened document.Snapshot
	decodeResource(t, callCore(t, b, "survivor-open", ws, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: "pending.txt"}), &opened)
	if opened.Revision != 1 || len(opened.Text) != 0 {
		t.Fatalf("rejected open altered retained document: %+v", opened)
	}
}
