package viewmodel

import (
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/screens/shelleditor"
	"crude/tui/screens/shellinput"
)

// openMultiLineEditor mounts a multi-line document in the app's editor.
func openMultiLineEditor(t *testing.T, app *App, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "doc.go")
	app.EditorState.Editor = editor.NewPreview(app.Theme, path, content, 0)
	app.EditorState.ActiveView = path
	return path
}

// setupEditorMouseApp sizes the app, opens the editor, and paints the shell so
// the editor screen is mounted in the root Host hit tree (the geometry source
// for the migrated mouse path).
func setupEditorMouseApp(t *testing.T, w, h int, sidebar bool) *App {
	t.Helper()
	app := newFullTestApp(t)
	app.width, app.height = w, h
	if sidebar {
		treeModel := filetree.NewFileTreeModel(t.TempDir())
		app.Tree = NewTreeState(treeModel, 30)
		app.Tree.Visible = true
		app.sidebarScreen.SetModel(treeModel)
	} else {
		app.Tree = NewTreeState(nil, 30)
		app.Tree.Visible = false
	}
	openMultiLineEditor(t, app, "line0\nline1\nline2\nline3\nline4\n")
	app.paintShellFrame()
	return app
}

// setupChatInputApp sizes the app with an active agent so the chat area and
// input shell are mounted (launcher is not active), then paints the shell.
func setupChatInputApp(t *testing.T, w, h int, sidebar bool) *App {
	t.Helper()
	app := newFullTestApp(t)
	app.width, app.height = w, h
	if sidebar {
		treeModel := filetree.NewFileTreeModel(t.TempDir())
		app.Tree = NewTreeState(treeModel, 30)
		app.Tree.Visible = true
		app.sidebarScreen.SetModel(treeModel)
	} else {
		app.Tree = NewTreeState(nil, 30)
		app.Tree.Visible = false
	}
	app.AddOrchestrator(
		"agent",
		make(chan provider.UserInput, 1),
		make(chan provider.UserInput, 1),
		make(chan struct{}, 1),
		make(chan string, 1),
	)
	app.paintShellFrame()
	return app
}

// TestEditorMouseClickUsesHostLocalCoords verifies that a screen click inside
// the editor pane lands the cursor at the position the Host hit tree derives
// (pane origin from the Host rect, editor PadL as the model-internal offset).
func TestEditorMouseClickUsesHostLocalCoords(t *testing.T) {
	cases := []struct {
		name    string
		w, h    int
		sidebar bool
	}{
		{"standard", 80, 24, false},
		{"standard_sidebar", 80, 24, true},
		{"wide", 200, 50, false},
		{"wide_sidebar", 200, 50, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := setupEditorMouseApp(t, tc.w, tc.h, tc.sidebar)
			r, ok := app.rootHost.ScreenRect(shelleditor.ScreenID)
			if !ok || r.Empty() {
				t.Fatalf("editor screen not mounted: ok=%v rect=%+v", ok, r)
			}
			padL := app.EditorState.Editor.PadL
			// 5-line file: gutter = 2 (min line digits) + 2 = 4 columns.
			localX := 6 // 2 cells into the text column
			localY := 2
			_, _ = app.dispatchMouseClick(tea.MouseClickMsg{
				X: r.X + padL + localX, Y: r.Y + localY, Button: tea.MouseLeft,
			})
			if got := app.EditorState.Editor.CursorLine(); got != localY {
				t.Fatalf("cursor line = %d, want %d (screen y=%d rect=%+v)", got, localY, r.Y+localY, r)
			}
			if got := app.EditorState.Editor.CursorCol(); got != localX-4 {
				t.Fatalf("cursor col = %d, want %d", got, localX-4)
			}
		})
	}
}

