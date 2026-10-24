package viewmodel

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"crude/tui/screens/activitybar"
	"crude/tui/screens/activitylist"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/workspace"

	tea "charm.land/bubbletea/v2"
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
	app.launcherScreen = nil
	app.Update(launcherWorkspacesMsg{Gen: 10, Infos: []workspace.WorkspaceInfo{{ProjectDir: "D:/beta", ProjectName: "beta"}}})
	app.paintShellFrame()
	if !strings.Contains(bufferPlain(app.mainSurf.Buffer()), "beta") {
		t.Fatal("launcher unmount discarded workspace catalog")
	}
	app.Update(launcherWorkspacesMsg{Gen: 9, Infos: []workspace.WorkspaceInfo{{ProjectDir: "D:/stale"}}})
	app.paintShellFrame()
	if strings.Contains(bufferPlain(app.mainSurf.Buffer()), "stale") {
		t.Fatal("stale catalog replaced current listing")
	}
	app.Update(launcherWorkspacesMsg{Gen: 10, Err: errors.New("offline")})
	app.paintShellFrame()
	if !strings.Contains(bufferPlain(app.mainSurf.Buffer()), "beta") || !strings.Contains(bufferPlain(app.mainSurf.Buffer()), "Couldn’t refresh") {
		t.Fatal("failed refresh lost last usable catalog/error")
	}
	app.resetWorkspaceNavigation()
	app.setActivity(activityAgents, true)
	if path, ok := app.workspaceSidebarPath(sidebarItemWorkspacePrefix + "D:/beta"); ok || path != "" {
		t.Fatal("unmounted workspace list admitted a stale resource action")
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
	app.rootHost.Focus(activitylist.FilterID)
	app.Update(tea.PasteMsg{Content: "beta"})
	app.paintShellFrame()
	if app.activityList.Query() != "beta" {
		t.Fatal("search paste escaped workspace owner")
	}
	app.setActivity(activityAgents, true)
	app.paintShellFrame()
	app.setActivity(activityWorkspaces, true)
	app.paintShellFrame()
	if app.activityList.Query() != "beta" {
		t.Fatal("sidebar switch lost workspace search")
	}
	r, _ := app.rootHost.ScreenRect(activitylist.DividerID)
	before := app.Tree.Width
	app.Update(tea.MouseClickMsg{X: r.X, Y: r.Y + 5, Button: tea.MouseLeft})
	if !app.sidebarResizeActive() {
		t.Fatal("workspace divider is not shared capture/resize path")
	}
	app.paintShellFrame() // Native input presents frames between press and motion.
	if !app.sidebarResizeActive() {
		t.Fatal("presenting the pressed divider cancelled its drag")
	}
	app.Update(tea.MouseMotionMsg{X: r.X + 5, Y: r.Y + 5, Button: tea.MouseLeft})
	app.paintShellFrame()
	if app.Tree.Width != before+5 {
		t.Fatalf("drag captured but did not resize sidebar: %d -> %d", before, app.Tree.Width)
	}
	resized, _ := app.rootHost.ScreenRect(activitylist.DividerID)
	if resized.X != r.X+5 {
		t.Fatalf("rendered divider did not move with width: %+v -> %+v", r, resized)
	}
	app.Update(tea.MouseReleaseMsg{X: r.X + 5, Y: r.Y + 5, Button: tea.MouseLeft})
	if app.rootHost.HasPointerCapture() {
		t.Fatal("workspace divider retained capture")
	}
	app.setActivity(activityAgents, true)
	app.paintShellFrame()
	app.setActivity(activityWorkspaces, true)
	app.paintShellFrame()
	if app.Tree.Width != before+5 {
		t.Fatal("return to Workspaces lost resized width")
	}
}

