package viewmodel

import (
	"strings"

	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/gitpanel"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/workspace"
)

// A dock surface becomes an ordinary main tab: it paints in the main area,
// its footer pill reveals the tab instead of the dock, and closing the tab
// returns it to the dock.
func TestDockSurfaceOpensAsMainTab(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 140, 44
	app.gitPanel.SetSnapshot(gitDockSnapshot(app.ws.RootPath(), workspace.GitChange{Path: "a.go", Area: workspace.GitAreaWorking, Code: "M"}))
	app.toggleGitPanel()
	if !app.termDock.IsOpen() {
		t.Fatal("git dock did not open")
	}
	app.handleHostAction(&ui.Action{ID: termdock.ActionOpenAsTab, Payload: termdock.PanelGit})
	tab := app.Win.ActiveTab()
	if tab == nil || tab.Resource.Kind != window.ResourcePanel || tab.Resource.ID != termdock.PanelGit {
		t.Fatalf("active tab = %+v", tab)
	}
	if app.termDock.IsOpen() && app.termDock.ActivePanel() == termdock.PanelGit {
		t.Fatal("the surface is mounted in both the dock and a tab")
	}
	if !app.gitPanelOpen() {
		t.Fatal("a git tab is not treated as a visible git surface")
	}
	app.paintShellFrame()
	if plain := bufferPlain(app.mainSurf.Buffer()); !strings.Contains(plain, "Changes 1") || !strings.Contains(plain, "a.go") {
		t.Fatalf("git tab did not paint:\n%s", plain)
	}
	if focused := app.rootHost.Focused(); focused != gitpanel.ListID {
		t.Fatalf("focus = %s", focused)
	}
	_, _ = app.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	if app.gitPanel.Mode() != gitpanel.ModeHistory {
		t.Fatalf("keys did not reach the git tab: mode=%s", app.gitPanel.Mode())
	}
	app.toggleGitPanel()
	if app.termDock.IsOpen() || app.Win.ActiveTab().Resource.ID != termdock.PanelGit {
		t.Fatal("the footer toggle opened the dock instead of revealing the tab")
	}
	_ = app.handleAgentTabClose(app.Win.IndexOf(app.Win.ActiveTab().ID))
	if app.panelTab(termdock.PanelGit) != nil || app.placementOf(termdock.PanelGit) != placeDock {
		t.Fatal("closing the tab did not return the surface to the dock")
	}
}

// A surface opens as a window: its tab floats in a pane window, keys reach
// it there, and the title-bar Dock button returns it to the dock.
func TestDockSurfaceOpensAsWindowAndDocks(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 140, 44
	app.gitPanel.SetSnapshot(gitDockSnapshot(app.ws.RootPath(), workspace.GitChange{Path: "a.go", Area: workspace.GitAreaWorking, Code: "M"}))
	app.toggleGitPanel()
	app.handleHostAction(&ui.Action{ID: termdock.ActionOpenAsWindow, Payload: termdock.PanelGit})
	tab := app.panelTab(termdock.PanelGit)
	if tab == nil || !app.Win.IsFloating(tab.Group) {
		t.Fatalf("surface tab did not float: %+v", tab)
	}
	group := tab.Group
	app.paintShellFrame()
	if !app.overlayHost.IsOpen(paneWindowID(group)) || !app.panelShown(termdock.PanelGit) {
		t.Fatal("window not open")
	}
	if app.termDock.IsOpen() && app.termDock.ActivePanel() == termdock.PanelGit {
		t.Fatal("the surface is mounted in both the dock and a window")
	}
	if app.focusTarget() == FocusFloatingWindow {
		t.Fatal("a pane window was treated as a popup that owns keys")
	}
	_, _ = app.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	if app.gitPanel.Mode() != gitpanel.ModeHistory {
		t.Fatalf("keys did not reach the git window: mode=%s", app.gitPanel.Mode())
	}
	app.toggleGitPanel()
	if app.termDock.IsOpen() && app.termDock.ActivePanel() == termdock.PanelGit {
		t.Fatal("the pill opened the dock while the surface is in a window")
	}
	app.dockPaneWindow(group)
	app.paintShellFrame()
	if app.overlayHost.IsOpen(paneWindowID(group)) || app.panelTab(termdock.PanelGit) != nil || !app.termDock.IsOpen() || app.termDock.ActivePanel() != termdock.PanelGit {
		t.Fatal("Dock did not return the surface to the dock")
	}
}