// TestEditorMouseDragUsesHostLocalCoords verifies a click+drag in the editor
// selects the exact line/col span derived from the Host-local coordinates.
func TestEditorMouseDragUsesHostLocalCoords(t *testing.T) {
	app := setupEditorMouseApp(t, 80, 24, false)
	r, ok := app.rootHost.ScreenRect(shelleditor.ScreenID)
	if !ok || r.Empty() {
		t.Fatalf("editor screen not mounted: ok=%v rect=%+v", ok, r)
	}
	padL := app.EditorState.Editor.PadL
	// gutter for the 5-line file is 4; local (6, 1) -> (line 1, col 2).
	clickX, clickY := r.X+padL+6, r.Y+1
	_, _ = app.dispatchMouseClick(tea.MouseClickMsg{X: clickX, Y: clickY, Button: tea.MouseLeft})
	_, _ = app.dispatchMouseMotion(tea.MouseMotionMsg{X: clickX, Y: r.Y + 3, Button: tea.MouseLeft})

	ed := app.EditorState.Editor
	if !ed.HasSelection() {
		t.Fatal("click+drag should produce a selection")
	}
	sl, sc, el, ec := ed.SelectionRange()
	if sl != 1 || sc != 2 || el != 3 || ec != 2 {
		t.Fatalf("selection = (%d,%d)-(%d,%d), want (1,2)-(3,2)", sl, sc, el, ec)
	}

	_, _ = app.dispatchMouseRelease(tea.MouseReleaseMsg{X: clickX, Y: r.Y + 3, Button: tea.MouseLeft})
}

// TestEditorMouseWheelUsesHostLocalCoords verifies wheel-over-editor still
// scrolls exactly the same amount after the coordinate-source migration.
func TestEditorMouseWheelUsesHostLocalCoords(t *testing.T) {
	lines := ""
	for i := 0; i < 30; i++ {
		lines += "line\n"
	}
	app := setupEditorMouseApp(t, 80, 24, false)
	openMultiLineEditor(t, app, lines)
	app.paintShellFrame()
	r, ok := app.rootHost.ScreenRect(shelleditor.ScreenID)
	if !ok || r.Empty() {
		t.Fatalf("editor screen not mounted: ok=%v rect=%+v", ok, r)
	}

	before := app.EditorState.Editor.ScrollY()
	_, _ = app.dispatchMouseWheel(tea.MouseWheelMsg{X: r.X + 20, Y: r.Y + 5, Button: tea.MouseWheelDown})
	if got := app.EditorState.Editor.ScrollY(); got != before+3 {
		t.Fatalf("wheel down scroll = %d, want %d", got, before+3)
	}
	_, _ = app.dispatchMouseWheel(tea.MouseWheelMsg{X: r.X + 20, Y: r.Y + 5, Button: tea.MouseWheelDown})
	if got := app.EditorState.Editor.ScrollY(); got != before+6 {
		t.Fatalf("wheel down scroll = %d, want %d", got, before+6)
	}
	_, _ = app.dispatchMouseWheel(tea.MouseWheelMsg{X: r.X + 20, Y: r.Y + 5, Button: tea.MouseWheelUp})
	if got := app.EditorState.Editor.ScrollY(); got != before+3 {
		t.Fatalf("wheel up scroll = %d, want %d", got, before+3)
	}
}

