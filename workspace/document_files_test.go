package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"crude/document"
)

func TestDocumentFilesRetainedDescendantAndDestinationFailBeforeEffects(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "dir")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "text")
	if err := os.WriteFile(path, []byte("disk"), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewDocumentService(root, nil, nil)
	t.Cleanup(s.Close)
	owner := document.Owner{ClientID: "owner", Epoch: 1}
	snap, err := s.Open(owner, path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CloseView(owner, snap.ID); err != nil {
		t.Fatal(err)
	}
	for _, paths := range [][]string{{dir}, {filepath.Join(root, "other"), dir}, {path}} {
		called := false
		got := s.MutateFiles(context.Background(), FileOpRename, paths, nil, func() FileOperationResultMsg { called = true; return FileOperationResultMsg{} })
		if got.Err == nil || called {
			t.Fatalf("retained closed-view path unprotected: %v %+v", paths, got)
		}
	}
}

func TestDocumentFilesDirtyBatchRejectsAllBeforeEffects(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "text")
	if err := os.WriteFile(path, []byte("disk"), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewDocumentService(root, nil, nil)
	t.Cleanup(s.Close)
	owner := document.Owner{ClientID: "owner", Epoch: 1}
	snap, err := s.Open(owner, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(document.Change{Owner: owner, DocumentID: snap.ID, RequestID: "edit", BaseRevision: snap.Revision, Edits: []document.Edit{{Start: 4, End: 4, Text: " unsaved"}}}); err != nil {
		t.Fatal(err)
	}
	called := false
	got := s.MutateFiles(context.Background(), FileOpApplyEdit, nil, []FileEdit{{Path: filepath.Join(root, "unopened"), OldContent: "a", NewContent: "b"}, {Path: path, OldContent: "disk", NewContent: "lost"}}, func() FileOperationResultMsg { called = true; return FileOperationResultMsg{} })
	if got.Err == nil || called {
		t.Fatalf("dirty batch performed effects: %+v", got)
	}
	gotSnap, err := s.Snapshot(snap.ID)
	if err != nil || string(gotSnap.Text) != "disk unsaved" {
		t.Fatalf("canonical work lost: %+v %v", gotSnap, err)
	}
}

func TestDocumentRenameDirtyDirectoryDurableUndoAndPathIndex(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "old")
	target := filepath.Join(root, "new")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := openWorkspaceDBAt(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	s := NewDocumentService(root, nil, nil, db.DB)
	owner := document.Owner{ClientID: "author", Epoch: 1}
	ids := []string{}
	for _, name := range []string{"a", "b"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("disk"), 0600); err != nil {
			t.Fatal(err)
		}
		snap, err := s.Open(owner, path)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, snap.ID)
		documentEdit(t, s, owner, snap.ID, name, 1, 4, 4, " unsaved")
	}
	ops := NewFileOpsService(root, nil, nil)
	result := s.MutateFiles(context.Background(), FileOpRename, []string{dir, target}, nil, func() FileOperationResultMsg { return ops.RenamePath(dir, target) })
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	for _, id := range ids {
		snap, err := s.Snapshot(id)
		if err != nil || snap.Path != filepath.Join(target, filepath.Base(id)) || snap.ID != id || string(snap.Text) != "disk unsaved" || !snap.Dirty {
			t.Fatal(snap, err)
		}
		if _, ok := s.SnapshotIfOpen(id); ok {
			t.Fatal("old path aliases ID")
		}
		opened, err := s.Open(owner, snap.Path)
		if err != nil || opened.ID != id {
			t.Fatal(opened, err)
		}
	}
	s.Close()
	s = NewDocumentService(root, nil, nil, db.DB)
	defer s.Close()
	for _, id := range ids {
		snap, err := s.Open(owner, filepath.Join(target, filepath.Base(id)))
		if err != nil || snap.ID != id || !snap.Dirty {
			t.Fatal(snap, err)
		}
		if _, err = s.Undo(owner, id, "undo-"+filepath.Base(id), snap.Revision); err != nil {
			t.Fatal(err)
		}
		got := documentText(t, s, id, "disk")
		if got.Dirty {
			t.Fatal(got)
		}
	}
}

