package core

import (
	"crude/coreprotocol"
	"crude/document"
	"errors"
	"testing"
)

func TestLSPViewsCloseDetachNeverRevokesPeerDocument(t *testing.T) {
	r, root := sharedDocumentRuntime(t)
	a, ws := resourceConnection(t, r, root, "language-a")
	b, _ := resourceConnection(t, r, root, "language-b")
	callCore(t, a, "lsp-open", ws, coreprotocol.LSPOpenFile, coreprotocol.LSPRequest{Path: "new.go", Text: "untrusted local text"})
	callCore(t, b, "lsp-open", ws, coreprotocol.LSPOpenFile, coreprotocol.LSPRequest{Path: "new.go"})
	var snap document.Snapshot
	decodeResource(t, callCore(t, b, "doc-open", ws, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: "new.go"}), &snap)
	if len(snap.Text) != 0 {
		t.Fatal("legacy open overwrote authoritative disk bytes")
	}
	callCore(t, a, "lsp-close", ws, coreprotocol.LSPCloseFile, coreprotocol.LSPRequest{Path: "new.go"})
	a.Close()
	callCore(t, b, "barrier", ws, coreprotocol.Snapshot, struct{}{})
	r.mu.Lock()
	actor := r.workspaces[ws]
	r.mu.Unlock()
	if _, err := actor.ws.LiveDocuments.Sync(lspViewOwner(document.Owner{ClientID: b.Owner.ClientID, Epoch: b.Owner.Epoch}), snap.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := actor.ws.LiveDocuments.Sync(lspViewOwner(document.Owner{ClientID: a.Owner.ClientID, Epoch: a.Owner.Epoch}), snap.ID, 1); !errors.Is(err, document.ErrOwner) {
		t.Fatal(err)
	}
	if _, err := actor.ws.LiveDocuments.Sync(document.Owner{ClientID: b.Owner.ClientID, Epoch: b.Owner.Epoch}, snap.ID, 1); err != nil {
		t.Fatal(err)
	}
}
