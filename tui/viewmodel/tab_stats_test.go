package viewmodel

import (
	"strings"
	"testing"

	"crude/tui/screens/termdock"
	"crude/tui/screens/workspacehub"
	"crude/workspace"
)

// The header's tab count opens the Workspace panel's Tabs page in a window
// with the pointed-to agent selected and its details open. No second Open
// Tabs window exists.
func TestHeaderTabCountOpensWorkspaceTabs(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 160, 40
	app.AddOrchestrator("root")
	runAppCommand(t, app, app.openWorkspaceTabs("agent:root"))
	if app.hub.Active() != workspacehub.TabTabs || !app.panelShown(termdock.PanelWorkspace) {
		t.Fatalf("Workspace Tabs page is not shown: tab=%s", app.hub.Active())
	}
	tab := app.panelTab(termdock.PanelWorkspace)
	if tab == nil || !app.Win.IsFloating(tab.Group) {
		t.Fatal("the Workspace panel did not open in its own window")
	}
	if item, ok := app.hubTabs.Selected(); !ok || item.ID != "agent:root" || !app.hubTabs.IsExpanded("agent:root") {
		t.Fatalf("the pointed-to agent is not selected and open: %+v", item)
	}
	// A second click switches the shown hub instead of opening another one.
	runAppCommand(t, app, app.openLocalHistory())
	runAppCommand(t, app, app.openWorkspaceTabs(""))
	if app.hub.Active() != workspacehub.TabTabs || app.panelTab(termdock.PanelWorkspace).ID != tab.ID {
		t.Fatal("the Workspace panel moved instead of switching its tab")
	}
}

func TestOpenAgentDetailsFollowBackgroundSummaryLifecycle(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 160, 40
	app.AddOrchestrator("root")
	apply := func(phase string) {
		wsOf(app).ApplyAgentEvent(workspace.AgentEvent{AgentID: "root", Type: "context_precompaction", Data: map[string]any{"phase": phase, "request_id": "summary"}})
	}
	apply("started")
	runAppCommand(t, app, app.openWorkspaceTabs("agent:root"))
	screen := app.hubTabs
	if !screen.IsExpanded("agent:root") {
		t.Fatal("setup: agent details did not expand")
	}
	detail := func() string {
		item, ok := screen.Selected()
		if !ok {
			t.Fatal("agent selection was lost")
		}
		for _, line := range item.Lines {
			if line.Label == "Context summary" {
				return line.Value
			}
		}
		return ""
	}
	if !strings.Contains(detail(), "Preparing") {
		t.Fatal("setup: summary preparation not visible")
	}
	apply("ready")
	app.syncWorkspacePanels()
	if !screen.IsExpanded("agent:root") || !strings.Contains(detail(), "summary ready") {
		t.Fatalf("open disclosure stayed stale or collapsed: %q", detail())
	}
	apply("discarded")
	app.syncWorkspacePanels()
	if !screen.IsExpanded("agent:root") || detail() != "" {
		t.Fatalf("discarded summary remained in open disclosure: %q", detail())
	}
}

// Open agents and previous sessions are separate Workspace tabs.
func TestPreviousSessionsHaveTheirOwnWorkspaceTab(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	for _, item := range app.statusTabItems() {
		if item.ID == "previous-sessions" {
			t.Fatal("previous sessions appear on the Tabs page")
		}
	}
	items := app.workspaceSessionItems()
	if len(items) == 0 || items[0].ID != "previous-sessions" || items[len(items)-1].ID != "sessions-empty" {
		t.Fatalf("Sessions page items: %+v", items)
	}
}
