package viewmodel

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/window"
)

type stubLogger struct{}

func (stubLogger) Info(string, string, ...any)  {}
func (stubLogger) Warn(string, string, ...any)  {}
func (stubLogger) Error(string, string, ...any) {}
func (stubLogger) Debug(string, string, ...any) {}

func setupTreeRoutingApp(t *testing.T) (*App, *filetree.FileTreeModel, string) {
	t.Helper()
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(mainPath, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(dir, "other.go")
	if err := os.WriteFile(extra, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	app.width = 100
	app.height = 30
	app.launcherScreen = nil
	app.BufferMgr = editor.NewBufferManager()

	model := filetree.NewFileTreeModel(dir)
	app.Tree = NewTreeState(model, 30)
	app.Tree.Visible = true
	app.sidebarScreen.SetModel(model)
	app.paintShellFrame()

	app.AddOrchestrator(
		"agent")

	ed := editor.New(app.Theme)
	if err := ed.Open(mainPath); err != nil {
		t.Fatal(err)
	}
	app.EditorState.Editor = ed
	app.EditorState.ActiveView = mainPath

	return app, model, mainPath
}

// TestTreeFocusRoutingContractTable verifies viewmodel keyboard routing to the
// sidebar Host only when the tree is focused and visible.
func TestTreeFocusRoutingContractTable(t *testing.T) {
	app, model, mainPath := setupTreeRoutingApp(t)

	t.Run("tree_focused_routes_down_to_sidebar", func(t *testing.T) {
		app.FocusStack.Clear()
		app.FocusStack.Push(FocusTree)
		if app.focusTarget() != FocusTree {
			t.Fatalf("focusTarget=%v want FocusTree", app.focusTarget())
		}
		before := model.CursorIndex()
		app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyDown})
		if model.CursorIndex() != before+1 {
			t.Fatalf("cursor=%d want %d when tree focused", model.CursorIndex(), before+1)
		}
	})

	t.Run("editor_focus_does_not_move_tree_cursor", func(t *testing.T) {
		app.FocusStack.Clear()
		beforePath := model.SelectedNode().Path
		app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyDown})
		if model.SelectedNode().Path != beforePath {
			t.Fatal("tree cursor must not move when editor/input owns focus")
		}
	})

	t.Run("hidden_tree_does_not_consume_keys", func(t *testing.T) {
		app.FocusStack.Clear()
		app.FocusStack.Push(FocusTree)
		app.Tree.Visible = false
		if app.focusTarget() == FocusTree {
			t.Fatal("hidden tree must not become focusTarget")
		}
		before := model.CursorIndex()
		app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyDown})
		if model.CursorIndex() != before {
			t.Fatal("hidden tree must not consume navigation keys")
		}
		app.Tree.Visible = true
	})

	t.Run("esc_pops_tree_focus_restores_editor", func(t *testing.T) {
		app.FocusStack.Clear()
		app.FocusStack.Push(FocusTree)
		app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
		if app.FocusStack.Top() == FocusTree {
			t.Fatal("Esc should pop FocusTree from stack")
		}
		if app.focusTarget() != FocusEditor {
			t.Fatalf("after Esc focusTarget=%v want FocusEditor", app.focusTarget())
		}
	})

	t.Run("esc_with_active_filter_clears_then_pops", func(t *testing.T) {
		app.FocusStack.Clear()
		app.FocusStack.Push(FocusTree)
		model.EnterFilterMode()
		model.SetFilter("mai")

		app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
		if model.Filter() != "" {
			t.Fatalf("first Esc filter=%q want empty", model.Filter())
		}
		if model.FilterActive() {
			t.Fatal("first Esc should deactivate filter mode")
		}
		if app.FocusStack.Top() != FocusTree {
			t.Fatal("first Esc should keep tree focus")
		}

		app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
		if app.FocusStack.Top() == FocusTree {
			t.Fatal("second Esc should pop FocusTree")
		}
		if app.focusTarget() != FocusEditor {
			t.Fatalf("after second Esc focusTarget=%v want FocusEditor", app.focusTarget())
		}
	})

	t.Run("ctrl_c_does_not_pop_tree_focus", func(t *testing.T) {
		app.FocusStack.Clear()
		app.FocusStack.Push(FocusTree)
		app.dispatchKeyPress(tea.KeyPressMsg{Text: "ctrl+c", Mod: tea.ModCtrl, Code: 'c'})
		if app.FocusStack.Top() != FocusTree {
			t.Fatal("Ctrl+C must not pop FocusTree (copy-only elsewhere)")
		}
	})

	t.Run("enter_on_file_opens_once", func(t *testing.T) {
		app.FocusStack.Clear()
		app.FocusStack.Push(FocusTree)
		app.EditorState = NewEditorState()
		app.BufferMgr = editor.NewBufferManager()
		app.Win = window.New(app.Theme)

		model.Select(mainPath)
		_, cmd := app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd != nil {
			if msg := cmd(); msg != nil {
				app.handleAppMessages(msg)
			}
		}
		if len(app.EditorState.Tabs) != 1 {
			t.Fatalf("first Enter should open one tab, got %d", len(app.EditorState.Tabs))
		}

		app.FocusStack.Push(FocusTree)
		model.Select(mainPath)
		_, cmd = app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd != nil {
			if msg := cmd(); msg != nil {
				app.handleAppMessages(msg)
			}
		}
		if len(app.EditorState.Tabs) != 1 {
			t.Fatalf("second Enter on same file opened duplicate tab: tabs=%d", len(app.EditorState.Tabs))
		}
	})
}
