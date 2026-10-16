package viewmodel

import (
	"testing"

	"crude/tui/ui"
	"crude/tui/ui/popup"
	"crude/tui/wm"
)

// TestFindReplaceToggleHitTargets pins the component-driven toggle hit-test:
// each Aa/ab/.* ToggleButton's popup-local bounds must equal its painted
// position (Host screen geometry), so a click at the pill's painted center
// resolves to that toggle, with no band math or assumed content offsets.
func TestFindReplaceToggleHitTargets(t *testing.T) {
	app := newTestAppForPopupMouse(t)
	app.FindReplace = &popup.FindReplaceState{}
	app.FindReplace.SetQuery("x")

	frame := ui.BuildFindReplaceFrame(app.Theme, ui.FindReplaceFrameSpec{Width: 44, Height: 7})
	fw := wm.NewFloatingWindow("find-replace", "", frame, 44, 7,
		wm.WithDecoration(false), wm.WithFocusable(true))
	fw.SetPosition(0, 0)
	app.overlayHost.Open(ui.OverlayFindReplace, fw, ui.DefaultOverlayPolicy(), nil)
	defer app.overlayHost.Close(ui.OverlayFindReplace)

	cw := fw.Content.(*ui.ComponentWidget)
	cw.Host().Resize(44, 7)
	cw.Host().Layout()

	// The Aa/ab/.* toggles sit right-aligned in one 4-cell pill each; a
	// click at each pill's PAINTED center must resolve to that toggle.
	for want := 0; want < 3; want++ {
		r, ok := ui.FindReplaceFrameToggleBounds(cw, want)
		if !ok {
			t.Fatalf("toggle %d has no bounds", want)
		}
		lx := r.X + r.W/2
		ly := r.Y + r.H/2
		if got := app.findReplaceToggleAt(lx, ly); got != want {
			t.Fatalf("findReplaceToggleAt(%d,%d) = %d, want %d", lx, ly, got, want)
		}
	}
	// Points off the toggles must not resolve.
	if got := app.findReplaceToggleAt(0, 0); got != -1 {
		t.Fatalf("findReplaceToggleAt(0,0) = %d, want -1", got)
	}
}
