package workspace

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"crude/document"
)

func durableDeltaFixture(t *testing.T) (*DocumentService, *WorkspaceDB, document.Snapshot, document.Owner) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "text.txt")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := openWorkspaceDBAt(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := NewDocumentService(root, nil, nil, db.DB)
	t.Cleanup(s.Close)
	owner := document.Owner{ClientID: "author", Epoch: 1}
	snap, err := s.Open(owner, path)
	if err != nil {
		t.Fatal(err)
	}
	return s, db, snap, owner
}

func TestDocumentOrdinaryEditNeverRewritesCheckpoint(t *testing.T) {
	s, db, snap, owner := durableDeltaFixture(t)
	var before []byte
	if err := db.DB.QueryRow(`SELECT checkpoint FROM workspace_documents WHERE id=?`, snap.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec(`CREATE TRIGGER no_checkpoint_edit BEFORE UPDATE ON workspace_documents BEGIN SELECT RAISE(ABORT,'ordinary edit rewrote checkpoint'); END`); err != nil {
		t.Fatal(err)
	}
	for i := range 100 {
		documentEdit(t, s, owner, snap.ID, fmt.Sprint(i), uint64(i+1), 0, 1, string(rune('a'+i%26)))
	}
	var after []byte
	if err := db.DB.QueryRow(`SELECT checkpoint FROM workspace_documents WHERE id=?`, snap.ID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("base changed on typing")
	}
	var count, maxSize int
	if err := db.DB.QueryRow(`SELECT count(*),max(length(payload)) FROM workspace_document_deltas`).Scan(&count, &maxSize); err != nil {
		t.Fatal(err)
	}
	if count != 100 || maxSize > 4096 {
		t.Fatalf("delta metadata grew with history: count=%d bytes=%d", count, maxSize)
	}
}

func TestDocumentDeltaReceiptFailureIsAtomic(t *testing.T) {
	s, db, snap, owner := durableDeltaFixture(t)
	if _, err := db.DB.Exec(`CREATE TRIGGER fail_receipt BEFORE INSERT ON workspace_document_receipts BEGIN SELECT RAISE(ABORT,'receipt failed'); END`); err != nil {
		t.Fatal(err)
	}
	r, err := s.Apply(document.Change{Owner: owner, DocumentID: snap.ID, RequestID: "fail", BaseRevision: 1, Edits: []document.Edit{{End: 1, Text: "z"}}})
	if err == nil || r.Accepted || r.Revision != 1 {
		t.Fatal(r, err)
	}
	documentText(t, s, snap.ID, "original")
	var revision, count int
	if err = db.DB.QueryRow(`SELECT revision FROM workspace_document_heads WHERE id=?`, snap.ID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if err = db.DB.QueryRow(`SELECT count(*) FROM workspace_document_deltas`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if revision != 1 || count != 0 || len(s.docs[snap.ID].requests) != 0 {
		t.Fatal("partial durable publication")
	}
}

func TestDocumentReceiptsBeyondOldLimitRestartAndDuplicate(t *testing.T) {
	s, db, snap, owner := durableDeltaFixture(t)
	first := document.Change{Owner: owner, DocumentID: snap.ID, RequestID: "first", BaseRevision: 1, Edits: []document.Edit{{End: 1, Text: "z"}}}
	receipt, err := s.Apply(first)
	if err != nil {
		t.Fatal(err)
	}
	// Rejections are durable commands too, without expensive text mutation.
	for i := range documentRequestLimit + 1 {
		_, err = s.Apply(document.Change{Owner: owner, DocumentID: snap.ID, RequestID: fmt.Sprint(i), BaseRevision: 1, Edits: []document.Edit{{End: 1, Text: "q"}}})
		if !errors.Is(err, document.ErrRevision) {
			t.Fatal(i, err)
		}
	}
	if len(s.docs[snap.ID].requests) > 256 {
		t.Fatal("unbounded receipt cache")
	}
	s.Close()
	s = NewDocumentService(s.root, nil, nil, db.DB)
	t.Cleanup(s.Close)
	if _, err = s.Open(owner, snap.Path); err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.Apply(first)
	if err != nil || duplicate != receipt {
		t.Fatal(duplicate, err)
	}
	first.Edits[0].Text = "wrong"
	if _, err = s.Apply(first); !errors.Is(err, document.ErrRequest) {
		t.Fatal(err)
	}
	documentEdit(t, s, owner, snap.ID, "continue", 2, 0, 1, "y")
	if _, err = s.Undo(owner, snap.ID, "undo", 3); err != nil {
		t.Fatal(err)
	}
	documentText(t, s, snap.ID, "zriginal")
}

func TestDocumentOldCheckpointRejectedWithoutUpgrade(t *testing.T) {
	s, db, snap, _ := durableDeltaFixture(t)
	s.Close()
	var data []byte
	if err := db.DB.QueryRow(`SELECT checkpoint FROM workspace_documents WHERE id=?`, snap.ID).Scan(&data); err != nil {
		t.Fatal(err)
	}
	var state durableDocument
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&state); err != nil {
		t.Fatal(err)
	}
	state.Version = 1
	data, err := documentEncode(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB.Exec(`UPDATE workspace_documents SET checkpoint=? WHERE id=?`, data, snap.ID); err != nil {
		t.Fatal(err)
	}
	loaded := NewDocumentService(s.root, nil, nil, db.DB)
	defer loaded.Close()
	if loaded.stateErr == nil {
		t.Fatal("old format accepted")
	}
}

func TestDocumentRollingCheckpointRestartKeepsRecentUndo(t *testing.T) {
	s, db, snap, owner := durableDeltaFixture(t)
	for i := range documentHistoryLimit + 10 {
		documentEdit(t, s, owner, snap.ID, fmt.Sprint(i), uint64(i+1), 0, 1, string(rune('a'+i%26)))
	}
	s.Close()
	s = NewDocumentService(s.root, nil, nil, db.DB)
	t.Cleanup(s.Close)
	if _, err := s.Open(owner, snap.Path); err != nil {
		t.Fatal(err)
	}
	result, err := s.Sync(owner, snap.ID, 1)
	if err != nil || !result.Reset {
		t.Fatal(result, err)
	}
	if _, err = s.Undo(owner, snap.ID, "undo", uint64(documentHistoryLimit+11)); err != nil {
		t.Fatal(err)
	}
	want := string(rune('a'+(documentHistoryLimit+8)%26)) + "riginal"
	documentText(t, s, snap.ID, want)
}

func TestDocumentDurableHeadFenceRejectsStaleWriter(t *testing.T) {
	s, db, snap, owner := durableDeltaFixture(t)
	if _, err := db.DB.Exec(`UPDATE workspace_document_heads SET revision=9 WHERE id=?`, snap.ID); err != nil {
		t.Fatal(err)
	}
	r, err := s.Apply(document.Change{Owner: owner, DocumentID: snap.ID, RequestID: "stale", BaseRevision: 1, Edits: []document.Edit{{End: 1, Text: "z"}}})
	if !errors.Is(err, document.ErrRevision) || r.Accepted {
		t.Fatal(r, err)
	}
	documentText(t, s, snap.ID, "original")
}
