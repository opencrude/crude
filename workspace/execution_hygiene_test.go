package workspace

import (
	"path/filepath"
	"strings"
	"testing"

	"crude/document"
	"crude/worktree"
)

func TestExecutionReviewAndCleanupPreserveUnsavedCheckoutDocuments(t *testing.T) {
	s, _, _ := executionFixture(t)
	c := executionCreate(t, s)
	owner := document.Owner{ClientID: "human", Epoch: 1}
	snap, err := s.documents.Open(owner, filepath.Join(c.Root, "live.txt"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.documents.Apply(document.Change{DocumentID: snap.ID, RequestID: "human-edit", Owner: owner, BaseRevision: snap.Revision, GroupID: "human", Edits: []document.Edit{{Start: 0, End: 3, Text: "HUMAN"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Unpin(c.ID, c.Owner); err != nil {
		t.Fatal(err)
	}
	for _, check := range []func() error{
		func() error { _, err := s.Review(t.Context(), c.ID); return err },
		func() error { _, err := s.ReviewDiscard(t.Context(), c.ID); return err },
		func() error { return s.Cleanup(t.Context(), c.ID, c.Owner, worktree.CleanupOptions{}) },
	} {
		if err := check(); err == nil || !strings.Contains(err.Error(), "unsaved") {
			t.Fatal("unsaved checkout document omitted", err)
		}
	}
	got, err := s.documents.Snapshot(snap.ID)
	if err != nil || string(got.Text) != "HUMAN\ntwo\nthree\n" {
		t.Fatal("accepted human work changed", err)
	}
}
