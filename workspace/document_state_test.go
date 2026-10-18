package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/gob"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/document"
	"crude/fileio"
)

func TestDocumentDurableRestartAuthorHistoryAndReceipts(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "text.txt")
	if err := os.WriteFile(path, []byte("start"), 0600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "workspace.db")
	db, err := openWorkspaceDBAt(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	s := NewDocumentService(root, nil, nil, db.DB)
	a := document.Owner{ClientID: "stable-profile", Epoch: 99}
	snap, err := s.Open(a, path)
	if err != nil {
		t.Fatal(err)
	}
	for i, text := range []string{"middle", "finish"} {
		_, err = s.Apply(document.Change{Owner: a, DocumentID: path, RequestID: fmt.Sprint(i), GroupID: "typing", BaseRevision: uint64(i + 1), Edits: []document.Edit{{End: len([]string{"start", "middle"}[i]), Text: text}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = openWorkspaceDBAt(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	lsp := NewLSPManager(nil)
	defer lsp.Stop()
	s = NewDocumentService(root, nil, lsp, db.DB)
	defer s.Close()
	if got := documentText(t, s, path, "finish"); !got.Dirty || string(got.Saved) != "start" || got.Revision != 3 {
		t.Fatal(got)
	}
	if len(lsp.documents) != 0 || len(s.docs[path].owners) != 0 {
		t.Fatal("passive restore started language view or restored ownership")
	}
	a.Epoch = 1 // A restarted core's fresh connection epoch is not the old process clock.
	snap, err = s.Open(a, path)
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.Undo(a, path, "undo", snap.Revision)
	if err != nil {
		t.Fatal(err)
	}
	documentText(t, s, path, "start")
	r, err := s.Redo(a, path, "redo", u.Revision)
	if err != nil {
		t.Fatal(err)
	}
	documentText(t, s, path, "finish")
	duplicate, err := s.Redo(a, path, "redo", u.Revision)
	if err != nil || duplicate != r {
		t.Fatal(duplicate, err)
	}
}

func TestDocumentRestartMissingParentDoesNotDisableWorkspace(t *testing.T) {
	for _, dirty := range []bool{false, true} {
		t.Run(fmt.Sprintf("dirty=%v", dirty), func(t *testing.T) {
			root := t.TempDir()
			removedDir := filepath.Join(root, "removed")
			removedPath := filepath.Join(removedDir, "retained.txt")
			healthyPath := filepath.Join(root, "healthy.txt")
			if err := os.Mkdir(removedDir, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{removedPath, healthyPath} {
				if err := os.WriteFile(path, []byte("saved text"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			db, err := openWorkspaceDBAt(filepath.Join(t.TempDir(), "workspace.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			owner := document.Owner{ClientID: "author", Epoch: 1}
			s := NewDocumentService(root, nil, nil, db.DB)
			t.Cleanup(s.Close)
			removed, err := s.Open(owner, removedPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Open(owner, healthyPath); err != nil {
				t.Fatal(err)
			}
			want := "saved text"
			if dirty {
				documentEdit(t, s, owner, removed.ID, "typing", 1, 0, len(want), "unsaved text")
				want = "unsaved text"
			}
			s.Close()
			if err := os.Remove(removedPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(removedDir); err != nil {
				t.Fatal(err)
			}

			for restart := 0; restart < 2; restart++ {
				s = NewDocumentService(root, nil, nil, db.DB)
				t.Cleanup(s.Close)
				if _, err := s.Open(owner, healthyPath); err != nil {
					t.Fatalf("one removed parent disabled an unrelated editor: %v", err)
				}
				fresh, err := s.Open(owner, filepath.Join(root, "new.txt"))
				if err != nil || fresh.ID == "" {
					t.Fatalf("new document could not open: %v", err)
				}
				retained := documentText(t, s, removed.ID, want)
				if retained.ID != removed.ID || retained.Path != removedPath || !retained.Conflict || retained.Dirty != dirty || string(retained.Saved) != "saved text" {
					t.Fatalf("removed document lost its recovery evidence: %+v", retained)
				}
				if _, err := s.Open(owner, removedPath); err != nil {
					t.Fatalf("retained document cannot be opened for recovery: %v", err)
				}
				if _, err := s.Open(owner, filepath.Join(removedDir, "unretained.txt")); err == nil {
					t.Fatal("opening an unretained file accepted a missing parent")
				}
				called := false
				result := s.MutateFiles(t.Context(), FileOpCreate, []string{filepath.Join(root, "created.txt")}, nil, func() FileOperationResultMsg {
					called = true
					return FileOperationResultMsg{}
				})
				if result.Err != nil || !called {
					t.Fatalf("removed document disabled unrelated file actions: %+v", result)
				}
				if restart == 1 && dirty {
					if _, err := s.Undo(owner, removed.ID, "undo", retained.Revision); err != nil {
						t.Fatal(err)
					}
					documentText(t, s, removed.ID, "saved text")
				}
				s.Close()
			}
			if _, err := os.Stat(removedDir); !os.IsNotExist(err) {
				t.Fatalf("recovery recreated the removed directory: %v", err)
			}
		})
	}
}

func TestDocumentStorageFailureDoesNotInstallMutation(t *testing.T) {
	s, snap, a, _ := documentFixture(t, "original")
	db, err := openWorkspaceDBAt(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.attachState(db.DB); err != nil {
		t.Fatal(err)
	}
	if err = s.persistDocument(s.docs[snap.ID]); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := s.Apply(document.Change{Owner: a, DocumentID: snap.ID, RequestID: "failed", BaseRevision: 1, Edits: []document.Edit{{End: 8, Text: "changed"}}})
	if err == nil || r.Accepted || r.Revision != 1 {
		t.Fatal(r, err)
	}
	got := documentText(t, s, snap.ID, "original")
	if got.Dirty || got.Revision != 1 || len(s.docs[snap.ID].journal) != 0 || len(s.docs[snap.ID].undo) != 0 || len(s.docs[snap.ID].requests) != 0 {
		t.Fatal("storage failure changed canonical history")
	}
	s.lsp.mu.Lock()
	text := s.lsp.documents[snap.Path].text
	s.lsp.mu.Unlock()
	if text != "original" {
		t.Fatalf("LSP mutated: %q", text)
	}
}

func TestDocumentDetachDoesNotWaitForSaveMutationLease(t *testing.T) {
	s, snap, a, _ := documentFixture(t, "original")
	documentEdit(t, s, a, snap.ID, "edit", 1, 0, 8, "changed")
	unlock, err := fileio.LockMutationContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	done := make(chan error, 1)
	go func() { _, err := s.Save(context.Background(), a, snap.ID, 2, DocumentSaveManual); done <- err }()
	deadline := time.Now().Add(time.Second)
	for {
		s.ownerMu.Lock()
		pending := s.saveCancels[a] != nil
		s.ownerMu.Unlock()
		if pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("save not admitted")
		}
		time.Sleep(time.Millisecond)
	}
	start := time.Now()
	s.Detach(a)
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("detach waited for disk I/O")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("detach did not cancel pending disk write")
	}
	if _, err = s.Open(a, snap.Path); !errors.Is(err, document.ErrOwner) {
		t.Fatal(err)
	}
	s.ReleaseDetached(a)
	if len(s.docs[snap.ID].owners) != 1 {
		t.Fatal("cleanup removed peer")
	}
	documentText(t, s, snap.ID, "changed")
}

func TestDocumentRollingHistoryKeepsTypingAndExplicitReset(t *testing.T) {
	s, snap, a, _ := documentFixture(t, "x")
	for i := 0; i < documentHistoryLimit+10; i++ {
		documentEdit(t, s, a, snap.ID, fmt.Sprint(i), uint64(i+1), 0, 1, []string{"a", "b"}[i%2])
	}
	sync, err := s.Sync(a, snap.ID, 1)
	if err != nil || !sync.Reset || len(sync.Changes) != 0 {
		t.Fatal(sync, err)
	}
	u, err := s.Undo(a, snap.ID, "undo", uint64(documentHistoryLimit+11))
	if err != nil || !u.Accepted {
		t.Fatal(u, err)
	}
}

func TestDocumentEmptyServiceWatcherIsLazyAndJoined(t *testing.T) {
	s := NewDocumentService(t.TempDir(), nil, nil)
	if s.watch.watcher != nil {
		t.Fatal("empty workspace allocated filesystem watcher")
	}
	s.Close()
	select {
	case <-s.watch.done:
	default:
		t.Fatal("watch worker not joined")
	}
}

func TestDocumentCrashDiskIntentReconcilesExactSavepoint(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "text.txt")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := openWorkspaceDBAt(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	s := NewDocumentService(root, nil, nil, db.DB)
	a := document.Owner{ClientID: "a", Epoch: 1}
	snap, err := s.Open(a, path)
	if err != nil {
		t.Fatal(err)
	}
	documentEdit(t, s, a, path, "edit", snap.Revision, 0, 6, "after")
	// Reproduce a crash after disk replacement but before savepoint publication.
	s.mu.Lock()
	trial := stageDocument(s.docs[path])
	trial.diskIntent = &documentDiskIntent{Revision: 2, Hash: sha256.Sum256([]byte("after")), Exists: true}
	err = s.persistDocument(trial)
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err = os.WriteFile(path, []byte("after"), 0600); err != nil {
		t.Fatal(err)
	}
	s = NewDocumentService(root, nil, nil, db.DB)
	defer s.Close()
	got := documentText(t, s, path, "after")
	if got.Dirty || got.Conflict || string(got.Saved) != "after" || got.Revision != 2 || s.docs[path].diskIntent != nil {
		t.Fatal(got)
	}
	if _, err = s.Open(a, path); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Undo(a, path, "undo", 2); err != nil {
		t.Fatal(err)
	}
	got = documentText(t, s, path, "before")
	if !got.Dirty || string(got.Saved) != "after" {
		t.Fatal(got)
	}
}

func TestDocumentDiskCommitStorageFailurePreservesDurableEvidence(t *testing.T) {
	s, snap, a, _ := documentFixture(t, "before")
	db, err := openWorkspaceDBAt(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err = s.attachState(db.DB); err != nil {
		t.Fatal(err)
	}
	documentEdit(t, s, a, snap.ID, "edit", 1, 0, 6, "after")
	unlock, err := fileio.LockMutationContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.Save(context.Background(), a, snap.ID, 2, DocumentSaveManual); done <- err }()
	deadline := time.Now().Add(time.Second)
	for {
		s.saves.mu.Lock()
		active := len(s.saves.active) > 0
		s.saves.mu.Unlock()
		if active {
			break
		}
		if time.Now().After(deadline) {
			unlock()
			t.Fatal("save not waiting for lease")
		}
		time.Sleep(time.Millisecond)
	}
	_, err = db.DB.Exec(`CREATE TRIGGER fail_document_state BEFORE UPDATE ON workspace_documents BEGIN SELECT RAISE(ABORT, 'injected state failure'); END`)
	if err != nil {
		unlock()
		t.Fatal(err)
	}
	unlock()
	if err = <-done; err == nil {
		t.Fatal("false clean acknowledgement")
	}
	got := documentText(t, s, snap.ID, "after")
	if !got.Dirty || !got.Conflict || string(got.Saved) != "before" || s.docs[snap.ID].diskIntent == nil {
		t.Fatal(got)
	}
	disk, err := os.ReadFile(snap.Path)
	if err != nil || string(disk) != "after" {
		t.Fatal(string(disk), err)
	}
	var checkpoint []byte
	if err = db.DB.QueryRow(`SELECT checkpoint FROM workspace_documents WHERE id=?`, snap.ID).Scan(&checkpoint); err != nil {
		t.Fatal(err)
	}
	var state durableDocument
	if err = gob.NewDecoder(bytes.NewReader(checkpoint)).Decode(&state); err != nil || state.DiskIntent == nil || state.DiskIntent.Hash != sha256.Sum256([]byte("after")) {
		t.Fatal("durable disk evidence missing", err)
	}
	if _, err = db.DB.Exec(`DROP TRIGGER fail_document_state`); err != nil {
		t.Fatal(err)
	}
	if err = s.ReconcileDisk(context.Background(), snap.ID); err != nil {
		t.Fatal(err)
	}
	got = documentText(t, s, snap.ID, "after")
	if got.Dirty || got.Conflict || string(got.Saved) != "after" {
		t.Fatal(got)
	}
}