// A surface in its own window moves into the main tabs with the window's ⧉
// button, the dock header's glyph for a main tab: the window closes, the
// surface becomes an ordinary main tab that keeps keys, and closing that tab
// returns it to the dock. ⇲ keeps returning it to the bottom panel.
func TestDockSurfaceWindowMovesToMainTabs(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	_, _ = app.Update(tea.WindowSizeMsg{Width: 140, Height: 44})
	app.gitPanel.SetSnapshot(gitDockSnapshot(app.ws.RootPath(), workspace.GitChange{Path: "a.go", Area: workspace.GitAreaWorking, Code: "M"}))
	app.toggleGitPanel()
	app.handleHostAction(&ui.Action{ID: termdock.ActionOpenAsWindow, Payload: termdock.PanelGit})
	tab := app.panelTab(termdock.PanelGit)
	if tab == nil || !app.Win.IsFloating(tab.Group) {
		t.Fatalf("surface tab did not float: %+v", tab)
	}
	group := tab.Group
	app.paintShellFrame()
	pw := app.mainPanes.windows[group]
	if first := strings.SplitN(paneWindowText(t, app, group), "\n", 2)[0]; !strings.Contains(first, "⧉") || !strings.Contains(first, "⇲") {
		t.Fatalf("a surface window offers no move to the main tabs: %q", first)
	}
	x, y := pw.fw.Position()
	w, _ := pw.fw.Size()
	mainX := x + w - 3 - 2*ui.TitleActionWidth + 1
	_, cmd := app.Update(tea.MouseClickMsg{X: mainX, Y: y, Button: tea.MouseLeft})
	runAppCommand(t, app, cmd)
	_, cmd = app.Update(tea.MouseReleaseMsg{X: mainX, Y: y, Button: tea.MouseLeft})
	runAppCommand(t, app, cmd)
	app.paintShellFrame()
	tab = app.panelTab(termdock.PanelGit)
	if tab == nil || app.Win.IsFloating(tab.Group) || app.overlayHost.IsOpen(paneWindowID(group)) {
		t.Fatalf("⧉ did not move the surface into the main tabs: %+v", tab)
	}
	if app.placementOf(termdock.PanelGit) != placeTab || app.termDock.IsOpen() && app.termDock.ActivePanel() == termdock.PanelGit {
		t.Fatal("the surface left its tab placement or is also docked")
	}
	if active := app.Win.ActiveTab(); active == nil || active.ID != tab.ID {
		t.Fatal("the moved surface is not the shown main tab")
	}
	if plain := bufferPlain(app.mainSurf.Buffer()); !strings.Contains(plain, "a.go") {
		t.Fatalf("the moved git tab did not paint:\n%s", plain)
	}
	_, _ = app.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	if app.gitPanel.Mode() != gitpanel.ModeHistory {
		t.Fatalf("keys did not reach the moved git tab: mode=%s", app.gitPanel.Mode())
	}
	_ = app.handleAgentTabClose(app.Win.IndexOf(tab.ID))
	if app.panelTab(termdock.PanelGit) != nil || app.placementOf(termdock.PanelGit) != placeDock {
		t.Fatal("closing the moved tab did not return the surface to the dock")
	}
}

// Only a window that holds a workspace surface alone offers ⧉; a window of
// files or agents already returns to the main tabs with ⇲.
func TestPaneWindowOffersMainTabsOnlyForSurfaces(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 44)
	openEditorTabs(t, app, "window.go")
	app.openTabInWindow(app.Win.ActiveTab().ID)
	_ = app.View()
	group := app.Win.GroupOf(app.Win.ActiveTab().ID)
	if !app.Win.IsFloating(group) {
		t.Fatal("the file did not open in a window")
	}
	actions := app.paneWindowTitleActions(group)
	if len(actions) != 2 || actions[0].Glyph != "⇲" || actions[0].Hint != "Move to the main tabs" {
		t.Fatalf("file window actions = %+v", actions)
	}
	for _, action := range actions {
		if action.ID == paneWindowMainTabs {
			t.Fatal("a file window offers a second move to the main tabs")
		}
	}
}

// The dock header offers tab, window and hide; the old overflow and
// full-height buttons are gone.
func TestDockHeaderOffersPlacementControls(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 140, 44
	app.toggleWorkspacePanel(termdock.PanelNotices)
	app.paintShellFrame()
	plain := bufferPlain(app.mainSurf.Buffer())
	for _, want := range []string{"⧉", "⇱", "⌄"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("dock header lacks %q", want)
		}
	}
	if strings.Contains(plain, "↕") {
		t.Fatal("the full-height button is still painted")
	}
}
