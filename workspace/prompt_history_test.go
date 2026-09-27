package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/history"
	"crude/sessiondb"
)

func TestRestorePromptRebindsCollidingLabelsAndPreservesBinary(t *testing.T) {
	db, dir := setupRefStoreDB(t)
	t.Cleanup(func() { _ = db.Close() })
	store := NewReferenceStore("sess1", dir, db)
	path := filepath.Join(dir, "picture.png")
	if err := os.WriteFile(path, []byte("image contents"), 0600); err != nil {
		t.Fatal(err)
	}
	existing, err := store.CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedImage, FilePath: path})
	if err != nil {
		t.Fatal(err)
	}
	old := existing
	old.ID, old.SessionID, old.Mime = "other-session-image", "source", "image/png"
	text := "inspect " + old.DisplayName() + " and array[0]"
	entry := history.Entry{Text: text, Attachments: []history.Attachment{{Start: 8, End: 8 + len([]rune(old.DisplayName())), Reference: old}}}
	got, ids, err := store.RestorePrompt(entry)
	if err != nil {
		t.Fatal(err)
	}
	if got == text || !strings.Contains(got, "array[0]") || len(ids) != 1 {
		t.Fatalf("collision not rebound or literal brackets changed: %q", got)
	}
	refs := store.ListReferences()
	if len(refs) != 2 {
		t.Fatalf("restore did not import an independent reference: %+v", refs)
	}
	for _, ref := range refs {
		if ref.ID == existing.ID {
			continue
		}
		if ref.SessionID != "sess1" || ref.FilePath == path || ref.Mime != "image/png" || !strings.Contains(got, ref.DisplayName()) {
			t.Fatalf("restored attachment is not sendable in destination: %+v", ref)
		}
		data, err := os.ReadFile(ref.FilePath)
		if err != nil || string(data) != "image contents" {
			t.Fatalf("session-owned snapshot was not retained: %q %v", data, err)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.RestorePrompt(entry); err == nil || len(store.ListReferences()) != 2 {
		t.Fatal("missing media must fail before changing references")
	}
}

func TestRestorePromptLabelsCannotCollideWithLiteralProse(t *testing.T) {
	probe := NewReferenceStore("probe", t.TempDir(), nil)
	generated, err := probe.CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedText, Content: "probe"})
	if err != nil {
		t.Fatal(err)
	}
	store := NewReferenceStore("destination", t.TempDir(), nil)
	old := sessiondb.Reference{ID: "source", Kind: sessiondb.KindPastedText, Label: "[source]", Content: "original"}
	entry := history.Entry{Text: "[source] and literal " + generated.DisplayName(), Attachments: []history.Attachment{{Start: 0, End: 8, Reference: old}}}
	text, ids, err := store.RestorePrompt(entry)
	if err != nil || len(ids) != 1 {
		t.Fatalf("restore failed: %q %v %v", text, ids, err)
	}
	ref, ok := store.GetReference(ids[0])
	if !ok || ref.DisplayName() == generated.DisplayName() || strings.Count(text, ref.DisplayName()) != 1 || !strings.Contains(text, "literal "+generated.DisplayName()) {
		t.Fatalf("restored chip consumed literal prose: %q %+v", text, ref)
	}
}
