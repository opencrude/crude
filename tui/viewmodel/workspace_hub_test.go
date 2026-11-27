package viewmodel

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	historyscreen "crude/tui/screens/localhistory"
	"crude/tui/screens/statusoverview"
	"crude/tui/screens/termdock"
	"crude/tui/screens/workspacehub"
	"crude/tui/ui"
)

// clickHubTab clicks the painted label of one Workspace panel tab, as a
// user does.
func clickHubTab(t *testing.T, app *App, label string) {
	t.Helper()
	app.paintShellFrame()
	strip, ok := app.screenRectOf(workspacehub.StripID)
	if !ok || strip.Empty() {
		t.Fatal("the Workspace panel tab strip is not on screen")
	}
	row := rowText(app, strip.Y)
	at := strings.Index(row, label)
	if at < 0 {
		t.Fatalf("the tab strip does not show %q: %q", label, row)
	}
	x := utf8.RuneCountInString(row[:at])
	_, cmd := app.Update(tea.MouseClickMsg{X: x + 1, Y: strip.Y, Button: tea.MouseLeft})
	runAppCommand(t, app, cmd)
	app.paintShellFrame()
}

// In its own window the Workspace panel keeps keyboard focus between keys:
// Tab walks the Local History controls, the strip and the Errand link, and
// returns to the list. A pane window's focus is read from its own host.
func TestWorkspacePanelWindowTabWalksEveryControl(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 160, 40
	app.AddOrchestrator("root")
	runAppCommand(t, app, app.openLocalHistory())
	app.paintShellFrame()
	if app.focusedHistoryControl() != historyscreen.EventsID {
		t.Fatalf("Local History opened with focus on %q", app.focusedHistoryControl())
	}
	seen := map[ui.ComponentID]bool{}
	for range 8 {
		before := app.focusedHistoryControl()
		_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		runAppCommand(t, app, cmd)
		after := app.focusedHistoryControl()
		if after == before {
			t.Fatalf("Tab did not move focus from %q", before)
		}
		seen[after] = true
	}
	for _, want := range []ui.ComponentID{workspacehub.StripID, workspacehub.ErrandID, historyscreen.FilterID, historyscreen.EventsID} {
		if !seen[want] {
			t.Fatalf("Tab never reached %q; reached %v", want, seen)
		}
	}
}

// closeWorkspacePanelWindow closes the Workspace panel's window as its ×
// does.
func closeWorkspacePanelWindow(app *App) tea.Cmd {
	if tab := app.panelTab(termdock.PanelWorkspace); tab != nil && app.Win.IsFloating(tab.Group) {
		return app.closePaneWindowTabs(tab.Group)
	}
	return nil
}

// expandHubTabsRow clicks the first rows of the Workspace panel's Tabs list,
// through the host that mounts it, until row id opens.
func expandHubTabsRow(app *App, id string) bool {
	list, ok := app.screenRectOf(statusoverview.ListID)
	if !ok || list.Empty() {
		return false
	}
	host := app.hostOf(statusoverview.ListID)
	for y := list.Y; y < list.Y+4 && !app.hubTabs.IsExpanded(id); y++ {
		host.Handle(tea.MouseClickMsg{X: list.X + 4, Y: y, Button: tea.MouseLeft})
	}
	return app.hubTabs.IsExpanded(id)
}
