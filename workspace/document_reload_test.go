package workspace

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"testing"

	"crude/document"
)

// documentConflict edits the document, then changes the same line of its file
// outside Crude, which leaves unsaved text in conflict with the disk.
func documentConflict(t *testing.T) (*DocumentService, document.Snapshot, document.Owner) {
	t.Helper()
	s, snap, owner, _ := documentFixture(t, "one\ntwo\n")
	r := documentEdit(t, s, owner, snap.ID, "mine", snap.Revision, 0, 3, "ONE")
	if err := os.WriteFile(snap.Path, []byte("uno\nTWO\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileDisk(context.Background(), snap.ID); err != nil {
		t.Fatal(err)
	}
	current := documentText(t, s, snap.ID, "ONE\ntwo\n")
	if !current.Conflict || current.Revision != r.Revision {
		t.Fatalf("setup did not reach a disk conflict: %+v", current)
	}
	return s, current, owner
}

// Discard loads the file text and clears the conflict, as one undoable change.
func TestReloadDiscardSettlesDiskConflict(t *testing.T) {
	s, snap, owner := documentConflict(t)
	got, err := s.Reload(context.Background(), owner, snap.ID, "discard", snap.Revision, nil)
	if err != nil || string(got.Text) != "uno\nTWO\n" || got.Conflict || got.Dirty {
		t.Fatalf("discard: %q conflict=%v dirty=%v err=%v", got.Text, got.Conflict, got.Dirty, err)
	}
	if _, err := s.Undo(owner, snap.ID, "undo", got.Revision); err != nil {
		t.Fatal(err)
	}
	documentText(t, s, snap.ID, "ONE\ntwo\n")
}

// Keeping the text makes the changed file the save baseline, so Save writes
// the user's version over it.
func TestReloadKeepThenSaveOverwrites(t *testing.T) {
	s, snap, owner := documentConflict(t)
	got, err := s.Reload(context.Background(), owner, snap.ID, "keep", snap.Revision, &document.Resolution{})
	if err != nil || string(got.Text) != "ONE\ntwo\n" || got.Conflict || !got.Dirty {
		t.Fatalf("keep: %q conflict=%v dirty=%v err=%v", got.Text, got.Conflict, got.Dirty, err)
	}
	if _, err := s.Save(context.Background(), owner, snap.ID, got.Revision, DocumentSaveManual); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(snap.Path); string(data) != "ONE\ntwo\n" {
		t.Fatalf("file = %q", data)
	}
}

// A merged text replaces the document only against the exact disk version
// the user compared; a newer file is refused as a conflict.
func TestReloadReplaceRequiresTheReviewedDiskVersion(t *testing.T) {
	s, snap, owner := documentConflict(t)
	reviewed := sha256.Sum256([]byte("uno\nTWO\n"))
	if err := os.WriteFile(snap.Path, []byte("newer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := s.Reload(context.Background(), owner, snap.ID, "stale", snap.Revision, &document.Resolution{Text: []byte("ONE\nTWO\n"), Replace: true, DiskHash: reviewed})
	if !errors.Is(err, ErrDocumentConflict) {
		t.Fatalf("an unseen disk version must be refused, got %v", err)
	}
	documentText(t, s, snap.ID, "ONE\ntwo\n")
	current := sha256.Sum256([]byte("newer\n"))
	got, err := s.Reload(context.Background(), owner, snap.ID, "merge", snap.Revision, &document.Resolution{Text: []byte("ONE\nnewer\n"), Replace: true, DiskHash: current})
	if err != nil || string(got.Text) != "ONE\nnewer\n" || got.Conflict || !got.Dirty || string(got.Saved) != "newer\n" {
		t.Fatalf("merge: %q saved=%q conflict=%v err=%v", got.Text, got.Saved, got.Conflict, err)
	}
}
