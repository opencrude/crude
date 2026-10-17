package viewmodel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
)

// TestFindReplace_FullUserJourney simulates the complete find/replace
// workflow exactly as a user drives it: keys through the real dispatch
// chain, clicks at painted (Host screen) positions. This is the
// integration guard for the interaction overhaul: if any layer (dispatch
// ordering, hit geometry, state sync) regresses, this fails.
func TestFindReplace_FullUserJourney(t *testing.T) {
	app := newTestAppForPopupMouse(t)
	app.EditorState.ActiveView = "/tmp/foo.go" // editor owns focus (FocusEditor)
	app.EditorState.Editor.ReplaceDocument("alpha beta\ngamma alpha\ndelta\n")
	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl}) // open Find
	if app.FindReplace == nil {
		t.Fatal("ctrl+f did not open the find popup from the editor")
	}

	// Type the query.
	for _, r := range "alpha" {
		app.dispatchKeyPress(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if app.FindReplace.MatchCount != 2 {
		t.Fatalf("after typing: MatchCount=%d want 2", app.FindReplace.MatchCount)
	}

	// Toggle case sensitivity via the keyboard chord (must not open the
	// composer).
	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt})
	if !app.FindReplace.CaseSensitive {
		t.Fatal("alt+c did not toggle case sensitivity")
	}
	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt}) // off again

	// Transform to Replace via Ctrl+H while the popup owns focus, then type
	// the replacement into the (focused) find field; Tab to replace.
	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'h', Mod: tea.ModCtrl})
	if !app.FindReplace.ReplaceMode {
		t.Fatal("ctrl+h did not transform to replace mode")
	}
	app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyTab})
	if app.FindReplace.FocusedField != 1 {
		t.Fatal("tab did not focus the replace field")
	}
	for _, r := range "ALPHA" {
		app.dispatchKeyPress(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	// Click the Replace All button at its painted position.
	fw := app.windowManager.Window(string(ui.OverlayFindReplace))
	if fw == nil {
		t.Fatal("popup window lost mid-journey")
	}
	cw, ok := fw.Content.(*ui.ComponentWidget)
	if !ok {
		t.Fatal("content is not a ComponentWidget")
	}
	ww, hh := fw.Size()
	cw.Host().Resize(ww, hh)
	cw.Host().Layout()
	all, ok := ui.FindReplaceActionBounds(cw, "find.replace.all")
	if !ok {
		t.Fatal("find.replace.all button has no bounds")
	}
	app.clickFindReplace(all.X+all.W/2, all.Y+all.H/2, nil)
	got := app.EditorState.Editor.Value()
	if !strings.Contains(got, "ALPHA beta") || !strings.Contains(got, "gamma ALPHA") {
		t.Fatalf("Replace All click did not substitute; buffer=%q", got)
	}

	// Esc closes.
	app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	if app.FindReplace != nil || app.windowManager.Window("find-replace") != nil {
		t.Fatal("esc did not close the popup")
	}
}
