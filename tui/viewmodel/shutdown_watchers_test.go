package viewmodel

import (
	"os"
	"path/filepath"
	"testing"

	"crude/tui/editor"
	"crude/tui/filetree"
)

func TestStopAsyncIOReleasesShellWatchersAndRejectsLateStarts(t *testing.T) {
	app, _ := newRealShellApp(t)
	paths := openEditorTabs(t, app, "one.txt", "two.txt")
	editors := []*editor.Editor{app.EditorState.Tabs[paths[0]].Editor, app.EditorState.Tabs[paths[1]].Editor}
	orphan := editor.New(app.Theme)
	if err := orphan.Open(paths[0]); err != nil {
		t.Fatal(err)
	}
	app.EditorState.Editor = orphan
	editors = append(editors, orphan)
	// Keep the regression test self-cleaning even before the shutdown fix.
	t.Cleanup(func() { app.closeWorkspaceEditors() })
	app.wireAndStartWorkspaceTreeWatcher(app.Tree.Model)
	watcher := app.Tree.Model.Watcher()
	if watcher == nil {
		t.Fatal("fixture could not start tree watcher")
	}
	t.Cleanup(app.Tree.Model.StopWatcher)
	app.StopAsyncIO()
	for _, ed := range editors {
		if ed.IsOpen() {
			t.Error("shutdown retained a TUI-owned editor and its watcher")
		}
	}
	select {
	case <-watcher.Done():
	default:
		t.Error("shutdown retained the tree watcher")
	}
	late := filetree.NewFileTreeModel(t.TempDir())
	t.Cleanup(late.StopWatcher)
	app.wireAndStartWorkspaceTreeWatcher(late)
	if late.Watcher() != nil {
		t.Error("shutdown accepted a late synchronous watcher start")
	}
	app.StopAsyncIO() // repeated teardown is safe
}

func TestStopAsyncIOJoinsInFlightTreeStartup(t *testing.T) {
	app, _ := newRealShellApp(t)
	root := t.TempDir()
	for i := range 30 {
		if err := os.MkdirAll(filepath.Join(root, string(rune('a'+i))), 0700); err != nil {
			t.Fatal(err)
		}
	}
	model := filetree.NewFileTreeModel(root)
	t.Cleanup(model.StopWatcher)
	app.wireAndStartWorkspaceTreeWatcherAsync(model, app.workspaceSwitchGen.Load())
	app.StopAsyncIO()
	if model.Watcher() != nil {
		t.Fatal("tree startup outlived UI shutdown")
	}
	app.wireAndStartWorkspaceTreeWatcherAsync(model, app.workspaceSwitchGen.Load())
	if model.Watcher() != nil {
		t.Fatal("stopped UI admitted a new background watcher")
	}
}
