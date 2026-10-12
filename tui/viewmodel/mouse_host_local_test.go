package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"crude/tui/filetree"
)

// openMultiLineEditor mounts a multi-line document in the app's editor.
func openMultiLineEditor(t *testing.T, app *App, content string) string {
	t.Helper()
	if app.BufferMgr == nil {
		app.BufferMgr = editor.NewBufferManager()
	}
	path := filepath.Join(t.TempDir(), "doc.go")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.openEditorTab(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.StopAsyncIO)
	if lsp := app.ws.LSPMgr(); lsp != nil {
		t.Cleanup(lsp.Stop)
	}
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
		"agent")

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
			r, ok := app.rootHost.ScreenRect(app.editorScreen.ID())
			if !ok || r.Empty() {
				t.Fatalf("editor screen not mounted: ok=%v rect=%+v", ok, r)
			}
			padL := app.EditorState.Editor.PadL
			gutter := app.EditorState.Editor.GutterWidth()
			localX := gutter + 2 // 2 cells into the text column
			localY := 2
			_, _ = app.dispatchMouseClick(tea.MouseClickMsg{
				X: r.X + padL + localX, Y: r.Y + localY, Button: tea.MouseLeft,
			})
			if got := app.EditorState.Editor.CursorLine(); got != localY {
				t.Fatalf("cursor line = %d, want %d (screen y=%d rect=%+v)", got, localY, r.Y+localY, r)
			}
			if got := app.EditorState.Editor.CursorCol(); got != localX-gutter {
				t.Fatalf("cursor col = %d, want %d", got, localX-gutter)
			}
		})
	}
}

