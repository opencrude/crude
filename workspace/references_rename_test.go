package workspace

import (
	"testing"

	"crude/sessiondb"
)

// TestReferenceStoreRenameReference guards the attachment-rename plumbing:
// bare names are bracket-wrapped, collisions get a numeric suffix, deleted
// references never block uniqueness, and unknown/deleted IDs are rejected.
func TestReferenceStoreRenameReference(t *testing.T) {
	s := NewReferenceStore("sess", t.TempDir(), nil)
	refA, err := s.CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindSelectedFile, Title: "a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindSelectedFile, Title: "b.txt"}); err != nil {
		t.Fatal(err)
	}

	// Bare name is wrapped in brackets and persisted into the stored ref.
	label, ok := s.RenameReference(refA.ID, "api spec")
	if !ok || label != "[api spec]" {
		t.Fatalf("rename = %q ok=%v, want [api spec]", label, ok)
	}
	got, _ := s.GetReference(refA.ID)
	if got.Label != "[api spec]" || got.DisplayName() != "[api spec]" {
		t.Fatalf("stored label = %q", got.Label)
	}

	// Colliding with another active reference uniquifies instead of failing.
	sibling := s.ListReferences()
	var siblingID string
	for _, r := range sibling {
		if r.ID != refA.ID {
			siblingID = r.ID
			break
		}
	}
	siblingLabel, _ := s.GetReference(siblingID)
	label, ok = s.RenameReference(refA.ID, siblingLabel.Label)
	if !ok || label == siblingLabel.Label {
		t.Fatalf("collision rename = %q ok=%v, want unique suffixed label ≠ %q", label, ok, siblingLabel.Label)
	}

	// Unknown ID is rejected.
	if _, ok := s.RenameReference("missing", "nope"); ok {
		t.Fatal("rename of unknown reference must fail")
	}

	// Deleted references are not renamable and do not block other labels.
	if err := s.DeleteReference(refA.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.RenameReference(refA.ID, "zombie"); ok {
		t.Fatal("deleted reference must not be renamable")
	}
	if _, ok := s.RenameReference(siblingID, "unique-name"); !ok {
		t.Fatal("rename of remaining reference must succeed")
	}
}
