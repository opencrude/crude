package viewmodel

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/screens/activitybar"
	workspacescreen "crude/tui/screens/workspaces"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/workspace"
)

func TestWorkspaceRailAvailableDuringNestedWorkflow(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	app.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	app.launcherScreen = nil // a live session may have removed workspace home
	app.Win.OpenResource(window.Resource{Kind: window.ResourceAgent, ID: "root"}, "agent:root", true)
	app.Win.SplitGroup(app.Win.ActiveGroup(), ui.Horizontal, false)
	app.Win.OpenResource(window.Resource{Kind: window.ResourceAgent, ID: "child"}, "agent:child", true)
	app.Win.SplitGroup(app.Win.ActiveGroup(), ui.Vertical, false)
	app.workspaceInfos = []workspace.WorkspaceInfo{{ProjectDir: "C:/alpha", ProjectName: "alpha"}, {ProjectDir: "D:/beta", ProjectName: "beta"}}
	app.paintShellFrame()
	r, _ := app.rootHost.ScreenRect(activitybar.ScreenID)
	app.Update(tea.MouseClickMsg{X: r.X + 3, Y: r.Y + 2*activitybar.RowStride + 1, Button: tea.MouseLeft})
	app.Update(launcherWorkspacesMsg{Gen: app.launcherLoadGen.Load(), Infos: app.workspaceInfos})
	app.paintShellFrame()
	if app.activityView != activityWorkspaces || app.focusTarget() != FocusTree {
		t.Fatal("rail did not reach workspace navigation during nested flow")
	}
	paint := bufferPlain(app.mainSurf.Buffer())
	for _, label := range []string{"New Workspace…", "Search workspaces", "alpha", "beta"} {
		if !strings.Contains(paint, label) {
			t.Fatalf("missing %q in nested workflow:\n%s", label, paint)
		}
	}
	if strings.Contains(paint, "↻ Refresh") {
		t.Fatal("workspace rail still paints the refresh command row")
	}
	if app.launcherScreen != nil {
		t.Fatal("rail resurrected launcher instead of independent navigation")
	}
	if len(app.Win.GroupIDs()) != 3 {
		t.Fatal("opening rail changed active pane topology")
	}
}

func TestWorkspaceListingHasOneOwnerAndSurvivesLauncherUnmount(t *testing.T) {
	app := newFullTestApp(t)
	app.launcherLoadGen.Store(10)
	app.setActivity(activityWorkspaces, true)
	app.paintShellFrame()
	screen := app.workspacesScreen
	app.launcherScreen = nil
	app.Update(launcherWorkspacesMsg{Gen: 10, Infos: []workspace.WorkspaceInfo{{ProjectDir: "D:/beta", ProjectName: "beta"}}})
	app.paintShellFrame()
	if !screen.HasPath("D:/beta") {
		t.Fatal("launcher unmount discarded workspace catalog")
	}
	app.Update(launcherWorkspacesMsg{Gen: 9, Infos: []workspace.WorkspaceInfo{{ProjectDir: "D:/stale"}}})
	if screen.HasPath("D:/stale") {
		t.Fatal("stale catalog replaced current listing")
	}
	app.Update(launcherWorkspacesMsg{Gen: 10, Err: errors.New("offline")})
	app.paintShellFrame()
	if !screen.HasPath("D:/beta") || !strings.Contains(bufferPlain(app.mainSurf.Buffer()), "Couldn’t refresh") {
		t.Fatal("failed refresh lost last usable catalog/error")
	}
	old := screen
	app.resetWorkspaceNavigation()
	if old == app.workspacesScreen {
		t.Fatal("workspace transition retained intent identity")
	}
	cmd, handled := app.handleWorkspaceSwitcherAction(ui.Action{ID: workspacescreen.ActionNew, Payload: workspacescreen.Request{Screen: old}})
	if !handled || cmd != nil || app.FilePicker != nil {
		t.Fatal("stale switcher command changed replacement workspace")
	}
}

func TestWorkspaceNavigationPreservesIndependentSearchAndDivider(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 35})
	app.workspaceInfos = []workspace.WorkspaceInfo{{ProjectDir: "C:/alpha", ProjectName: "alpha"}, {ProjectDir: "D:/beta", ProjectName: "beta"}}
	app.setActivity(activityWorkspaces, true)
	app.FocusStack.Push(FocusTree)
	app.paintShellFrame()
	app.rootHost.Focus(workspacescreen.SearchID)
	app.Update(tea.PasteMsg{Content: "beta"})
	app.paintShellFrame()
	if app.workspacesScreen.Query() != "beta" {
		t.Fatal("search paste escaped workspace owner")
	}
	app.setActivity(activityAgents, true)
	app.paintShellFrame()
	app.setActivity(activityWorkspaces, true)
	app.paintShellFrame()
	if app.workspacesScreen.Query() != "beta" {
		t.Fatal("sidebar switch lost workspace search")
	}
	r, _ := app.rootHost.ScreenRect(workspacescreen.DividerID)
	app.Update(tea.MouseClickMsg{X: r.X, Y: r.Y + 5, Button: tea.MouseLeft})
	if !app.sidebarResizeActive() {
		t.Fatal("workspace divider is not shared capture/resize path")
	}
	app.Update(tea.MouseMotionMsg{X: r.X + 5, Y: r.Y + 5, Button: tea.MouseLeft})
	app.Update(tea.MouseReleaseMsg{X: r.X + 5, Y: r.Y + 5, Button: tea.MouseLeft})
	if app.rootHost.HasPointerCapture() {
		t.Fatal("workspace divider retained capture")
	}
}