// TestEditorMouseDragUsesHostLocalCoords verifies a click+drag in the editor
// selects the exact line/col span derived from the Host-local coordinates.
func TestEditorMouseDragUsesHostLocalCoords(t *testing.T) {
	app := setupEditorMouseApp(t, 80, 24, false)
	r, ok := app.rootHost.ScreenRect(app.editorScreen.ID())
	if !ok || r.Empty() {
		t.Fatalf("editor screen not mounted: ok=%v rect=%+v", ok, r)
	}
	padL := app.EditorState.Editor.PadL
	gutter := app.EditorState.Editor.GutterWidth()
	localX := gutter + 2
	clickX, clickY := r.X+padL+localX, r.Y+1
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
	r, ok := app.rootHost.ScreenRect(app.editorScreen.ID())
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

func TestEditorHorizontalWheelPaintsThroughApp(t *testing.T) {
	for _, size := range []struct {
		name    string
		w, h    int
		sidebar bool
	}{
		{"narrow", 80, 24, false},
		{"desktop_sidebar", 160, 40, true},
	} {
		t.Run(size.name, func(t *testing.T) {
			app := setupEditorMouseApp(t, size.w, size.h, size.sidebar)
			wsOf(app).Logger = stubLogger{}
			line := strings.Repeat("abcdefghijklmnopqrstuvwxyz", 20)
			openMultiLineEditor(t, app, strings.Repeat(line+"\n", 100))
			_, _ = app.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
			_ = app.View()
			r, ok := app.rootHost.ScreenRect(app.editorScreen.ID())
			if !ok || r.Empty() {
				t.Fatal("editor is not mounted")
			}
			ed := app.EditorState.Editor
			textX := r.X + ed.PadL + ed.GutterWidth()
			for _, step := range []struct {
				button tea.MouseButton
				mod    tea.KeyMod
				x      int
			}{
				{tea.MouseWheelRight, 0, 3},
				{tea.MouseWheelRight, 0, 6},
				{tea.MouseWheelLeft, 0, 3},
				{tea.MouseWheelDown, tea.ModShift, 6},
				{tea.MouseWheelUp, tea.ModShift, 3},
			} {
				_, _ = app.Update(tea.MouseWheelMsg{X: textX + 10, Y: r.Y + 2, Button: step.button, Mod: step.mod})
				for frame := 0; frame < 2; frame++ {
					_ = app.View()
					if got := app.mainSurf.Buffer().Get(textX, r.Y+2).Char; got != rune(line[step.x]) {
						t.Fatalf("button=%v mod=%v frame=%d painted %q, want %q at offset %d", step.button, step.mod, frame, got, line[step.x], step.x)
					}
					if ed.ScrollY() != 0 || ed.CursorLine() != 0 || ed.CursorCol() != 0 {
						t.Fatalf("horizontal gesture moved vertical viewport or cursor: y=%d cursor=%d:%d", ed.ScrollY(), ed.CursorLine(), ed.CursorCol())
					}
				}
			}
		})
	}
}

func TestEditorScrollbarDragUsesRootCaptureOutsideEditor(t *testing.T) {
	var content strings.Builder
	for i := 0; i < 80; i++ {
		content.WriteString("line\n")
	}
	app := setupEditorMouseApp(t, 80, 24, false)
	openMultiLineEditor(t, app, content.String())
	app.paintShellFrame()
	r, ok := app.rootHost.ScreenRect(app.editorScreen.ID())
	if !ok || r.Empty() {
		t.Fatalf("editor screen not mounted: ok=%v rect=%+v", ok, r)
	}
	ed := app.EditorState.Editor
	scroll := ed.ScrollModel()
	if scroll.MaxOffset() <= 0 {
		t.Fatal("harness: editor must overflow")
	}
	thumbTop, _ := scroll.ThumbBounds(scroll.Viewport())
	trackX := r.X + r.W - ed.PadL - 1
	thumbY := r.Y + thumbTop

	_, _ = app.dispatchMouseClick(tea.MouseClickMsg{X: trackX, Y: thumbY, Button: tea.MouseLeft})
	owner, _, captured := app.rootHost.CapturedPointer()
	if !captured || owner != app.editorScreen.ID() || !ed.IsScrollbarDragging() {
		t.Fatalf("editor scrollbar capture owner=%q captured=%v dragging=%v", owner, captured, ed.IsScrollbarDragging())
	}

	outsideX := r.X + r.W + 8
	dragY := r.Y + scroll.Viewport() - 1
	_, _ = app.dispatchMouseMotion(tea.MouseMotionMsg{X: outsideX, Y: dragY, Button: tea.MouseLeft})
	if ed.ScrollY() == 0 {
		t.Fatal("captured scrollbar motion outside editor did not scroll content")
	}
	_, _ = app.dispatchMouseRelease(tea.MouseReleaseMsg{X: outsideX, Y: dragY, Button: tea.MouseLeft})
	if app.rootHost.HasPointerCapture() || ed.IsScrollbarDragging() {
		t.Fatal("outside release did not clear editor scrollbar capture")
	}
}

func TestEditorGutterLineSelectionUsesRootCaptureOutsideEditor(t *testing.T) {
	app := setupEditorMouseApp(t, 100, 30, false)
	openMultiLineEditor(t, app, "one\ntwo\nthree\nfour\nfive\n")
	app.paintShellFrame()
	r, ok := app.rootHost.ScreenRect(app.editorScreen.ID())
	if !ok {
		t.Fatal("editor screen missing")
	}
	x := r.X + app.EditorState.Editor.PadL + 3
	_, _ = app.dispatchMouseClick(tea.MouseClickMsg{X: x, Y: r.Y + 1, Button: tea.MouseLeft})
	if !app.rootHost.HasPointerCapture() {
		t.Fatal("line-number drag did not capture through Host")
	}
	_, _ = app.dispatchMouseMotion(tea.MouseMotionMsg{X: x, Y: r.Y + r.H + 3, Button: tea.MouseLeft})
	if !app.EditorState.Editor.HasSelection() {
		t.Fatal("captured line drag did not select complete lines")
	}
	_, _ = app.dispatchMouseRelease(tea.MouseReleaseMsg{X: x, Y: r.Y + r.H + 3, Button: tea.MouseLeft})
	if app.rootHost.HasPointerCapture() || app.EditorState.Editor.IsMouseDragging() {
		t.Fatal("outside release retained gutter capture")
	}
}

// TestEditorMouseDegenerateSizesNoPanic verifies the migrated editor mouse path
// degrades gracefully (never panics) at zero/min/narrow viewport sizes.
func TestEditorMouseDegenerateSizesNoPanic(t *testing.T) {
	sizes := []struct {
		name string
		w, h int
	}{
		{"zero", 0, 0},
		{"min", 1, 1},
		{"narrow", 10, 3},
	}
	for _, tc := range sizes {
		t.Run(tc.name, func(t *testing.T) {
			app := setupEditorMouseApp(t, tc.w, tc.h, false)
			_, _ = app.dispatchMouseClick(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
			if r, ok := app.rootHost.ScreenRect(app.editorScreen.ID()); ok && !r.Empty() {
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
			app.paintShellFrame()
			r, ok := app.rootHost.ScreenRect(app.inputScreen.ID())
			if !ok || r.Empty() {
				t.Fatalf("input screen not mounted: ok=%v rect=%+v", ok, r)
			}
			padLR := app.Theme.Spacing.PadLR
			// First text row (separator at local y=0, text at y=1); column 5.
			clickX := r.X + padLR + 5
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
	app.paintShellFrame()
	r, ok := app.rootHost.ScreenRect(app.inputScreen.ID())
	if !ok || r.Empty() {
		t.Fatalf("input screen not mounted: ok=%v rect=%+v", ok, r)
	}
	padLR := app.Theme.Spacing.PadLR
	// Row 0 col 2 -> flat 2; row 1 col 3 -> flat 6+3 = 9.
	app.routeInputMouse(tea.MouseClickMsg{X: r.X + padLR + 2, Y: r.Y + 1, Button: tea.MouseLeft})
	app.routeInputMouse(tea.MouseMotionMsg{X: r.X + padLR + 3, Y: r.Y + 2, Button: tea.MouseLeft})
	start, end := app.Input.InputSelectionRange()
	if start != 2 || end != 9 {
		t.Fatalf("input selection = (%d,%d), want (2,9)", start, end)
	}
	app.routeInputMouse(tea.MouseReleaseMsg{X: r.X + padLR + 3, Y: r.Y + 2, Button: tea.MouseLeft})
	// Copy-on-release: the drag writes the selection and drops it, so no
	// pointer-owned selection state survives the release.
	if start, end := app.Input.InputSelectionRange(); start != 0 || end != 0 {
		t.Fatalf("release must drop the drag selection: (%d,%d)", start, end)
	}
	if app.Input.InputSelecting() {
		t.Fatal("release left the composer in selecting state")
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

	r, ok := app.rootHost.ScreenRect(app.inputScreen.ID())
	if !ok || r.Empty() {
		t.Fatalf("input screen not mounted: ok=%v rect=%+v", ok, r)
	}
	chatY := r.Y - 2 // above the input, inside the chat region
	_, _ = app.dispatchMouseClick(tea.MouseClickMsg{X: r.X + 10, Y: chatY, Button: tea.MouseLeft})
	if app.Input.InputSelecting() {
		t.Fatal("click in the chat area should clear the input selection")
	}
}
