package viewmodel

import (
	"testing"

	"crude/tui/ui"

	tea "charm.land/bubbletea/v2"
)

func TestMountedComposerBackwardAndKeyboardSelectionPaint(t *testing.T) {
	app := setupChatInputApp(t, 100, 30, false)
	wsOf(app).Logger = stubLogger{}
	app.Input.SetInput("abcdef")
	app.paintShellFrame()
	r, _ := app.rootHost.ScreenRect(app.inputScreen.ID())
	x, y := r.X+app.Theme.Spacing.PadLR, r.Y+1
	run := func(msg tea.Msg) { _, cmd := app.Update(msg); runAppCommand(t, app, cmd); app.paintShellFrame() }
	run(tea.MouseClickMsg{X: x + 3, Y: y, Button: tea.MouseLeft})
	run(tea.MouseMotionMsg{X: x + 1, Y: y, Button: tea.MouseLeft})
	run(tea.MouseReleaseMsg{X: x + 1, Y: y, Button: tea.MouseLeft})
	if app.Input.SelectedText() != "bcd" || app.Input.InputDragging() || app.rootHost.HasPointerCapture() {
		t.Fatal("mounted composer drag/capture inconsistent")
	}
	app.Input.SetInput("abcdef")
	app.Input.SetCursorFlat(3)
	run(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift})
	if app.Input.SelectedText() != "c" || app.Input.InputDragging() {
		t.Fatal("keyboard did not use insertion boundary")
	}
	selection := ui.NewTokenSet(app.Theme).Color(ui.ColorTextSelection)
	if app.mainSurf.Buffer().Get(x+2, y).BG != selection || app.mainSurf.Buffer().Get(x+3, y).BG == selection {
		t.Fatal("paint selected covered d instead of crossed c")
	}
	run(tea.MouseMotionMsg{X: x + 5, Y: y})
	if app.Input.SelectedText() != "c" {
		t.Fatal("hover mutated keyboard range")
	}
	run(tea.KeyPressMsg{Code: 'X', Text: "X"})
	if app.Input.Value() != "abXdef" {
		t.Fatalf("keyboard replacement=%q", app.Input.Value())
	}
}

func TestFindFieldUsesSamePointerAndKeyboardBoundaries(t *testing.T) {
	app := newEditorPaintApp(t, t.TempDir())
	app.registerOverlayHandlers()
	app.width, app.height = 120, 40
	wsOf(app).RootPath = t.TempDir()
	openEditorTabs(t, app, "selection.txt")
	app.EditorState.Editor.ReplaceDocument("abcdef")
	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	wsOf(app).Logger = stubLogger{}
	app.Update(tea.PasteMsg{Content: "abcdef"})
	app.View() // publish the real editor/popup geometry before pointer input
	r := findFieldScreenRect(t, app)
	run := func(msg tea.Msg) { _, cmd := app.Update(msg); runAppCommand(t, app, cmd); app.View() }
	run(tea.MouseClickMsg{X: r.X + 3, Y: r.Y, Button: tea.MouseLeft})
	run(tea.MouseMotionMsg{X: r.X + 1, Y: r.Y, Button: tea.MouseLeft})
	run(tea.MouseReleaseMsg{X: r.X + 1, Y: r.Y, Button: tea.MouseLeft})
	if app.FindReplace.FindEdit().SelectedText() != "bcd" {
		t.Fatalf("find drag=%q", app.FindReplace.FindEdit().SelectedText())
	}
	app.FindReplace.FindEdit().SetCursor(3, false)
	run(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift})
	if app.FindReplace.FindEdit().SelectedText() != "c" {
		t.Fatalf("find keyboard=%q", app.FindReplace.FindEdit().SelectedText())
	}
	run(tea.KeyPressMsg{Code: 'X', Text: "X"})
	if app.FindReplace.Query() != "abXdef" {
		t.Fatalf("find replacement=%q", app.FindReplace.Query())
	}
}
