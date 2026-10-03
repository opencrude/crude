package workspace

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func savedWorkspaceFixture(t *testing.T) (string, string) {
	t.Helper()
	setTestHome(t)
	project := t.TempDir()
	db, err := OpenWorkspaceDB(project)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.RegisterSession("session-1", project, "Project", "model", "provider"); err != nil {
		t.Fatal(err)
	}
	saved := db.Dir
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(saved, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(saved, "sessions", "evidence.txt"), []byte("exact saved conversation\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(project, "source.txt"), []byte("project stays untouched\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return project, saved
}

func TestSavedWorkspaceDeleteAndUndoPreserveEveryByte(t *testing.T) {
	project, saved := savedWorkspaceFixture(t)
	before, err := os.ReadFile(filepath.Join(saved, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PreviewWorkspaceDeletion(t.Context(), WorkspaceID(project), project, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if plan.SessionCount != 1 {
		t.Fatalf("preview=%+v", plan)
	}
	deleted, err := DeleteSavedWorkspace(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(saved); !os.IsNotExist(err) {
		t.Fatal("deleted workspace remains in recents storage")
	}
	for _, path := range []string{filepath.Join(project, "source.txt"), filepath.Join(deleted.RecoveryPath, "data", "sessions", "evidence.txt"), filepath.Join(deleted.RecoveryPath, "workspace.json")} {
		if _, err = os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
	if err = RestoreDeletedWorkspace(t.Context(), deleted); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(saved, "workspace.db"))
	if err != nil || string(before) != string(after) {
		t.Fatal("Undo changed the workspace database")
	}
	text, err := os.ReadFile(filepath.Join(saved, "sessions", "evidence.txt"))
	if err != nil || string(text) != "exact saved conversation\n" {
		t.Fatal("Undo lost session content")
	}
	info, err := os.Stat(filepath.Join(saved, "sessions", "evidence.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("Undo changed permissions")
	}
	if _, err = DeleteSavedWorkspace(t.Context(), plan); err == nil {
		t.Fatal("old preview could be replayed after restore")
	}
}

func TestSavedWorkspaceDeletionRejectsChangesAndCurrentWorkspace(t *testing.T) {
	project, saved := savedWorkspaceFixture(t)
	if _, err := PreviewWorkspaceDeletion(t.Context(), WorkspaceID(project), project, project); err == nil {
		t.Fatal("current workspace accepted")
	}
	if _, err := PreviewWorkspaceDeletion(t.Context(), "../escape", project, t.TempDir()); err == nil {
		t.Fatal("path traversal accepted")
	}
	plan, err := PreviewWorkspaceDeletion(t.Context(), WorkspaceID(project), project, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(saved, "new-session"), []byte("later work"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = DeleteSavedWorkspace(t.Context(), plan); err == nil {
		t.Fatal("stale preview deleted newer work")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = DeleteSavedWorkspace(ctx, plan); err == nil {
		t.Fatal("cancelled delete succeeded")
	}
	if _, err = os.Stat(saved); err != nil {
		t.Fatal("failed deletion changed the source")
	}
}

func TestWorkspaceUndoNeverOverwritesReplacement(t *testing.T) {
	project, saved := savedWorkspaceFixture(t)
	plan, err := PreviewWorkspaceDeletion(t.Context(), WorkspaceID(project), project, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := DeleteSavedWorkspace(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(saved, 0700); err != nil {
		t.Fatal(err)
	}
	if err = RestoreDeletedWorkspace(t.Context(), deleted); err == nil {
		t.Fatal("Undo replaced a later directory")
	}
	if _, err = os.Stat(filepath.Join(deleted.RecoveryPath, "data", "workspace.db")); err != nil {
		t.Fatal("conflict lost recovery data")
	}
}

func TestWorkspaceDeleteRefusesSymlinkStorage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires Windows privilege")
	}
	project, saved := savedWorkspaceFixture(t)
	backup := saved + "-moved"
	if err := os.Rename(saved, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(backup, saved); err != nil {
		t.Fatal(err)
	}
	if _, err := PreviewWorkspaceDeletion(t.Context(), WorkspaceID(project), project, t.TempDir()); err == nil {
		t.Fatal("symlink storage accepted")
	}
}