func TestDocumentRenameStorageFailureBeforeAndAfterDisk(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "after"}[after], func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "old")
			target := filepath.Join(root, "new")
			if err := os.WriteFile(path, []byte("disk"), 0600); err != nil {
				t.Fatal(err)
			}
			db, err := openWorkspaceDBAt(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			s := NewDocumentService(root, nil, nil, db.DB)
			owner := document.Owner{ClientID: "a", Epoch: 1}
			snap, err := s.Open(owner, path)
			if err != nil {
				t.Fatal(err)
			}
			documentEdit(t, s, owner, snap.ID, "edit", 1, 4, 4, " unsaved")
			inject := func() {
				if _, err := db.DB.Exec(`CREATE TRIGGER fail_path BEFORE UPDATE ON workspace_documents BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if !after {
				inject()
			}
			called := false
			result := s.MutateFiles(context.Background(), FileOpRename, []string{path, target}, nil, func() FileOperationResultMsg {
				called = true
				err := os.Rename(path, target)
				if err == nil {
					inject()
				}
				return FileOperationResultMsg{Err: err}
			})
			if result.Err == nil || called != after {
				t.Fatal(result, called)
			}
			s.Close()
			if _, err = db.DB.Exec(`DROP TRIGGER fail_path`); err != nil {
				t.Fatal(err)
			}
			s = NewDocumentService(root, nil, nil, db.DB)
			defer s.Close()
			got := documentText(t, s, path, "disk unsaved")
			expected := path
			if after {
				expected = target
			}
			if got.Path != expected || !got.Dirty || got.Conflict {
				t.Fatal(got)
			}
		})
	}
}

func TestDocumentDeleteRetainsCleanIdentityAndRefusesDirty(t *testing.T) {
	s, snap, a, _ := documentFixture(t, "disk")
	documentEdit(t, s, a, snap.ID, "edit", 1, 4, 4, " unsaved")
	called := false
	result := s.MutateFiles(context.Background(), FileOpDelete, []string{snap.Path}, nil, func() FileOperationResultMsg { called = true; return FileOperationResultMsg{} })
	if result.Err == nil || called {
		t.Fatal(result)
	}
	if _, err := s.Undo(a, snap.ID, "undo", 2); err != nil {
		t.Fatal(err)
	}
	result = s.MutateFiles(context.Background(), FileOpDelete, []string{snap.Path}, nil, func() FileOperationResultMsg { return FileOperationResultMsg{Err: os.Remove(snap.Path)} })
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	got := documentText(t, s, snap.ID, "disk")
	if !got.Conflict {
		t.Fatal(got)
	}
	if _, err := s.Save(context.Background(), a, snap.ID, got.Revision, DocumentSaveManual); err == nil {
		t.Fatal("deleted file silently recreated")
	}
}

func TestDocumentRelocationAmbiguousColdRecoveryRetainsBoth(t *testing.T) {
	root := t.TempDir()
	path, target := filepath.Join(root, "old"), filepath.Join(root, "new")
	if err := os.WriteFile(path, []byte("disk"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := openWorkspaceDBAt(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	s := NewDocumentService(root, nil, nil, db.DB)
	owner := document.Owner{ClientID: "a", Epoch: 1}
	snap, err := s.Open(owner, path)
	if err != nil {
		t.Fatal(err)
	}
	documentEdit(t, s, owner, snap.ID, "edit", 1, 4, 4, " unsaved")
	pre, err := documentPreimage(path)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	trial := stageDocument(s.docs[path])
	trial.relocation = &documentRelocation{Source: path, Target: target, Preimages: map[string]documentPathPreimage{path: pre}}
	err = s.persistDocument(trial)
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err = os.WriteFile(target, []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	s = NewDocumentService(root, nil, nil, db.DB)
	defer s.Close()
	got := documentText(t, s, path, "disk unsaved")
	if got.Path != path || !got.Conflict || s.docs[path].relocation == nil {
		t.Fatal(got)
	}
	for path, want := range map[string]string{path: "disk", target: "other"} {
		text, err := os.ReadFile(path)
		if err != nil || string(text) != want {
			t.Fatal(string(text), err)
		}
	}
}
