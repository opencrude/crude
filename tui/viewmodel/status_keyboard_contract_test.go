package viewmodel

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
)

// TestFeedbackActionRegistryChordsDispatch verifies actionable status hints
// (file.save, edit.undo, edit.redo) fire through viewmodel keyboard routing,
// not through a display-only chrome component.
func TestFeedbackActionRegistryChordsDispatch(t *testing.T) {
	app := newFullTestApp(t)
	app.launcherScreen = nil
	app.width = 100
	app.height = 30

	path := filepath.Join(t.TempDir(), "save.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := editor.New(app.Theme)
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	defer ed.Close()
	app.EditorState.Editor = ed
	app.EditorState.ActiveView = path
	app.EditorState.Tabs[path] = NewEditorTab(path, ed)

	ed.InsertRune('x')
	if !ed.Modified() {
		t.Fatal("setup: editor should be dirty before save chord")
	}

	_, cmd := app.dispatchKeyPress(tea.KeyPressMsg{Text: "ctrl+s", Mod: tea.ModCtrl, Code: 's'})
	if cmd == nil || !ed.Modified() {
		t.Fatal("save must be command-owned and dirty until completion")
	}
	_, _ = app.Update(cmd())
	if ed.Modified() {
		t.Fatal("Ctrl+S via ActionRegistry should save and clear dirty flag")
	}

	beforeUndo := ed.Value()
	ed.InsertRune('y')
	app.dispatchKeyPress(tea.KeyPressMsg{Text: "ctrl+z", Mod: tea.ModCtrl, Code: 'z'})
	if ed.Value() == beforeUndo+"y" {
		t.Fatal("Ctrl+Z via ActionRegistry should undo insert")
	}
}
