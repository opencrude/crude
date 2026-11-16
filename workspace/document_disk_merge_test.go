package workspace

import (
	"context"
	"os"
	"testing"

	"crude/document"
)

// An external write that touches no unsaved line joins the text: the user's
// edits stay unsaved, the file becomes the save baseline and nothing blocks a
// save. Before, any external write to a changed file was a conflict that only
// Save or close revealed.
func TestDiskChangeMergesIntoUnsavedTextWhenChangesDoNotTouch(t *testing.T) {
	s, snap, a, _ := documentFixture(t, "one\ntwo\nthree\nfour\n")
	r := documentEdit(t, s, a, snap.ID, "mine", 1, 0, 3, "ONE")
	if err := os.WriteFile(snap.Path, []byte("one\ntwo\nthree\nFOUR\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileDisk(context.Background(), snap.ID); err != nil {
		t.Fatal(err)
	}
	current := documentText(t, s, snap.ID, "ONE\ntwo\nthree\nFOUR\n")
	if current.Conflict || !current.Dirty || string(current.Saved) != "one\ntwo\nthree\nFOUR\n" {
		t.Fatalf("merge must keep the edit unsaved over the new file: %+v", current)
	}
	sync, err := s.Sync(a, snap.ID, r.Revision)
	if err != nil || sync.Reset || len(sync.Changes) != 1 || sync.Changes[0].RequestID != document.DiskRequestID {
		t.Fatalf("the merge must be one disk change: %+v %v", sync, err)
	}
	if edits := sync.Changes[0].Edits; len(edits) != 1 || edits[0].Text != "FOUR" {
		t.Fatalf("the disk change must touch only the external line: %+v", edits)
	}
	saved, err := s.Save(context.Background(), a, snap.ID, current.Revision, DocumentSaveManual)
	if err != nil || saved.Dirty || saved.Conflict {
		t.Fatalf("a merged document must save normally: %+v %v", saved, err)
	}
	disk, _ := os.ReadFile(snap.Path)
	if string(disk) != "ONE\ntwo\nthree\nFOUR\n" {
		t.Fatalf("saved file lost a side: %q", disk)
	}
}

// Changes to the same line are the user's decision: both versions stay and a
// save fails closed. When the file later returns to text the edits do not
// touch, the conflict settles itself.
func TestOverlappingDiskChangeStaysAConflictUntilItMerges(t *testing.T) {
	s, snap, a, _ := documentFixture(t, "one\ntwo\nthree\n")
	documentEdit(t, s, a, snap.ID, "mine", 1, 4, 7, "TWO")
	if err := os.WriteFile(snap.Path, []byte("one\ntwo!\nthree\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileDisk(context.Background(), snap.ID); err != nil {
		t.Fatal(err)
	}
	current := documentText(t, s, snap.ID, "one\nTWO\nthree\n")
	if !current.Conflict || string(current.Saved) != "one\ntwo\nthree\n" {
		t.Fatalf("an overlapping change must keep both versions: %+v", current)
	}
	if _, err := s.Save(context.Background(), a, snap.ID, current.Revision, DocumentSaveManual); err == nil {
		t.Fatal("a conflicted document must not overwrite the external file")
	}
	if err := os.WriteFile(snap.Path, []byte("one\ntwo\nthree!\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileDisk(context.Background(), snap.ID); err != nil {
		t.Fatal(err)
	}
	current = documentText(t, s, snap.ID, "one\nTWO\nthree!\n")
	if current.Conflict || !current.Dirty {
		t.Fatalf("a later mergeable file must settle the conflict: %+v", current)
	}
}

// The same change on both sides is no conflict; when it was the only change
// the document is simply saved.
func TestIdenticalDiskChangeIsNoConflict(t *testing.T) {
	s, snap, a, _ := documentFixture(t, "one\ntwo\n")
	documentEdit(t, s, a, snap.ID, "mine", 1, 4, 7, "TWO")
	if err := os.WriteFile(snap.Path, []byte("one\nTWO\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileDisk(context.Background(), snap.ID); err != nil {
		t.Fatal(err)
	}
	if current := documentText(t, s, snap.ID, "one\nTWO\n"); current.Conflict || current.Dirty {
		t.Fatalf("identical text must be saved and clean: %+v", current)
	}
	documentEdit(t, s, a, snap.ID, "more", 2, 0, 3, "ONE")
	if err := os.WriteFile(snap.Path, []byte("one\nTWO\nthree\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileDisk(context.Background(), snap.ID); err != nil {
		t.Fatal(err)
	}
	if current := documentText(t, s, snap.ID, "ONE\nTWO\nthree\n"); current.Conflict || !current.Dirty {
		t.Fatalf("an appended line must merge with an unsaved edit: %+v", current)
	}
}

// A clean document follows the file with the smallest edits, so a caret,
// selection or fold away from the changed line does not move.
func TestCleanDiskReloadUsesMinimalEdits(t *testing.T) {
	s, snap, a, _ := documentFixture(t, "alpha\nbeta\ngamma\n")
	if err := os.WriteFile(snap.Path, []byte("alpha\nBETA\ngamma\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileDisk(context.Background(), snap.ID); err != nil {
		t.Fatal(err)
	}
	current := documentText(t, s, snap.ID, "alpha\nBETA\ngamma\n")
	if current.Dirty || current.Conflict {
		t.Fatalf("a clean reload must stay clean: %+v", current)
	}
	sync, err := s.Sync(a, snap.ID, snap.Revision)
	if err != nil || len(sync.Changes) != 1 || sync.Changes[0].RequestID != document.DiskRequestID {
		t.Fatalf("reload must be one disk change: %+v %v", sync, err)
	}
	if edits := sync.Changes[0].Edits; len(edits) != 1 || edits[0].Start != 6 || edits[0].End != 10 || edits[0].Text != "BETA" {
		t.Fatalf("reload must replace only the changed text: %+v", edits)
	}
}