// TestEditorMouseDegenerateSizesNoPanic verifies the migrated editor mouse path
// degrades gracefully (never panics) at zero/min/narrow viewport sizes.
func TestEditorMouseDegenerateSizesNoPanic(t *testing.T) {
	sizes := []struct{ name string; w, h int }{
		{"zero", 0, 0},
		{"min", 1, 1},
		{"narrow", 10, 3},
	}
	for _, tc := range sizes {
		t.Run(tc.name, func(t *testing.T) {
			app := setupEditorMouseApp(t, tc.w, tc.h, false)
			_, _ = app.dispatchMouseClick(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
			if r, ok := app.rootHost.ScreenRect(shelleditor.ScreenID); ok && !r.Empty() {
				_, _ = app.dispatchMouseClick(tea.MouseClickMsg{X: r.X, Y: r.Y, Button: tea.MouseLeft})
				_, _ = app.dispatchMouseMotion(tea.MouseMotionMsg{X: r.X, Y: r.Y, Button: tea.MouseLeft})
			}
		})
	}
}

// TestInputMouseClickUsesHostLocalCoords verifies the legacy input mouse path
// maps a screen click to the exact rune index from the Host input screen rect.
func TestInputMouseClickUsesHostLocalCoords(t *testing.T) {
	cases := []struct {
		name    string
		w, h    int
		sidebar bool
	}{
		{"standard", 80, 24, false},
		{"standard_sidebar", 80, 24, true},
		{"wide", 200, 50, false},
		{"wide_sidebar", 200, 50, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := setupChatInputApp(t, tc.w, tc.h, tc.sidebar)
			app.Input.SetInput("hello world")
			r, ok := app.rootHost.ScreenRect(shellinput.ScreenID)
			if !ok || r.Empty() {
				t.Fatalf("input screen not mounted: ok=%v rect=%+v", ok, r)
			}
			padLR := app.Theme.Spacing.PadLR
			// First text row (separator at local y=0, text at y=1); column 5.
			clickX := r.X + padLR + 2 + 5
			clickY := r.Y + 1
			app.routeInputMouse(tea.MouseClickMsg{X: clickX, Y: clickY, Button: tea.MouseLeft})
			start, end := app.Input.InputSelectionRange()
			if start != 5 || end != 5 {
				t.Fatalf("input selection = (%d,%d), want (5,5) (click=%d,%d rect=%+v)", start, end, clickX, clickY, r)
			}
		})
	}
}

// TestInputMouseDragUsesHostLocalCoords verifies the legacy input selection
// drag maps start/end rows to flat rune indexes from Host-local coordinates.
func TestInputMouseDragUsesHostLocalCoords(t *testing.T) {
	app := setupChatInputApp(t, 80, 24, false)
	app.Input.SetInput("hello\nworld")
	r, ok := app.rootHost.ScreenRect(shellinput.ScreenID)
	if !ok || r.Empty() {
		t.Fatalf("input screen not mounted: ok=%v rect=%+v", ok, r)
	}
	padLR := app.Theme.Spacing.PadLR
	// Row 0 col 2 -> flat 2; row 1 col 3 -> flat 6+3 = 9.
	app.routeInputMouse(tea.MouseClickMsg{X: r.X + padLR + 2 + 2, Y: r.Y + 1, Button: tea.MouseLeft})
	app.routeInputMouse(tea.MouseMotionMsg{X: r.X + padLR + 2 + 3, Y: r.Y + 2, Button: tea.MouseLeft})
	start, end := app.Input.InputSelectionRange()
	if start != 2 || end != 9 {
		t.Fatalf("input selection = (%d,%d), want (2,9)", start, end)
	}
	app.routeInputMouse(tea.MouseReleaseMsg{X: r.X + padLR + 2 + 3, Y: r.Y + 2, Button: tea.MouseLeft})
	if start, end := app.Input.InputSelectionRange(); start != 2 || end != 9 {
		t.Fatalf("release must keep selection: (%d,%d), want (2,9)", start, end)
	}
}

// TestInputMouseClickAwayClearsSelection verifies the full-path click-away
// contract: a click in the chat area while the input is selecting clears the
// selection via the migrated routeInputMouse path.
func TestInputMouseClickAwayClearsSelection(t *testing.T) {
	app := setupChatInputApp(t, 80, 24, false)
	app.Input.SetInput("select me")
	_, _ = app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModShift, Text: "shift+home"})
	if !app.Input.InputSelecting() {
		t.Fatal("setup: shift+home should select the input prefix")
	}

	r, ok := app.rootHost.ScreenRect(shellinput.ScreenID)
	if !ok || r.Empty() {
		t.Fatalf("input screen not mounted: ok=%v rect=%+v", ok, r)
	}
	chatY := r.Y - 2 // above the input, inside the chat region
	_, _ = app.dispatchMouseClick(tea.MouseClickMsg{X: r.X + 10, Y: chatY, Button: tea.MouseLeft})
	if app.Input.InputSelecting() {
		t.Fatal("click in the chat area should clear the input selection")
	}
}
