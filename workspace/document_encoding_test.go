package workspace

import (
	"bytes"
	"crude/textencoding"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"crude/document"
)

func TestUntitledEncodingSurvivesSaveAutosaveReconcileAndRestart(t *testing.T) {
	for _, encoding := range []textencoding.Encoding{textencoding.UTF8, textencoding.UTF8BOM, textencoding.UTF16LE, textencoding.UTF16BE} {
		t.Run(encoding.Label(), func(t *testing.T) {
			f := newReviewedFixture(t)
			s := f.documents
			saves := NewDocumentSaveService()
			t.Cleanup(saves.Close)
			s.mu.Lock()
			s.saves = saves
			s.mu.Unlock()
			draft, err := s.CreateUntitled(f.owner, textencoding.UTF8)
			if err != nil {
				t.Fatal(err)
			}
			text := "café 😀\r\n"
			edit := documentEdit(t, s, f.owner, draft.ID, "type", draft.Revision, 0, 0, text)
			target := filepath.Join(filepath.Dir(f.path), "encoded.txt")
			saved, err := s.SaveUntitled(t.Context(), f.owner, draft.ID, edit.Revision, target, encoding)
			if err != nil || saved.Encoding != encoding || saved.Dirty || saved.ID != draft.ID || string(saved.Text) != text {
				t.Fatalf("first save = %+v, %v", saved, err)
			}
			want, err := textencoding.EncodeText([]byte(text), encoding)
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(got, want) || saved.DiskHash != sha256.Sum256(want) {
				t.Fatalf("disk bytes = %x, %v; want %x", got, err, want)
			}
			edit = documentEdit(t, s, f.owner, draft.ID, "later", saved.Revision, 0, 0, "x")
			saved, err = s.Save(t.Context(), f.owner, draft.ID, edit.Revision, DocumentSaveAfterDelay)
			want, _ = textencoding.EncodeText([]byte("x"+text), encoding)
			got, readErr := os.ReadFile(target)
			if err != nil || readErr != nil || !bytes.Equal(got, want) || saved.Encoding != encoding || saved.Dirty {
				t.Fatalf("autosave changed the encoding: %+v %x %v %v", saved, got, err, readErr)
			}
			// A clean external edit retains decoded text, raw-byte conflict
			// evidence and the detected encoding through the same watcher owner.
			external, _ := textencoding.EncodeText([]byte("external é\n"), encoding)
			if err := os.WriteFile(target, external, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := s.ReconcileDisk(t.Context(), draft.ID); err != nil {
				t.Fatal(err)
			}
			saved, err = s.Snapshot(draft.ID)
			if err != nil || string(saved.Text) != "external é\n" || saved.Encoding != encoding || saved.Conflict || saved.Dirty {
				t.Fatalf("reconciled = %+v %v", saved, err)
			}
			db, root := s.stateDB, s.root
			s.Close()
			restarted := NewDocumentService(root, nil, nil, db)
			defer restarted.Close()
			reopened, err := restarted.OpenUntitled(f.owner, draft.ID)
			if err != nil || reopened.ID != draft.ID || reopened.Encoding != encoding || string(reopened.Text) != "external é\n" {
				t.Fatalf("restart lost encoding or identity: %+v %v", reopened, err)
			}
			// With no checkpoint at all the BOM still gives an exact decode.
			fresh := NewDocumentService(root, nil, nil)
			defer fresh.Close()
			reopened, err = fresh.Open(f.owner, target)
			if err != nil || reopened.Encoding != encoding || string(reopened.Text) != "external é\n" {
				t.Fatalf("fresh open = %+v %v", reopened, err)
			}
		})
	}
}

func TestEncodingChoiceFenceDoesNotFollowAnotherConversion(t *testing.T) {
	f := newReviewedFixture(t)
	snapshot, err := f.documents.Snapshot(f.path)
	if err != nil {
		t.Fatal(err)
	}
	selected := snapshot.Encoding
	converted, err := f.documents.SaveWithEncoding(t.Context(), f.owner, snapshot.ID, snapshot.Revision, textencoding.UTF16LE, f.store, f.files)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	// A peer can change disk encoding without changing the text revision.
	// A save that already selected the old encoding must not silently follow it.
	_, err = f.documents.save(t.Context(), f.owner, snapshot.ID, snapshot.Revision, DocumentSaveManual, &selected)
	if !errors.Is(err, document.ErrRevision) {
		t.Fatalf("stale encoding choice was accepted: %v", err)
	}
	after, err := os.ReadFile(f.path)
	if err != nil || !bytes.Equal(before, after) || converted.Encoding != textencoding.UTF16LE {
		t.Fatal("the stale choice changed the converted file")
	}
}

func TestUntitledEncodedReplacementUsesExactPreimageAndOptionalComparison(t *testing.T) {
	f := newReviewedFixture(t)
	target := filepath.Join(filepath.Dir(f.path), "target.txt")
	before := []byte{0xfe, 0xff, 0, 'o', 0, 'l', 0, 'd'}
	if err := os.WriteFile(target, before, 0o600); err != nil {
		t.Fatal(err)
	}
	draft, err := f.documents.CreateUntitled(f.owner, textencoding.UTF8)
	if err != nil {
		t.Fatal(err)
	}
	edit := documentEdit(t, f.documents, f.owner, draft.ID, "edit", draft.Revision, 0, 0, "new")
	preview, err := f.documents.PrepareUntitledOverwrite(t.Context(), f.owner, draft.ID, edit.Revision, target, textencoding.UTF16LE)
	if err != nil {
		t.Fatal(err)
	}
	comparison, err := f.documents.ReadUntitledOverwrite(t.Context(), f.owner, preview.ID, preview.Digest)
	if err != nil || string(comparison.Before) != "old" || string(comparison.After) != "new" {
		t.Fatalf("comparison = %+v, %v", comparison, err)
	}
	if _, err := f.documents.ReadUntitledOverwrite(t.Context(), document.Owner{ClientID: "peer", Epoch: 1}, preview.ID, preview.Digest); err == nil {
		t.Fatal("another attachment read the private comparison")
	}
	result := f.documents.ApplyReviewedEdit(t.Context(), f.owner, preview.ID, preview.Digest, f.store, f.files)
	if result.State != "committed" {
		t.Fatal(result)
	}
	after := []byte{0xff, 0xfe, 'n', 0, 'e', 0, 'w', 0}
	got, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(got, after) {
		t.Fatalf("replacement bytes = %x, %v", got, err)
	}
	snap, err := f.documents.Snapshot(draft.ID)
	if err != nil || snap.Encoding != textencoding.UTF16LE || string(snap.Text) != "new" || snap.Dirty {
		t.Fatalf("replacement changed canonical text: %+v %v", snap, err)
	}
	safety, err := f.store.LoadRetainedSnapshot(t.Context(), preview.ID)
	if err != nil || safety.Entries["target.txt"].Hash != reviewedHash(string(before)) {
		t.Fatalf("replacement did not retain exact encoded bytes: %+v %v", safety, err)
	}
}

func TestNamedEncodingConversionPreservesDraftUndoAndSafety(t *testing.T) {
	f := newReviewedFixture(t)
	before, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := f.documents.Snapshot(f.path)
	if err != nil {
		t.Fatal(err)
	}
	receipt := documentEdit(t, f.documents, f.owner, snap.ID, "accent", snap.Revision, 0, 0, "café 😀\r\n")
	original := documentText(t, f.documents, snap.ID, "café 😀\r\n"+string(before))
	saved, err := f.documents.SaveWithEncoding(t.Context(), f.owner, snap.ID, receipt.Revision, textencoding.UTF16BE, f.store, f.files)
	if err != nil || saved.Dirty || saved.Encoding != textencoding.UTF16BE || saved.SavedEncoding != saved.Encoding ||
		saved.ID != snap.ID || !bytes.Equal(saved.Text, original.Text) {
		t.Fatalf("conversion = %+v, %v", saved, err)
	}
	want, _ := textencoding.EncodeText(original.Text, textencoding.UTF16BE)
	actual, err := os.ReadFile(f.path)
	if err != nil || !bytes.Equal(actual, want) {
		t.Fatalf("converted bytes = %x, %v", actual, err)
	}
	entries, err := f.store.FileHistory(filepath.Base(f.path), 20)
	if err != nil || len(entries) == 0 {
		t.Fatalf("conversion omitted Local History: %+v, %v", entries, err)
	}
	undo, err := f.documents.Undo(f.owner, snap.ID, "undo-accent", saved.Revision)
	if err != nil || !undo.Accepted {
		t.Fatal("conversion cleared text undo:", err)
	}
	documentText(t, f.documents, snap.ID, string(before))
}
