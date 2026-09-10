package viewmodel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
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
	if find := ui.FindReplaceFrameFindField(cw); find != nil {
		if got := find.Value(); got != "abc" {
			t.Fatalf("frame find field: got %q, want %q", got, "abc")
		}
	}
}

// TestFindReplace_ButtonsMirrorActions pins the VSCode-style action row:
// find mode offers ↑/↓ only; replace mode offers Replace/Replace All
// plus the shared ↑/↓ pair. Buttons resolve via the button language
// (label-sized ui.Buttons, action-semantic variants) and activate through
// the same callbacks as the keyboard paths.
func TestFindReplace_ButtonsMirrorActions(t *testing.T) {
	app := newTestAppForPopupMouse(t)
	// Find mode: navigation pair only.
	findBar := ui.BuildFindReplaceFrame(app.Theme, ui.FindReplaceFrameSpec{
		FindValue: "x", MatchCount: 1, CurrentIdx: 0, Width: 60, Height: 6,
	})
	findActions := ui.FindReplaceButtonLabels(findBar)
	if len(findActions) != 2 || findActions[0] != "↑" || findActions[1] != "↓" {
		t.Fatalf("find action row = %v, want [↑ ↓]", findActions)
	}

	// Replace mode: Replace/Replace All prepended to the shared pair.
	replaceBar := ui.BuildFindReplaceFrame(app.Theme, ui.FindReplaceFrameSpec{
		FindValue: "x", ReplaceValue: "y", ReplaceMode: true,
		MatchCount: 3, CurrentIdx: 1, Width: 60, Height: 7,
	})
	replaceActions := ui.FindReplaceButtonLabels(replaceBar)
	if len(replaceActions) != 4 || replaceActions[0] != "Replace" || replaceActions[1] != "Replace All" ||
		replaceActions[2] != "↑" || replaceActions[3] != "↓" {
		t.Fatalf("replace action row = %v, want [Replace Replace All ↑ ↓]", replaceActions)
	}
}

// TestFindReplace_ActionButtonsFireCallbacks pins the action-row mouse
// path: clicking a painted button must fire the same editor callback as
// the keyboard chord. Coordinates resolve from painted button geometry
// (FindReplaceActionBounds — Host screen rects), never from assumed rows.
// The editor holds real matches so the buttons are enabled and the
// callbacks observably fire.
func TestFindReplace_ActionButtonsFireCallbacks(t *testing.T) {
	app := newTestAppForPopupMouse(t)
	// Real content with two "needle" matches so navigation/replace have
	// something to act on.
	app.EditorState.Editor.ReplaceDocument("needle one\nplain line\nneedle two\n")
	app.openFindReplace(true)
	fw := app.windowManager.Window(string(ui.OverlayFindReplace))
	if fw == nil {
		t.Fatal("replace popup should open")
	}
	app.FindReplace.SetQuery("needle")
	app.FindReplace.SetReplace("NEEDLE")
	if app.FindReplace.OnChange != nil {
		app.FindReplace.OnChange() // push query+replace into the editor
	}
	if app.FindReplace.MatchCount != 2 {
		t.Fatalf("precondition: want 2 matches, got %d", app.FindReplace.MatchCount)
	}
	fw = app.windowManager.Window(string(ui.OverlayFindReplace))
	cw, ok := fw.Content.(*ui.ComponentWidget)
	if !ok {
		t.Fatal("replace content is not a ComponentWidget")
	}
	// Ensure layout matches painted size before resolving geometry.
	ww, hh := fw.Size()
	cw.Host().Resize(ww, hh)
	cw.Host().Layout()

	// Clicking "Down" advances to the next match through OnNext.
	next, ok := ui.FindReplaceActionBounds(cw, "find.next")
	if !ok {
		t.Fatal("find.next button has no bounds")
	}
	if next.Y >= hh {
		t.Fatalf("find.next bounds %+v outside popup height %d (offset bug)", next, hh)
	}
	cur := app.FindReplace.CurrentIdx
	app.clickFindReplace(next.X+next.W/2, next.Y+next.H/2)
	if app.FindReplace == nil {
		t.Fatal("popup closed after Down click")
	}
	if app.FindReplace.CurrentIdx == cur {
		t.Fatal("Down click did not advance the match (OnNext did not fire)")
	}

	// Clicking "Replace" replaces the current match through OnReplace.
	one, ok := ui.FindReplaceActionBounds(cw, "find.replace.one")
	if !ok {
		t.Fatal("find.replace.one button has no bounds in replace mode")
	}
	app.clickFindReplace(one.X+one.W/2, one.Y+one.H/2)
	if got := app.EditorState.Editor.Value(); !strings.Contains(got, "NEEDLE") {
		t.Fatalf("Replace click did not substitute the match; buffer=%q", got)
	}
	if _, ok := ui.FindReplaceActionBounds(cw, "find.replace.all"); !ok {
		t.Fatal("find.replace.all button has no bounds in replace mode")
	}

	// Clicking outside every button resolves to nothing (no misfire).
	if act := app.findReplaceButtonAt(0, 0); act != "" {
		t.Fatalf("title corner resolved to action %q, want none", act)
	}
}

// TestFindReplace_TransformKeepsWindowSmooth pins the Ctrl+F ↔ Ctrl+H
// transform contract: same window, same position, and the measured tree
// always ends on the button row (replace one input-row taller than find).
// The window keeps position/focus and adopts the measured height; the
// button row is never clipped into the border.
func TestFindReplace_TransformKeepsWindowSmooth(t *testing.T) {
	app := newTestAppForPopupMouse(t)
	app.openFindReplace(false)
	fw := app.windowManager.Window(string(ui.OverlayFindReplace))
	if fw == nil {
		t.Fatal("find popup should open")
	}
	fx, fy := fw.Position()
	_, fh0 := fw.Size()
	if fh0 != 5 {
		t.Fatalf("find form height = %d, want 5 for find-row+gap+buttons", fh0)
	}

	app.openFindReplace(true)
	fw2 := app.windowManager.Window(string(ui.OverlayFindReplace))
	if fw2 == nil {
		t.Fatal("replace popup should open after transform")
	}
	if fw2 != fw {
		t.Fatal("transform must reuse the window (no close/reopen flicker)")
	}
	if x, y := fw2.Position(); x != fx || y != fy {
		t.Fatalf("transform moved the window: was (%d,%d), now (%d,%d)", fx, fy, x, y)
	}
	if _, fh1 := fw2.Size(); fh1 != 9 {
		t.Fatalf("replace form height = %d, want 9 (find + replace + counter + gaps + buttons)", fh1)
	}

	app.openFindReplace(false)
	fw3 := app.windowManager.Window(string(ui.OverlayFindReplace))
	if fw3 == nil {
		t.Fatal("find popup should reopen after reverse transform")
	}
	if _, fh2 := fw3.Size(); fh2 != fh0 {
		t.Fatalf("reverse transform height = %d, want %d", fh2, fh0)
	}
}
