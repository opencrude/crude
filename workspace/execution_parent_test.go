package workspace

import (
	"crude/agent/worktree"
	"crude/workspace/document"
	"testing"
)

func TestParentExecutionDoesNotOverlayLogicalDocuments(t *testing.T) {
	s, _, root := executionFixture(t)
	owner := document.Owner{ClientID: "human", Epoch: 1}
	snap, err := s.documents.Open(owner, "live.txt")
	if err != nil {
		t.Fatal(err)
	}
	documentEdit(t, s.documents, owner, snap.ID, "dirty", snap.Revision, 0, 3, "LOGICAL")
	a := executionCreate(t, s)
	executionPut(t, a.Root, "live.txt", "PARENT\ntwo\nthree\n")
	b, err := s.Create(t.Context(), worktree.Request{Owner: worktree.Owner{AgentID: "child", RunID: "child-run"}, Mode: worktree.Isolated, SourceContextID: a.ID})
	if err != nil {
		t.Fatal(err)
	}
	diskText(t, b.Root, "live.txt", "PARENT\ntwo\nthree\n")
	executionPut(t, b.Root, "live.txt", "PARENT\nCHILD\nthree\n")
	p, err := s.Service.Review(t.Context(), b.ID)
	if err != nil || len(p.Changes) != 1 || string(p.Changes[0].Before.Data) != "PARENT\ntwo\nthree\n" {
		t.Fatalf("delta %+v %v", p, err)
	}
	documentText(t, s.documents, snap.ID, "LOGICAL\ntwo\nthree\n")
	diskText(t, root, "live.txt", "one\ntwo\nthree\n")
}

func TestFileCatalogStaysInItsManagedCheckout(t *testing.T) {
	s, _, root := executionFixture(t)
	c := executionCreate(t, s)
	executionPut(t, c.Root, "only-in-worktree.go", "here")
	catalog, err := CatalogExecutionFiles(t.Context(), s, root, c.ID, BrowserPathRequest{})
	if err != nil || catalog.Root != c.Root {
		t.Fatal(catalog.Root, err)
	}
	found := false
	for _, e := range catalog.Entries {
		if e.Name == "only-in-worktree.go" {
			found = true
		}
	}
	if !found {
		t.Fatal("catalog read the logical checkout")
	}
	if _, err := CatalogExecutionFiles(t.Context(), s, root, "unknown", BrowserPathRequest{}); err == nil {
		t.Fatal("unknown checkout silently changed search roots")
	}
}
