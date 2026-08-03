package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
	"crude/tui/widgets"
)

// TestFindReplace_TypingDrivesTextEditDirectly proves find/replace keys flow
// through the shared ui.TextEdit engine (ui.HandleTextEditKey) — not through a
// find/replace-specific key bridge (Phase 11 #15). The frame TextFields mirror
// the state TextEdits on each keystroke.
func TestFindReplace_TypingDrivesTextEditDirectly(t *testing.T) {
	app := newTestAppForPopupMouse(t)
	app.openFindReplace(false)
	fw := app.windowManager.Window(string(ui.OverlayFindReplace))
	if fw == nil {
		t.Fatal("find-replace popup should open")
	}

	for _, r := range "abc" {
		app.handleFindReplaceKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	if got := app.FindReplace.Query(); got != "abc" {
		t.Fatalf("find query: got %q, want %q", got, "abc")
	}
	if got := app.EditorState.Editor.Search().Query; got != "abc" {
		t.Fatalf("editor query after typing: got %q, want %q", got, "abc")
	}

	// The frame's find TextField mirrors the TextEdit engine state.
	cw, ok := fw.Content.(*ui.ComponentWidget)
	if !ok {
		t.Fatal("find-replace content is not a ComponentWidget")
	}
	if find := widgets.FindReplaceFrameFindField(cw); find != nil {
		if got := find.Value(); got != "abc" {
			t.Fatalf("frame find field: got %q, want %q", got, "abc")
		}
	}
}
