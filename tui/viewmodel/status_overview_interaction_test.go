package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	statusoverview "crude/tui/screens/statusoverview"
	"crude/tui/ui"
)

func TestStatusOverviewDividerRoutesThroughFloatingWindowHost(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 36
	app.AddOrchestrator("root")
	app.openStatusOverview(statusoverview.ViewTabs, "agent:root")
	app.paintShellFrame()
	fw := app.windowManager.Window(string(ui.OverlayStatusOverview))
	if fw == nil || app.statusOverview == nil {
		t.Fatal("status overview did not open")
	}
	widget, ok := fw.Content.(*ui.WindowFrameWidget)
	if !ok || widget.Host() == nil {
		t.Fatalf("window content=%T", fw.Content)
	}
	before := app.statusOverview.SplitFirst()
	list, detail := app.statusOverview.PaneBounds()
	localX := (list.X + list.W + detail.X) / 2
	localY := list.Y + 2
	winX, winY := fw.Position()
	content := fw.ContentBounds()
	screenX, screenY := winX+content.X+localX, winY+content.Y+localY
	if cmd := app.forwardEventToWindowContent(fw, tea.MouseClickMsg{X: screenX, Y: screenY, Button: tea.MouseLeft}); cmd != nil {
		_ = cmd()
	}
	if !widget.Host().HasPointerCapture() {
		t.Fatal("floating host did not retain divider capture")
	}
	app.handlePopupContentHover(fw, screenX+5, screenY, tea.MouseLeft)
	_, cmd := app.dispatchMouseRelease(tea.MouseReleaseMsg{X: screenX + 5, Y: screenY, Button: tea.MouseLeft})
	if cmd != nil {
		_ = cmd()
	}
	widget.Host().MarkLayout()
	widget.Host().Layout()
	after := app.statusOverview.SplitFirst()
	if after <= before || widget.Host().HasPointerCapture() {
		t.Fatalf("floating divider did not resize/release: before=%d after=%d captured=%v", before, after, widget.Host().HasPointerCapture())
	}
}
