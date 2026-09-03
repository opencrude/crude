package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	historyscreen "crude/tui/screens/localhistory"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/wm"
)

func TestLocalHistoryWindowUsesCanonicalFrameInteractions(t *testing.T) {
	app := &App{Theme: theme.DefaultDark(), width: 120, height: 40}
	fw := app.newLocalHistoryWindow(historyscreen.New(app.Theme))
	if !fw.Decorated() || !fw.Movable() || !fw.Closable() || !fw.Resizable() {
		t.Fatalf("window interactions decorated=%v movable=%v closable=%v resizable=%v", fw.Decorated(), fw.Movable(), fw.Closable(), fw.Resizable())
	}
	fw.SetPosition(10, 5)
	w, h := fw.Size()
	if edge := fw.HitTestEdge(10+w-1, 5+h-1); edge != wm.EdgeBottomRight {
		t.Fatalf("bottom-right edge=%v", edge)
	}
	if !fw.IsOnCloseButton(10+w-2, 5) {
		t.Fatal("close button has no WM hit region")
	}
}

func TestLocalHistoryWindowCloseAndResizeRouteThroughWindowManager(t *testing.T) {
	th := theme.DefaultDark()
	app := &App{Theme: th, width: 120, height: 40, windowManager: wm.NewWindowManager()}
	app.overlayHost = ui.NewOverlayHost(app.windowManager)
	fw := app.newLocalHistoryWindow(historyscreen.New(th))
	fw.SetPosition(10, 5)
	app.overlayHost.Open(ui.OverlayLocalHistory, fw, ui.ModalDialogPolicy(), nil)
	w, h := fw.Size()
	app.dispatchMouseMotion(tea.MouseMotionMsg{X: 10 + w/2, Y: 5 + h - 1})
	if fw.HoverEdge() != wm.EdgeBottom {
		t.Fatalf("border hover=%v want bottom", fw.HoverEdge())
	}
	app.windowManager.HandleMouse(tea.MouseClickMsg{X: 10 + w - 1, Y: 5 + h - 1, Button: tea.MouseLeft})
	app.windowManager.HandleMouseMotion(tea.MouseMotionMsg{X: 10 + w + 3, Y: 5 + h + 2, Button: tea.MouseLeft})
	app.windowManager.HandleMouseRelease(tea.MouseReleaseMsg{X: 10 + w + 3, Y: 5 + h + 2, Button: tea.MouseLeft})
	grownW, grownH := fw.Size()
	if grownW <= w || grownH <= h {
		t.Fatalf("resize=%dx%d want larger than %dx%d", grownW, grownH, w, h)
	}
	x, y := fw.Position()
	app.dispatchMouseClick(tea.MouseClickMsg{X: x + grownW - 2, Y: y, Button: tea.MouseLeft})
	if app.windowManager.Window(string(ui.OverlayLocalHistory)) != nil {
		t.Fatal("close icon did not remove Local History overlay")
	}
}
