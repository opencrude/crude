package viewmodel

import (
	"testing"

	"crude/tui/screens/statusoverview"
	"crude/tui/screens/termdock"
)

func TestWorkspacePanelRowClickRoutesThroughRootHost(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 30
	app.AddOrchestrator("root")
	app.toggleWorkspacePanel(termdock.PanelWorkspace)
	app.paintShellFrame()

	if app.hostOf(statusoverview.ListID) != app.rootHost {
		t.Fatal("the docked Workspace panel's Tabs list is not mounted in the shell host")
	}
	if !expandHubTabsRow(app, "agent:root") {
		t.Fatal("workspace-panel row click did not expand through the shell host")
	}
}