func TestWorkspaceSidebarHasNamePathPairsWithoutGapsAndAuthoritativeCurrentFill(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	wsOf(app).RootPath = t.TempDir()
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 35})
	root, other := app.ws.RootPath(), filepath.Join(t.TempDir(), "ox")
	app.workspaceInfos = []workspace.WorkspaceInfo{{ProjectDir: root, ProjectName: "crude"}, {ProjectDir: other, ProjectName: "ox"}}
	app.setActivity(activityWorkspaces, true)
	app.setFocusTree()
	app.paintShellFrame()
	list, _ := app.rootHost.ScreenRect(activitylist.ListID)
	pane, _ := app.rootHost.ScreenRect(activitylist.ScreenID)
	for row, label := range []string{"New Workspace…", "crude", "crude", "ox", "ox"} {
		item, ok := app.activityList.ItemAtLocalY(list.Y - pane.Y + row)
		if !ok || item.Label != label {
			t.Fatalf("row %d has a spacer/duplicate instead of %q: %+v", row, label, item)
		}
	}
	for row, path := range map[int]string{2: root, 4: other} {
		item, _ := app.activityList.ItemAtLocalY(list.Y - pane.Y + row)
		if item.Subtitle != path {
			t.Fatal("workspace row discarded its path")
		}
		if ink := app.mainSurf.Buffer().Get(list.X+1, list.Y+row).FG; ink != ui.NewTokenSet(app.Theme).Color(ui.ColorMuted) {
			t.Fatal("rendered path did not use theme-adaptable muted text")
		}
	}
	if item, _ := app.activityList.SelectedItem(); item.ID != sidebarItemNewWorkspace {
		t.Fatal("command cursor did not match other sidebars")
	}
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnd}) // Browse ox without opening it.
	app.paintShellFrame()
	app.unfocus()
	app.paintShellFrame()
	buf := app.mainSurf.Buffer()
	tokens := ui.NewTokenSet(app.Theme)
	for _, row := range []int{1, 2} {
		if got := buf.Get(list.X+2, list.Y+row).BG; got != tokens.Color(ui.ColorSelection) {
			t.Fatal("current workspace name/path lost its authoritative active fill")
		}
	}
	if buf.Get(list.X+2, list.Y).BG != tokens.Color(ui.ColorSubPaneBg) {
		t.Fatal("New Workspace control background disappeared after focus left")
	}
	for _, y := range []int{list.Y + 3, list.Y + 4} {
		if got := buf.Get(list.X+2, y).BG; got != tokens.Color(ui.ColorBg) {
			t.Fatal("inactive workspace retained a false active fill")
		}
	}
	app.applyWorkspaceListing(launcherWorkspacesMsg{Infos: []workspace.WorkspaceInfo{app.workspaceInfos[1], app.workspaceInfos[0]}})
	app.paintShellFrame()
	if !app.workspaceSidebarItems()[2].Active || app.workspaceSidebarItems()[1].Active || app.ws.RootPath() != root {
		t.Fatal("recency reorder changed current workspace authority")
	}
}

func TestWorkspaceSidebarContextMenuSharesResourceDeleteAndFencesLifetime(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	wsOf(app).RootPath = t.TempDir()
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 35})
	root, other := app.ws.RootPath(), filepath.Join(t.TempDir(), "other")
	app.workspaceInfos = []workspace.WorkspaceInfo{{ID: workspace.WorkspaceID(root), ProjectDir: root, ProjectName: "current"}, {ID: workspace.WorkspaceID(other), ProjectDir: other, ProjectName: "other"}}
	app.setActivity(activityWorkspaces, true)
	app.paintShellFrame()
	r, _ := app.rootHost.ScreenRect(activitylist.ListID)
	menu := app.buildWorkspaceSidebarContextMenu(tea.Mouse{X: r.X + 2, Y: r.Y + 4}) // path row of the other workspace
	var remove ui.ContextMenuItem
	for _, section := range menu.Sections {
		for _, item := range section.Items {
			if item.Label == "Delete workspace…" {
				remove = item
			}
		}
	}
	if remove.Action == nil || remove.Disabled {
		t.Fatal("sidebar resource has no usable shared Delete action")
	}
	if _, hit := app.activityItemAtMouse(tea.Mouse{X: r.X + r.W, Y: r.Y + 4}); hit {
		t.Fatal("scrollbar gutter resolved as the adjacent workspace")
	}
	current := app.buildWorkspaceSidebarContextMenu(tea.Mouse{X: r.X + 2, Y: r.Y + 1})
	for _, section := range current.Sections {
		for _, item := range section.Items {
			if (item.Label == "Delete workspace…" || item.Label == "Open") && !item.Disabled {
				t.Fatal("current workspace offered an unsafe resource action")
			}
		}
	}
	app.workspaceSwitchGen.Add(1)
	remove.Action()
	if app.overlayHost.IsOpen(ui.OverlayWorkspaceDelete) {
		t.Fatal("stale menu opened deletion in a different workspace lifetime")
	}
}

