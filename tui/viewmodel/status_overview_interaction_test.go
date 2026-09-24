package viewmodel

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	statusoverview "crude/tui/screens/statusoverview"
	"crude/tui/screens/termdock"
)

func TestWorkspacePanelRowClickRoutesThroughRootHost(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 30
	app.AddOrchestrator("root")
	app.toggleWorkspacePanel(termdock.PanelWorkspace)
	app.paintShellFrame()

	content := app.termDock.Panel(termdock.PanelWorkspace)
	screen, ok := content.(*statusoverview.Surface)
	if !ok || screen == nil {
		t.Fatal("workspace status panel was not mounted")
	}
	list := screen.ListBounds()
	dock := app.termDockRect
	x := dock.X + list.X + 4
	for y := dock.Y + 1 + list.Y; y < dock.Y+1+list.Y+4 && !screen.IsExpanded("agent:root"); y++ {
		app.rootHost.Handle(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	}
	if !screen.IsExpanded("agent:root") {
		t.Fatal("workspace-panel row click did not expand through the shell host")
	}
}
