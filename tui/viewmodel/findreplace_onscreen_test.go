package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
)

// TestFindReplace_TransformNeverLeavesScreen pins the off-screen defect:
// opening Find via Ctrl+F and then transforming to Replace via Ctrl+H must
// keep the window fully inside the terminal. Both forms share one width
// (the replace form's widest content), so the transform changes height
// only — and any window grown past the screen edge is clamped back in.
func TestFindReplace_TransformNeverLeavesScreen(t *testing.T) {
	app := newTestAppForPopupMouse(t)
	// Narrow terminal: the editor area leaves just enough room that the
	// find form fits but a wider replace form would run off the right edge.
	app.width = 90
	app.height = 30
	app.EditorState.ActiveView = "/tmp/foo.go"

	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	fw := app.windowManager.Window(string(ui.OverlayFindReplace))
	if fw == nil {
		t.Fatal("ctrl+f did not open the find popup")
	}
	fx, _ := fw.Position()
	fwW, _ := fw.Size()
	if fx+fwW > app.width {
		t.Fatalf("find form opened off screen: x=%d w=%d screen=%d", fx, fwW, app.width)
	}

	// Transform while the popup owns focus.
	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'h', Mod: tea.ModCtrl})
	if app.FindReplace == nil || !app.FindReplace.ReplaceMode {
		t.Fatal("ctrl+h did not transform the popup")
	}
	fw2 := app.windowManager.Window(string(ui.OverlayFindReplace))
	if fw2 == nil {
		t.Fatal("transform lost the window")
	}
	x, y := fw2.Position()
	w, h := fw2.Size()
	if x+w > app.width {
		t.Fatalf("replace form runs off the right edge: x=%d w=%d screen=%d", x, w, app.width)
	}
	if y+h > app.height {
		t.Fatalf("replace form runs off the bottom: y=%d h=%d screen=%d", y, h, app.height)
	}
	if x < 0 || y < 0 {
		t.Fatalf("replace form positioned off the top/left: x=%d y=%d", x, y)
	}

	// Transform back — same contract.
	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	fw3 := app.windowManager.Window(string(ui.OverlayFindReplace))
	if fw3 == nil {
		t.Fatal("reverse transform lost the window")
	}
	x3, _ := fw3.Position()
	w3, _ := fw3.Size()
	if x3+w3 > app.width {
		t.Fatalf("find form runs off the right edge after reverse transform: x=%d w=%d screen=%d", x3, w3, app.width)
	}
}
