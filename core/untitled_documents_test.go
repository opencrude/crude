package core

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"crude/coreprotocol"
	"crude/document"
)

func TestConnectedUntitledDocumentSharesTextWithoutCreatingAFile(t *testing.T) {
	r, root := sharedDocumentRuntime(t)
	before, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	a, ws := resourceConnection(t, r, root, "untitled-author")
	b, _ := resourceConnection(t, r, root, "untitled-peer")
	var first, peer document.Snapshot
	decodeResource(t, callCore(t, a, "create", ws, coreprotocol.DocumentCreateUntitled, coreprotocol.Empty{}), &first)
	if first.ID == "" || first.Path != "" || first.Name == "" || first.Dirty {
		t.Fatalf("new document has incorrect identity or state: %+v", first)
	}
	decodeResource(t, callCore(t, b, "open", ws, coreprotocol.DocumentOpenUntitled, coreprotocol.DocumentReadRequest{ID: first.ID}), &peer)
	if peer.ID != first.ID || peer.Name != first.Name || peer.Path != "" {
		t.Fatalf("peer opened a different document: %+v", peer)
	}
	var receipt document.Receipt
	decodeResource(t, callCore(t, a, "edit", ws, coreprotocol.DocumentEdit, coreprotocol.DocumentEditRequest{
		ID: first.ID, Revision: first.Revision, Edits: []document.Edit{{Text: "shared draft"}},
	}), &receipt)
	var sync document.SyncResult
	decodeResource(t, callCore(t, b, "sync", ws, coreprotocol.DocumentSync, coreprotocol.DocumentSyncRequest{ID: first.ID}), &sync)
	if !receipt.Accepted || sync.Snapshot.ID != first.ID || sync.Snapshot.Name != first.Name ||
		string(sync.Snapshot.Text) != "shared draft" || !sync.Snapshot.Dirty {
		t.Fatalf("peer missed shared text: receipt=%+v sync=%+v", receipt, sync)
	}
	after, err := os.ReadDir(root)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("untitled work changed the project directory: before=%v after=%v error=%v", before, after, err)
	}
}

func TestConnectedFirstSaveKeepsDocumentIdentityAndReceipt(t *testing.T) {
	r, root := sharedDocumentRuntime(t)
	a, ws := resourceConnection(t, r, root, "untitled-saver")
	var created document.Snapshot
	decodeResource(t, callCore(t, a, "create", ws, coreprotocol.DocumentCreateUntitled, coreprotocol.Empty{}), &created)
	var edit document.Receipt
	decodeResource(t, callCore(t, a, "edit", ws, coreprotocol.DocumentEdit, coreprotocol.DocumentEditRequest{
		ID: created.ID, Revision: created.Revision, Edits: []document.Edit{{Text: "save me"}},
	}), &edit)
	target := filepath.Join(root, "saved.txt")
	input := coreprotocol.DocumentSaveUntitledRequest{ID: created.ID, Revision: edit.Revision, Directory: root, Name: "saved.txt"}
	response := callCore(t, a, "save-exact", ws, coreprotocol.DocumentSaveUntitled, input)
	var saved document.Snapshot
	decodeResource(t, response, &saved)
	if saved.ID != created.ID || saved.Path != target || saved.Dirty {
		t.Fatalf("first save replaced document identity: %+v", saved)
	}
	var receipt coreprotocol.Receipt
	decodeResource(t, callCore(t, a, "receipt", "", coreprotocol.GetReceipt, coreprotocol.ReceiptRequest{RequestID: "save-exact"}), &receipt)
	if receipt.State != "completed" || receipt.Method != coreprotocol.DocumentSaveUntitled {
		t.Fatalf("first save has no exact result receipt: %+v", receipt)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "save me" {
		t.Fatalf("wrong first-save bytes: %q %v", data, err)
	}
}
