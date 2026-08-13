package viewmodel

import (
	"runtime"
	"testing"

	"crude/tui/ui"
	"crude/workspace"
)

func TestDockAndPromoteTerminalUsesPanelDeckAndOverlay(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-app PTYs are unix-only")
	}
	app := newFullTestApp(t)
	app.width, app.height = 120, 40
	mgr := app.termMgr()
	if mgr == nil {
		t.Fatal("TermMgr missing")
	}
	info, err := mgr.Create(workspace.TerminalCreateOpts{
		Command: "/bin/cat",
		CWD:     t.TempDir(),
		Cols:    40,
		Rows:    12,
		Title:   "cat",
	})
	if err != nil {
		t.Skipf("pty create: %v", err)
	}
	t.Cleanup(func() { mgr.Close(info.ID) })

	app.dockTerminal(info.ID)
	projection := app.activeSessionDeck()
	if projection == nil || projection.deck == nil {
		t.Fatal("expected session deck")
	}
	found := false
	for _, p := range projection.deck.Panels() {
		if p.ID == terminalPanelID(info.ID) {
			found = true
			if p.Policy != ui.PanelInteractive {
				t.Fatalf("policy=%v want interactive", p.Policy)
			}
		}
	}
	if !found {
		t.Fatal("docked terminal missing from PanelDeck")
	}
	if app.overlayHost.IsOpen(ui.TerminalOverlayID(info.ID)) {
		t.Fatal("docked terminal must not also be a floating window")
	}

	app.promoteTerminal(info.ID)
	if !app.overlayHost.IsOpen(ui.TerminalOverlayID(info.ID)) {
		t.Fatal("promoted terminal must open a floating window")
	}
	for _, p := range projection.deck.Panels() {
		if p.ID == terminalPanelID(info.ID) {
			t.Fatal("promoted terminal must leave the deck")
		}
	}
	got, ok := mgr.Get(info.ID)
	if !ok || got.Placement != workspace.TerminalFloating {
		t.Fatalf("placement=%v floating=%v", got.Placement, ok)
	}

	app.dockTerminal(info.ID)
	if app.overlayHost.IsOpen(ui.TerminalOverlayID(info.ID)) {
		t.Fatal("dock must close the floating window without killing the PTY")
	}
	if _, ok := mgr.Get(info.ID); !ok {
		t.Fatal("PTY must survive dock/promote")
	}
}

func TestPromoteTwoTerminalsStacksByZOrder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-app PTYs are unix-only")
	}
	app := newFullTestApp(t)
	app.width, app.height = 120, 40
	mgr := app.termMgr()
	first, err := mgr.Create(workspace.TerminalCreateOpts{Command: "/bin/cat", CWD: t.TempDir(), Cols: 20, Rows: 8})
	if err != nil {
		t.Skipf("pty create: %v", err)
	}
	second, err := mgr.Create(workspace.TerminalCreateOpts{Command: "/bin/cat", CWD: t.TempDir(), Cols: 20, Rows: 8})
	if err != nil {
		t.Skipf("pty create: %v", err)
	}
	t.Cleanup(func() {
		mgr.Close(first.ID)
		mgr.Close(second.ID)
	})
	app.promoteTerminal(first.ID)
	app.promoteTerminal(second.ID)
	top := app.windowManager.TopWindow()
	if top == nil || top.ID != string(ui.TerminalOverlayID(second.ID)) {
		t.Fatalf("top=%v want second terminal", top)
	}
	app.windowManager.FocusWindow(string(ui.TerminalOverlayID(first.ID)))
	top = app.windowManager.TopWindow()
	if top == nil || top.ID != string(ui.TerminalOverlayID(first.ID)) {
		t.Fatalf("after focus top=%v want first terminal", top)
	}
}
