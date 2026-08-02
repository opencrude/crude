package viewmodel

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/screens/launcher"
	"crude/workspace"
)

// TestFocusPrecedence_TreeWinsOverVisibleLauncher verifies sidebar keyboard
// routing (including `/` filter) when FocusTree is pushed while the welcome
// launcher is still visible (no open tabs).
func TestFocusPrecedence_TreeWinsOverVisibleLauncher(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newFullTestApp(t)
	app.width, app.height = 100, 30
	app.ChatAreaHeight = 20
	app.BufferMgr = editor.NewBufferManager()
	app.EditorState = NewEditorState()
	app.AgentMgr = workspace.NewAgentManager()

	app.launcherScreen.SetData(dir, nil)
	app.paintShellFrame()

	model := filetree.NewFileTreeModel(dir)
	app.Tree = NewTreeState(model, 28)
	app.Tree.Visible = true
	app.sidebarScreen.SetModel(model)
	app.paintShellFrame()

	if !app.launcherHostVisible() {
		t.Fatal("precondition: launcher should be visible with no tabs")
	}

	// Without tree focus, launcher owns keys.
	app.FocusStack.Clear()
	if app.focusTarget() != FocusLauncher {
		t.Fatalf("focusTarget=%v want FocusLauncher", app.focusTarget())
	}
	app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyDown})
	if got := app.launcherScreen.SelectedItem(); got == nil || got.Kind != launcher.ItemOpenFolder {
		t.Fatalf("launcher should move selection on Down, got %#v", got)
	}

	// With tree focus, `/` opens tree filter even though launcher is visible.
	app.FocusStack.Clear()
	app.FocusStack.Push(FocusTree)
	if app.focusTarget() != FocusTree {
		t.Fatalf("focusTarget=%v want FocusTree with launcher visible", app.focusTarget())
	}
	app.dispatchKeyPress(tea.KeyPressMsg{Code: '/', Text: "/"})
	if !model.FilterActive() {
		t.Fatal("tree `/` filter must activate when tree is focused")
	}
}