func TestWorkspaceSidebarRefreshAnimatesWithoutLoadingTextOrLauncher(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 35})
	app.launcherScreen = nil
	app.workspaceInfos = []workspace.WorkspaceInfo{{ProjectDir: app.ws.RootPath(), ProjectName: "ox"}, {ProjectDir: "D:/crude", ProjectName: "crude"}}
	app.setActivity(activityWorkspaces, true)
	app.setFocusTree()
	app.paintShellFrame()
	list, _ := app.rootHost.ScreenRect(activitylist.ListID)
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	app.unfocus()
	app.refreshLauncherAsync(app.ws.RootPath())
	gen := app.launcherLoadGen.Load()
	app.SpinFrame = 100
	app.paintShellFrame()
	progress, ok := app.rootHost.ScreenRect(activitylist.ProgressID)
	if !ok || !app.wantsFastTick() || app.launcherScreen != nil {
		t.Fatal("visible sidebar refresh needs the standard animation without resurrecting Home")
	}
	first := app.mainSurf.Buffer().Clone()
	for _, text := range []string{"Loading", "Refreshing", "No recent workspaces"} {
		if strings.Contains(bufferPlain(first), text) {
			t.Fatalf("refresh injected %q instead of the standard progress animation", text)
		}
	}
	app.SpinFrame = 1500
	app.paintShellFrame()
	changed := false
	for x := progress.X; x < progress.X+progress.W; x++ {
		changed = changed || first.Get(x, progress.Y).FG != app.mainSurf.Buffer().Get(x, progress.Y).FG
	}
	if !changed {
		t.Fatal("sidebar progress did not advance on the shell clock")
	}
	if got, _ := app.rootHost.ScreenRect(activitylist.ListID); got != list {
		t.Fatal("refresh moved the list hit targets")
	}
	if got := app.mainSurf.Buffer().Get(list.X+2, list.Y).BG; got != ui.NewTokenSet(app.Theme).Color(ui.ColorSubPaneBg) {
		t.Fatal("New Workspace control background disappeared during unfocused refresh")
	}
	app.Update(launcherWorkspacesMsg{Gen: gen - 1, Infos: nil})
	if !app.wantsFastTick() {
		t.Fatal("stale completion stopped current refresh animation")
	}
	app.Tree.Visible = false
	if app.wantsFastTick() {
		t.Fatal("hidden sidebar kept requesting animation frames")
	}
	app.Tree.Visible = true
	app.setActivity(activityAgents, true)
	if app.wantsFastTick() {
		t.Fatal("workspace progress leaked into another sidebar")
	}
	app.setActivity(activityWorkspaces, true)
	if !app.wantsFastTick() {
		t.Fatal("returning to pending refresh lost its animation")
	}
	app.Update(launcherWorkspacesMsg{Gen: gen, Infos: app.workspaceInfos})
	app.paintShellFrame()
	if app.wantsFastTick() || app.workspaceListLoading {
		t.Fatal("settled catalog kept animating")
	}
	if !strings.Contains(bufferPlain(app.mainSurf.Buffer()), "D:/crude") {
		t.Fatal("refresh lost the retained workspace path")
	}
}
