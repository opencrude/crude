package workspace

import (
	"context"
	"crude/textencoding"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"crude/document"
)

func TestUntitledDocumentSurvivesRestartWithoutCreatingAFile(t *testing.T) {
	root := t.TempDir()
	db, err := openWorkspaceDBAt(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	saves := NewDocumentSaveService()
	defer saves.Close()
	lsp := NewLSPManager(nil)
	defer lsp.Close()
	owner := document.Owner{ClientID: "author", Epoch: 1}
	s := NewDocumentService(root, saves, lsp, db.DB)
	first, err := s.CreateUntitled(owner, textencoding.UTF8)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateUntitled(owner, textencoding.UTF8)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || first.Path != "" || second.Path != "" ||
		first.Name == "" || second.Name == "" || first.Name == second.Name {
		t.Fatalf("untitled identities are not independent: %+v %+v", first, second)
	}
	if first.Dirty || first.DiskExists || len(first.Text) != 0 {
		t.Fatalf("new untitled document is not empty and clean: %+v", first)
	}
	edit := documentEdit(t, s, owner, first.ID, "type", first.Revision, 0, 0, "draft text")
	if err := s.ReconcileDisk(context.Background(), first.ID); err != nil {
		t.Fatalf("unnamed document was treated as a missing disk file: %v", err)
	}
	if got := documentText(t, s, first.ID, "draft text"); got.Conflict {
		t.Fatal("disk reconciliation marked an unnamed document conflicted")
	}
	for _, reason := range []DocumentSaveReason{DocumentSaveManual, DocumentSaveAfterDelay, DocumentSaveFocusOut} {
		if _, err := s.Save(context.Background(), owner, first.ID, edit.Revision, reason); err == nil {
			t.Fatalf("%s save accepted an unnamed document without a destination", reason)
		}
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("unnamed editing created a project file: %v, %v", entries, err)
	}
	lsp.mu.Lock()
	openLanguages := len(lsp.documents)
	lsp.mu.Unlock()
	if openLanguages != 0 {
		t.Fatal("unnamed editing opened a language-server file")
	}
	s.watch.mu.Lock()
	watcherStarted := s.watch.watcher != nil
	s.watch.mu.Unlock()
	if watcherStarted {
		t.Fatal("unnamed editing started a document filesystem watcher")
	}
	s.Close()

	// A restarted core has a new language-service owner.
	lsp = NewLSPManager(nil)
	defer lsp.Close()
	s = NewDocumentService(root, saves, lsp, db.DB)
	defer s.Close()
	restored, err := s.OpenUntitled(owner, first.ID)
	if err != nil || restored.ID != first.ID || restored.Path != "" ||
		restored.Name != first.Name || string(restored.Text) != "draft text" || !restored.Dirty {
		t.Fatalf("untitled work was not restored: %+v, %v", restored, err)
	}
	if _, err := s.Undo(owner, restored.ID, "undo-after-restart", restored.Revision); err != nil {
		t.Fatal(err)
	}
	undone := documentText(t, s, first.ID, "")
	if undone.Dirty {
		t.Fatal("undo to the empty baseline left a dirty buffer")
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("restart or undo created a project file: %v, %v", entries, err)
	}
}

func TestUntitledFirstSaveCreatesFileAndKeepsDocumentIdentity(t *testing.T) {
	root := t.TempDir()
	db, err := openWorkspaceDBAt(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	saves := NewDocumentSaveService()
	defer saves.Close()
	owner := document.Owner{ClientID: "author", Epoch: 1}
	s := NewDocumentService(root, saves, nil, db.DB)
	first, err := s.CreateUntitled(owner, textencoding.UTF8)
	if err != nil {
		t.Fatal(err)
	}
	edit := documentEdit(t, s, owner, first.ID, "edit", first.Revision, 0, 0, "first draft")
	target := filepath.Join(root, "file.txt")
	if _, err := s.SaveUntitled(context.Background(), owner, first.ID, first.Revision, target, textencoding.UTF8); !errors.Is(err, document.ErrRevision) {
		t.Fatalf("stale save changed the document: %v", err)
	}
	saved, err := s.SaveUntitled(context.Background(), owner, first.ID, edit.Revision, target, textencoding.UTF8)
	if err != nil || saved.ID != first.ID || saved.Path != target || saved.Dirty || string(saved.Saved) != "first draft" {
		t.Fatalf("first save did not retain the document: %+v, %v", saved, err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "first draft" {
		t.Fatalf("first save wrote wrong bytes: %q, %v", data, err)
	}
	s.Close()
	s = NewDocumentService(root, saves, nil, db.DB)
	defer s.Close()
	restored, err := s.OpenUntitled(owner, first.ID)
	if err != nil || restored.ID != first.ID || restored.Path != target || string(restored.Text) != "first draft" {
		t.Fatalf("saved identity was lost after restart: %+v, %v", restored, err)
	}
	undo, err := s.Undo(owner, first.ID, "undo", restored.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(context.Background(), owner, first.ID, undo.Revision, DocumentSaveAfterDelay); err != nil {
		t.Fatalf("named file did not join normal autosave: %v", err)
	}
	data, err = os.ReadFile(target)
	if err != nil || len(data) != 0 {
		t.Fatalf("normal autosave did not save later edits: %q, %v", data, err)
	}
}

func TestUntitledFirstSavePublishesTheNamedAnalysisSource(t *testing.T) {
	root := t.TempDir()
	saves := NewDocumentSaveService()
	defer saves.Close()
	s := NewDocumentService(root, saves, nil)
	defer s.Close()
	owner := document.Owner{ClientID: "author", Epoch: 1}
	draft, err := s.CreateUntitled(owner, textencoding.UTF8)
	if err != nil {
		t.Fatal(err)
	}
	wake, unsubscribe, err := s.Insight.Subscribe(owner, draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	if before, err := s.Insight.Snapshot(owner, draft.ID); err != nil || before.Path != "" {
		t.Fatalf("unnamed insight = %+v, %v", before, err)
	}
	target := filepath.Join(root, "main.go")
	saved, err := s.SaveUntitled(context.Background(), owner, draft.ID, draft.Revision, target, textencoding.UTF8)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(target); err != nil || len(data) != 0 {
		t.Fatalf("saving an empty buffer did not create an empty file: %q, %v", data, err)
	}
	select {
	case <-wake:
	default:
		t.Fatal("first save did not publish the named analysis source")
	}
	after, err := s.Insight.Snapshot(owner, draft.ID)
	if err != nil || after.DocumentID != draft.ID || after.Path != target || after.Revision != saved.Revision {
		t.Fatalf("first save left analysis bound to the unnamed document: %+v, %v", after, err)
	}
	peer := document.Owner{ClientID: "peer", Epoch: 1}
	reopened, err := s.Open(peer, target)
	if err != nil || reopened.ID != draft.ID {
		t.Fatalf("opening the saved path did not reuse the document identity: %+v, %v", reopened, err)
	}
}

func TestUntitledFirstSaveRefusesExistingFileWithoutLosingDraft(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(target, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	saves := NewDocumentSaveService()
	defer saves.Close()
	s := NewDocumentService(root, saves, nil)
	defer s.Close()
	owner := document.Owner{ClientID: "author", Epoch: 1}
	snap, err := s.CreateUntitled(owner, textencoding.UTF8)
	if err != nil {
		t.Fatal(err)
	}
	edit := documentEdit(t, s, owner, snap.ID, "edit", snap.Revision, 0, 0, "draft")
	if _, err := s.SaveUntitled(context.Background(), owner, snap.ID, edit.Revision, target, textencoding.UTF8); !errors.Is(err, ErrDocumentConflict) {
		t.Fatalf("existing target was not refused: %v", err)
	}
	if current := documentText(t, s, snap.ID, "draft"); current.Path != "" || !current.Dirty || current.Conflict {
		t.Fatalf("failed save changed the draft: %+v", current)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "existing" {
		t.Fatalf("failed save changed the target: %q, %v", data, err)
	}
}

func TestClosingEmptyUntitledReleasesCapacityButDirtyWorkSurvives(t *testing.T) {
	root := t.TempDir()
	db, err := openWorkspaceDBAt(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	s := NewDocumentService(root, nil, nil, db.DB)
	defer s.Close()
	owner := document.Owner{ClientID: "author", Epoch: 1}
	for i := 0; i < documentCountLimit+10; i++ {
		snap, err := s.CreateUntitled(owner, textencoding.UTF8)
		if err != nil {
			t.Fatalf("empty close leaked document capacity at %d: %v", i, err)
		}
		if err := s.CloseView(owner, snap.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.OpenUntitled(owner, snap.ID); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("closed empty document remained openable: %v", err)
		}
	}
	snap, err := s.CreateUntitled(owner, textencoding.UTF8)
	if err != nil {
		t.Fatal(err)
	}
	documentEdit(t, s, owner, snap.ID, "edit", snap.Revision, 0, 0, "keep")
	if err := s.CloseView(owner, snap.ID); err != nil {
		t.Fatal(err)
	}
	restored, err := s.OpenUntitled(owner, snap.ID)
	if err != nil || string(restored.Text) != "keep" || !restored.Dirty {
		t.Fatalf("closing a dirty view discarded shared work: %+v %v", restored, err)
	}
}
