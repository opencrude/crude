package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"crude/document"
)

func documentFixture(t *testing.T, text string) (*DocumentService, document.Snapshot, document.Owner, document.Owner) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "text.txt")
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	saves := NewDocumentSaveService()
	lsp := NewLSPManager(nil)
	s := NewDocumentService(root, saves, lsp)
	t.Cleanup(func() { s.Close(); lsp.Stop(); saves.Close() })
	a, b := document.Owner{ClientID: "a", Epoch: 1}, document.Owner{ClientID: "b", Epoch: 1}
	snap, err := s.Open(a, "text.txt")
	if err != nil {
		t.Fatal(err)
	}
	peer, err := s.Open(b, path)
	if err != nil || peer.ID != snap.ID {
		t.Fatalf("identity=%v err=%v", peer, err)
	}
	return s, snap, a, b
}
func documentEdit(t *testing.T, s *DocumentService, owner document.Owner, id, request string, revision uint64, start, end int, text string) document.Receipt {
	t.Helper()
	r, err := s.Apply(document.Change{Owner: owner, DocumentID: id, RequestID: request, BaseRevision: revision, Edits: []document.Edit{{Start: start, End: end, Text: text}}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func documentText(t *testing.T, s *DocumentService, id, want string) document.Snapshot {
	t.Helper()
	snap, err := s.Snapshot(id)
	if err != nil || string(snap.Text) != want {
		t.Fatalf("text=%q want=%q err=%v", snap.Text, want, err)
	}
	return snap
}

func TestDocumentServiceInterleavedUndoRedo(t *testing.T) {
	s, snap, a, b := documentFixture(t, "alpha beta gamma")
	r := documentEdit(t, s, a, snap.ID, "a1", 1, 0, 5, "ALPHA")
	r = documentEdit(t, s, b, snap.ID, "b1", r.Revision, 11, 16, "GAMMA!")
	u, err := s.Undo(a, snap.ID, "undo", r.Revision)
	if err != nil {
		t.Fatal(err)
	}
	documentText(t, s, snap.ID, "alpha beta GAMMA!")
	if _, err = s.Redo(a, snap.ID, "redo", u.Revision); err != nil {
		t.Fatal(err)
	}
	documentText(t, s, snap.ID, "ALPHA beta GAMMA!")
}
func TestDocumentServiceUndoRebasesShiftAndRejectsOverlap(t *testing.T) {
	s, snap, a, b := documentFixture(t, "one two three")
	r := documentEdit(t, s, a, snap.ID, "a1", 1, 8, 13, "THREE")
	r = documentEdit(t, s, b, snap.ID, "b1", r.Revision, 0, 3, "ONE!")
	u, err := s.Undo(a, snap.ID, "u1", r.Revision)
	if err != nil {
		t.Fatal(err)
	}
	documentText(t, s, snap.ID, "ONE! two three")
	r = documentEdit(t, s, a, snap.ID, "a2", u.Revision, 9, 14, "THREE")
	r = documentEdit(t, s, b, snap.ID, "b2", r.Revision, 10, 11, "x")
	if _, err = s.Undo(a, snap.ID, "u2", r.Revision); !errors.Is(err, document.ErrOverlap) {
		t.Fatalf("overlap err=%v", err)
	}
	documentText(t, s, snap.ID, "ONE! two TxREE")
}
func TestDocumentServiceRequestsUTF8AndSnapshotIsolation(t *testing.T) {
	s, snap, a, _ := documentFixture(t, "é🙂 abc")
	c := document.Change{Owner: a, DocumentID: snap.ID, RequestID: "req", BaseRevision: 1, Edits: []document.Edit{{Start: 0, End: 2, Text: "É"}}}
	r, err := s.Apply(c)
	if err != nil {
		t.Fatal(err)
	}
	dup, err := s.Apply(c)
	if err != nil || dup != r {
		t.Fatalf("dedupe=%v err=%v", dup, err)
	}
	c.Edits[0].Text = "x"
	if _, err = s.Apply(c); !errors.Is(err, document.ErrRequest) {
		t.Fatal(err)
	}
	c.RequestID = "stale"
	if _, err = s.Apply(c); !errors.Is(err, document.ErrRevision) {
		t.Fatal(err)
	}
	for i, e := range []document.Edit{{Start: 1, End: 2, Text: "x"}, {Start: 2, End: 3, Text: "x"}, {Start: 0, End: 2, Text: string([]byte{255})}} {
		c.RequestID = string(rune('a' + i))
		c.BaseRevision = r.Revision
		c.Edits = []document.Edit{e}
		if _, err = s.Apply(c); err == nil {
			t.Fatal("accepted invalid UTF8")
		}
	}
	isolated := documentText(t, s, snap.ID, "É🙂 abc")
	isolated.Text[0] = 0
	isolated.Saved[0] = 0
	documentText(t, s, snap.ID, "É🙂 abc")
}
func TestDocumentServiceSaveDetachConflict(t *testing.T) {
	s, snap, a, b := documentFixture(t, "before")
	r := documentEdit(t, s, a, snap.ID, "edit", 1, 0, 6, "after")
	if _, err := s.Save(context.Background(), a, snap.ID, 1, DocumentSaveManual); !errors.Is(err, document.ErrRevision) {
		t.Fatal(err)
	}
	s.Detach(a)
	if _, err := s.Save(context.Background(), a, snap.ID, r.Revision, DocumentSaveManual); !errors.Is(err, document.ErrOwner) {
		t.Fatal(err)
	}
	if !documentText(t, s, snap.ID, "after").Dirty {
		t.Fatal("detach discarded dirty")
	}
	s.lsp.mu.Lock()
	_, retained := s.lsp.documents[snap.Path]
	s.lsp.mu.Unlock()
	if !retained {
		t.Fatal("detach closed peer LSP")
	}
	saved, err := s.Save(context.Background(), b, snap.ID, r.Revision, DocumentSaveManual)
	if err != nil || saved.Dirty || string(saved.Saved) != "after" {
		t.Fatalf("save=%v err=%v", saved, err)
	}
	r = documentEdit(t, s, b, snap.ID, "next", r.Revision, 0, 5, "local")
	if err := os.WriteFile(snap.Path, []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	conflict, err := s.Save(context.Background(), b, snap.ID, r.Revision, DocumentSaveManual)
	if !errors.Is(err, ErrDocumentConflict) || !conflict.Dirty || !conflict.Conflict {
		t.Fatalf("conflict=%v err=%v", conflict, err)
	}
	disk, err := os.ReadFile(snap.Path)
	if err != nil || string(disk) != "external" {
		t.Fatalf("disk=%q err=%v", disk, err)
	}
	if _, err = s.Open(a, snap.Path); !errors.Is(err, document.ErrOwner) {
		t.Fatal(err)
	}
	a.Epoch++
	reopened, err := s.Open(a, snap.Path)
	if err != nil || string(reopened.Text) != "local" {
		t.Fatalf("reopen=%v err=%v", reopened, err)
	}
}
func TestDocumentServiceConcurrentRevisionFence(t *testing.T) {
	s, snap, a, b := documentFixture(t, "original")
	var wg sync.WaitGroup
	results := make(chan document.Receipt, 2)
	for i, owner := range []document.Owner{a, b} {
		wg.Add(1)
		go func(i int, owner document.Owner) {
			defer wg.Done()
			r, _ := s.Apply(document.Change{Owner: owner, DocumentID: snap.ID, RequestID: "edit", BaseRevision: 1, Edits: []document.Edit{{Start: 0, End: 8, Text: []string{"first", "second"}[i]}}})
			results <- r
		}(i, owner)
	}
	wg.Wait()
	close(results)
	accepted := 0
	for r := range results {
		if r.Accepted {
			accepted++
		} else if r.Error != document.ErrRevision.Error() {
			t.Fatal(r)
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted=%d", accepted)
	}
	final, err := s.Snapshot(snap.ID)
	if err != nil || final.Revision != 2 || (string(final.Text) != "first" && string(final.Text) != "second") {
		t.Fatalf("final=%v err=%v", final, err)
	}
}
func TestDocumentServiceGroupedUndoAtomic(t *testing.T) {
	s, snap, a, _ := documentFixture(t, "start")
	for i, text := range []string{"middle", "finish"} {
		_, err := s.Apply(document.Change{Owner: a, DocumentID: snap.ID, RequestID: string(rune('a' + i)), BaseRevision: uint64(i + 1), GroupID: "typing", Edits: []document.Edit{{Start: 0, End: len([]string{"start", "middle"}[i]), Text: text}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	r, err := s.Undo(a, snap.ID, "undo", 3)
	if err != nil {
		t.Fatal(err)
	}
	documentText(t, s, snap.ID, "start")
	if _, err = s.Redo(a, snap.ID, "redo", r.Revision); err != nil {
		t.Fatal(err)
	}
	documentText(t, s, snap.ID, "finish")
}

func TestDocumentServiceGroupedUndoAfterPeerFailsWithoutLoss(t *testing.T) {
	s, snap, a, b := documentFixture(t, "aaa bbb ccc")
	for i, e := range []document.Edit{{Start: 0, End: 3, Text: "AAAA"}, {Start: 0, End: 4, Text: "AAAAA"}} {
		_, err := s.Apply(document.Change{Owner: a, DocumentID: snap.ID, RequestID: string(rune('a' + i)), BaseRevision: uint64(i + 1), GroupID: "group", Edits: []document.Edit{e}})
		if err != nil {
			t.Fatal(err)
		}
	}
	r := documentEdit(t, s, b, snap.ID, "peer", 3, 10, 13, "CCC")
	if _, err := s.Undo(a, snap.ID, "undo", r.Revision); !errors.Is(err, document.ErrOverlap) {
		t.Fatal(err)
	}
	final := documentText(t, s, snap.ID, "AAAAA bbb CCC")
	if final.Revision != r.Revision {
		t.Fatal("partial grouped undo")
	}
}

func TestDocumentServiceMultiEditNewFileAndClose(t *testing.T) {
	s, snap, a, _ := documentFixture(t, "aaa bbb ccc")
	r, err := s.Apply(document.Change{Owner: a, DocumentID: snap.ID, RequestID: "multi", BaseRevision: 1, Edits: []document.Edit{{Start: 0, End: 3, Text: "A"}, {Start: 8, End: 11, Text: "CCCC"}}})
	if err != nil {
		t.Fatal(err)
	}
	documentText(t, s, snap.ID, "A bbb CCCC")
	if _, err = s.Undo(a, snap.ID, "undo", r.Revision); err != nil {
		t.Fatal(err)
	}
	documentText(t, s, snap.ID, "aaa bbb ccc")
	newFile, err := s.Open(a, "new.txt")
	if err != nil || newFile.DiskExists {
		t.Fatalf("new=%v err=%v", newFile, err)
	}
	r = documentEdit(t, s, a, newFile.ID, "create", 1, 0, 0, "created")
	if _, err = s.Save(context.Background(), a, newFile.ID, r.Revision, DocumentSaveManual); err != nil {
		t.Fatal(err)
	}
	disk, err := os.ReadFile(newFile.Path)
	if err != nil || string(disk) != "created" {
		t.Fatalf("disk=%q err=%v", disk, err)
	}
	s.Close()
	if _, err = s.Snapshot(snap.ID); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
