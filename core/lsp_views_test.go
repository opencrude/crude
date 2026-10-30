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
	var snap document.Snapshot
	callCore(t, a, "doc-open", ws, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: "new.go"})
	decodeResource(t, callCore(t, b, "doc-open", ws, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: "new.go"}), &snap)
	if len(snap.Text) != 0 {
		t.Fatal("document open invented source bytes")
	}
	if snap.AnalysisGeneration == "" || snap.AnalysisLifetime == "" {
		t.Fatal("canonical source analysis identity was not published")
	}
	a.Close()
	callCore(t, b, "barrier", ws, coreprotocol.Snapshot, coreprotocol.Empty{})
	r.mu.Lock()
	actor := r.workspaces[ws]
	r.mu.Unlock()
	if _, err := actor.ws.LiveDocuments.Sync(document.Owner{ClientID: a.Owner.ClientID, Epoch: a.Owner.Epoch}, snap.ID, 1); !errors.Is(err, document.ErrOwner) {
		t.Fatal(err)
	}
	for _, removed := range []string{"lsp.open_file", "lsp.close_file", "lsp.clear_diagnostics"} {
		if lspMethod(removed) {
			t.Fatalf("competing source/diagnostic authority remains: %s", removed)
		}
	}
	if _, err := actor.ws.LiveDocuments.Sync(document.Owner{ClientID: b.Owner.ClientID, Epoch: b.Owner.Epoch}, snap.ID, 1); err != nil {
		t.Fatal(err)
	}
}
