package viewmodel

import (
	"os"
	"path/filepath"
	"testing"

	filepickerscreen "crude/tui/screens/filepicker"
)

func TestFilePickerNavigationLoadsDirectoryThroughWorkspace(t *testing.T) {
	app := newFullTestApp(t)
	app.FilePicker = filepickerscreen.New(app.Theme)
	root := t.TempDir()
	subdir := filepath.Join(root, "src")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	listing, err := app.BrowseDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	app.FilePicker.Open(listing.Path, filepickerscreen.ModeFile, pickerEntries(listing.Entries))

	app.handleAppMessages(filepickerscreen.NavigateMsg{Target: app.FilePicker, Path: subdir})
	if app.FilePicker.Current() != subdir || app.FilePicker.VisibleCount() != 2 {
		t.Fatalf("picker directory=%q rows=%d", app.FilePicker.Current(), app.FilePicker.VisibleCount())
	}
}

func TestFilePickerIgnoresResultsFromStaleScreen(t *testing.T) {
	app := newFullTestApp(t)
	app.FilePicker = filepickerscreen.New(app.Theme)
	stale := filepickerscreen.New(app.Theme)
	current := app.FilePicker.Current()
	app.handleAppMessages(filepickerscreen.NavigateMsg{Target: stale, Path: t.TempDir()})
	if app.FilePicker.Current() != current {
		t.Fatal("stale picker result mutated the active screen")
	}
}
