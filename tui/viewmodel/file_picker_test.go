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
	listing, err := wsOf(app).BrowseDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	app.FilePicker.Open(listing.Path, filepickerscreen.ModeFile, pickerEntries(listing.Entries))

	cmds := app.handleAppMessages(pickerNavigateMsgForTest(t, app.FilePicker, subdir))
	if len(cmds) != 1 || cmds[0] == nil {
		t.Fatal("navigation did not schedule a directory read")
	}
	app.handleAppMessages(cmds[0]())
	if app.FilePicker.Current() != subdir || app.FilePicker.VisibleCount() != 1 {
		t.Fatalf("picker directory=%q rows=%d, want one file row without parent pseudo-row", app.FilePicker.Current(), app.FilePicker.VisibleCount())
	}
}

func TestFilePickerIgnoresResultsFromStaleScreen(t *testing.T) {
	app := newFullTestApp(t)
	app.FilePicker = filepickerscreen.New(app.Theme)
	stale := filepickerscreen.New(app.Theme)
	stale.Open(t.TempDir(), filepickerscreen.ModeFile, nil)
	current := app.FilePicker.Current()
	app.handleAppMessages(pickerNavigateMsgForTest(t, stale, t.TempDir()))
	if app.FilePicker.Current() != current {
		t.Fatal("stale picker result mutated the active screen")
	}
}

func TestFilePickerRejectsStaleEpochAndOutOfOrderIntents(t *testing.T) {
	app := newFullTestApp(t)
	app.FilePicker = filepickerscreen.New(app.Theme)
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	app.FilePicker.Open(firstRoot, filepickerscreen.ModeFile, nil)
	staleEpoch := pickerNavigateMsgForTest(t, app.FilePicker, firstRoot)

	app.FilePicker.Close()
	app.FilePicker.Open(secondRoot, filepickerscreen.ModeFile, nil)
	if cmds := app.handleAppMessages(staleEpoch); len(cmds) != 0 {
		t.Fatal("closed picker epoch scheduled stale navigation")
	}
	if got := app.FilePicker.Current(); got != secondRoot {
		t.Fatalf("stale epoch changed current directory to %q", got)
	}

	older := pickerNavigateMsgForTest(t, app.FilePicker, firstRoot)
	newer := pickerNavigateMsgForTest(t, app.FilePicker, secondRoot)
	if cmds := app.handleAppMessages(older); len(cmds) != 0 {
		t.Fatal("superseded navigation intent scheduled a read")
	}
	cmds := app.handleAppMessages(newer)
	if len(cmds) != 1 || cmds[0] == nil {
		t.Fatal("newest navigation intent was not accepted")
	}
	app.handleAppMessages(cmds[0]())
}
