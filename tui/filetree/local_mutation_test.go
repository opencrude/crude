package filetree

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
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

func TestWatcherDeletionReconcilesCursorBeforeMouseWheel(t *testing.T) {
	root := t.TempDir()
	paths := make([]string, 45)
	for i := range paths {
		paths[i] = filepath.Join(root, fmt.Sprintf("file-%02d.go", i))
		if err := os.WriteFile(paths[i], []byte("package sample\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	model := NewFileTreeModel(root)
	model.Update(TreeHeightMsg{Height: 6})
	model.SetCursorIndex(44)
	model.SetScrollOffset(39)

	// This is the production crash shape: watcher events shrink 45 rows to 37
	// while the cursor used to be 44, then wheel-up calls MoveUp repeatedly.
	for i := 37; i < len(paths); i++ {
		if err := os.Remove(paths[i]); err != nil {
			t.Fatal(err)
		}
		model.ProcessWatcherEvent(FileChangeEvent{Type: FileDeleted, Path: paths[i]})
	}

	model.Update(TreeMouseMsg{Mouse: tea.MouseWheelMsg{Button: tea.MouseWheelUp}})
	assertTreeSelectionInvariant(t, model)
}

func TestVisibleProjectionReconcilesCollapsedAndHiddenSelections(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "pkg")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(dir, "child.go")
	hidden := filepath.Join(root, ".hidden.go")
	for _, path := range []string{child, hidden} {
		if err := os.WriteFile(path, []byte("package sample\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	model := NewFileTreeModel(root)
	model.Expand(dir)
	model.Select(child)
	model.CollapseAll()
	assertTreeSelectionInvariant(t, model)
	if selected := model.SelectedNode(); selected == nil || selected.Path != dir {
		t.Fatalf("collapsed descendant selection=%v, want visible parent %q", selected, dir)
	}

	model.SetShowHidden(true)
	model.Select(hidden)
	model.SetShowHidden(false)
	assertTreeSelectionInvariant(t, model)
}

func assertTreeSelectionInvariant(t *testing.T, model *FileTreeModel) {
	t.Helper()
	nodes := model.VisibleNodes()
	if len(nodes) == 0 {
		if model.CursorIndex() != 0 || model.ScrollOffset() != 0 {
			t.Fatalf("empty projection cursor=%d scroll=%d, want zero", model.CursorIndex(), model.ScrollOffset())
		}
		return
	}
	idx := model.CursorIndex()
	if idx < 0 || idx >= len(nodes) {
		t.Fatalf("cursor=%d outside %d visible rows", idx, len(nodes))
	}
	selected := model.SelectedNode()
	if selected == nil || selected.Path != nodes[idx].Path {
		t.Fatalf("selection=%v does not match cursor row %q", selected, nodes[idx].Path)
	}
	vh := model.VisibleHeight()
	if vh <= 0 {
		vh = 10
	}
	maxOffset := len(nodes) - vh
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset := model.ScrollOffset(); offset < 0 || offset > maxOffset {
		t.Fatalf("scroll=%d outside [0,%d] for %d rows", offset, maxOffset, len(nodes))
	}
}
