package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
	"crude/tui/wm"
)

// TestViewmodel_OverlayOpenCloseUsesHostFocusScopes proves the REAL overlay
// open/close paths (OverlayHost.Open/Close, which every viewmodel popup routes
// through) push and pop focus scopes on the shell Host.
func TestViewmodel_OverlayOpenCloseUsesHostFocusScopes(t *testing.T) {
	app := newTestApp(t)
	app.overlayHost.SetFocusHost(app.rootHost)

	// Open the command palette via the real viewmodel path.
	app.toggleCommandPalette()
	if got := app.rootHost.FocusScopes(); len(got) != 1 || got[0] != ui.ComponentID(string(ui.OverlayCommandPalette)) {
		t.Fatalf("scopes=%v want [command-palette]", got)
	}
	if fw := app.windowManager.Focused(); fw == nil || fw.ID != string(ui.OverlayCommandPalette) {
		t.Fatalf("wm focused=%v want command-palette", app.windowManager.Focused())
	}

	// Close it via the real path (Esc handler routes through OverlayHost.Close).
	app.overlayHost.Close(ui.OverlayCommandPalette)
	if got := app.rootHost.FocusScopes(); len(got) != 0 {
		t.Fatalf("scopes=%v want empty after close", got)
	}
	if app.windowManager.Focused() != nil {
		t.Fatalf("wm focused=%v want nil after close", app.windowManager.Focused())
	}
}

// TestViewmodel_NestedOverlayCloseRestoresExactPriorWindowFocus drives the
// exit-criterion scenario end to end: a keep-route popover stays open while
// the user clicks the shell (WM focus drops to nil), a modal opens over it,
// and closing the modal must restore the shell focus, NOT snap to the stale
// popover on top of the z-order.
func TestViewmodel_NestedOverlayCloseRestoresExactPriorWindowFocus(t *testing.T) {
	app := newTestApp(t)
	app.overlayHost.SetFocusHost(app.rootHost)

	// Open a keep-route popover (mirrors find-replace).
	pop := ui.NewPopupContent([]string{"╭─╮", "│ │", "╰─╯"})
	fwA := wm.NewFloatingWindow(string(ui.OverlayFindReplace), "", pop, 3, 3,
		wm.WithDecoration(false),
		wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideKeepRoute),
	)
	fwA.SetPosition(0, 0)
	app.overlayHost.Open(ui.OverlayFindReplace, fwA, ui.DefaultOverlayPolicy(), nil)
	if got := app.rootHost.FocusScopes(); len(got) != 1 || got[0] != ui.ComponentID(string(ui.OverlayFindReplace)) {
		t.Fatalf("scopes=%v want [find-replace]", got)
	}

	// The user clicks the shell (e.g. the editor): the popover stays open but
	// the WM drops window focus.
	app.windowManager.HandleMouse(tea.MouseClickMsg{X: 50, Y: 50, Button: tea.MouseLeft})
	if app.windowManager.Focused() != nil {
		t.Fatalf("shell click must clear WM focus, got %v", app.windowManager.Focused())
	}

	// A modal dialog opens over the popover.
	fwB := wm.NewFloatingWindow(string(ui.OverlayDirtyClose), "", ui.NewPopupContent([]string{"╭─╮", "│ x │", "╰─╯"}), 5, 3,
		wm.WithDecoration(false),
		wm.WithFocusable(true),
	)
	fwB.SetPosition(30, 5)
	app.overlayHost.Open(ui.OverlayDirtyClose, fwB, ui.ModalDialogPolicy(), nil)
	if fw := app.windowManager.Focused(); fw == nil || fw.ID != string(ui.OverlayDirtyClose) {
		t.Fatalf("modal must own WM focus, got %v", app.windowManager.Focused())
	}
	if got := app.rootHost.FocusScopes(); len(got) != 2 {
		t.Fatalf("scopes=%v want 2", got)
	}

	// Close the modal: WM focus must return to the shell (nil), NOT to the
	// stale find-replace window still sitting on top of the z-order.
	app.overlayHost.Close(ui.OverlayDirtyClose)
	if fw := app.windowManager.Focused(); fw != nil {
		t.Fatalf("after modal close WM focus=%v want nil (restored shell focus), not the stale popover", fw)
	}
	if got := app.rootHost.FocusScopes(); len(got) != 1 || got[0] != ui.ComponentID(string(ui.OverlayFindReplace)) {
		t.Fatalf("scopes=%v want [find-replace]", got)
	}

	// Close the popover: the stack empties and the shell owns focus again.
	app.overlayHost.Close(ui.OverlayFindReplace)
	if got := app.rootHost.FocusScopes(); len(got) != 0 {
		t.Fatalf("scopes=%v want empty", got)
	}
	if app.windowManager.Focused() != nil {
		t.Fatalf("wm focused=%v want nil", app.windowManager.Focused())
	}
}
