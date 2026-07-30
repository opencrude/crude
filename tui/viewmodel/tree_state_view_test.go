package viewmodel

import (
	"os"
	"path/filepath"
	"testing"

	"crude/tui/filetree"
)

func TestTreeStateViewDataProjectsVisibleRows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	model := filetree.NewFileTreeModel(dir)
	state := NewTreeState(model, 30)
	view := state.ViewData()
	if len(view.Rows) == 0 {
		t.Fatal("expected visible tree rows")
	}
	if view.Width != 30 || view.SelectedPath == "" {
		t.Fatalf("tree projection lost layout or selection: %#v", view)
	}
	if view.Rows[0].Path == "" {
		t.Fatalf("tree row did not retain path: %#v", view.Rows[0])
	}
}
