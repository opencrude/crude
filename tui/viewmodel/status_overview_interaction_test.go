package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	statusoverview "crude/tui/screens/statusoverview"
	"crude/tui/ui"
)

func TestStatusOverviewRowClickRoutesThroughFloatingWindowHost(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 36
	app.AddOrchestrator("root")
	app.openStatusOverview(statusoverview.ViewTabs, "")
	app.paintShellFrame()
	fw := app.windowManager.Window(string(ui.OverlayStatusOverview))
	if fw == nil || app.statusOverview == nil {
		t.Fatal("status overview did not open")
	}
	widget, ok := fw.Content.(*ui.WindowFrameWidget)
	if !ok || widget.Host() == nil {
		t.Fatalf("window content=%T", fw.Content)
	}
	list := app.statusOverview.ListBounds()
	if list.Empty() {
		t.Fatal("status overview list has no bounds")
	}
	winX, winY := fw.Position()
	content := fw.ContentBounds()
	body := widget.Frame().ContentBounds()
	screenX, screenY := winX+content.X+body.X+list.X+3, winY+content.Y+body.Y+list.Y
	if cmd := app.forwardEventToWindowContent(fw, tea.MouseClickMsg{X: screenX, Y: screenY, Button: tea.MouseLeft}); cmd != nil {
		_ = cmd()
	}
	if !app.statusOverview.IsExpanded("agent:root") {
		t.Fatal("floating host click did not expand status row")
	}
}
