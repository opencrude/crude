package filetree

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyLocalMutationUpdatesTreeWithoutWatcherCallback(t *testing.T) {
	root := t.TempDir()
	model := NewFileTreeModel(root)

	callbackCalls := 0
	model.SetOnFileChanged(func(string, int) {
		callbackCalls++
	})

	createdFile := filepath.Join(root, "created.go")
	if err := os.WriteFile(createdFile, []byte("package created\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	model.ApplyLocalMutation(FileChangeEvent{Type: FileCreated, Path: createdFile})
	assertTreePathState(t, model, createdFile, true, false)

	createdDir := filepath.Join(root, "created-dir")
	if err := os.Mkdir(createdDir, 0o700); err != nil {
		t.Fatal(err)
	}
	model.ApplyLocalMutation(FileChangeEvent{Type: FileCreated, Path: createdDir})
	assertTreePathState(t, model, createdDir, true, true)

	renamedFile := filepath.Join(root, "renamed.go")
	if err := os.Rename(createdFile, renamedFile); err != nil {
		t.Fatal(err)
	}
	model.ApplyLocalMutation(FileChangeEvent{
		Type:    FileRenamed,
		OldPath: createdFile,
		Path:    renamedFile,
	})
	assertTreePathState(t, model, createdFile, false, false)
	assertTreePathState(t, model, renamedFile, true, false)

	if err := os.RemoveAll(createdDir); err != nil {
		t.Fatal(err)
	}
	model.ApplyLocalMutation(FileChangeEvent{Type: FileDeleted, Path: createdDir})
	assertTreePathState(t, model, createdDir, false, false)

	if callbackCalls != 0 {
		t.Fatalf("local mutations invoked watcher callback %d times, want 0", callbackCalls)
	}
}

func assertTreePathState(t *testing.T, model *FileTreeModel, path string, wantPresent, wantDir bool) {
	t.Helper()

	node := model.NodeByPath(path)
	if (node != nil) != wantPresent {
		t.Fatalf("NodeByPath(%q) present=%v, want %v", path, node != nil, wantPresent)
	}
	if node != nil && node.IsDir != wantDir {
		t.Fatalf("NodeByPath(%q).IsDir=%v, want %v", path, node.IsDir, wantDir)
	}

	visible := false
	for _, row := range model.VisibleRows() {
		if row == path {
			visible = true
			break
		}
	}
	if visible != wantPresent {
		t.Fatalf("VisibleRows contains %q=%v, want %v; rows=%v", path, visible, wantPresent, model.VisibleRows())
	}
}
