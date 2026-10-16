package viewmodel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/filetree"
	"crude/tui/screens/activitybar"
	"crude/tui/screens/activitylist"
	"crude/tui/ui"
	"crude/workspace"
)

func TestActivityBarAlwaysMounted(t *testing.T) {
	app := newFullTestApp(t)
	app.Tree.Visible = false
	app.paintShellFrame()
	if !app.shellSlotActive(shellActivitySlot) {
		t.Fatal("activity rail must stay mounted when the sidebar content is hidden")
	}
	lay := app.computeLayout()
	if lay.SidebarW != activitybar.Width {
		t.Fatalf("hidden sidebar SidebarW=%d want activity rail %d", lay.SidebarW, activitybar.Width)
	}
}

func TestToggleActivityShowsListAndFocusesSidebar(t *testing.T) {
	app := newFullTestApp(t)
	model := filetree.NewFileTreeModel(t.TempDir())
	app.Tree = NewTreeState(model, 24)
	app.Tree.Visible = true
	app.sidebarScreen.SetModel(model)

	app.toggleActivity(activitySkills)
	app.paintShellFrame()
	if app.activityView != activitySkills {
		t.Fatalf("activityView=%v want skills", app.activityView)
	}
	if !app.Tree.Visible {
		t.Fatal("toggleActivity should reveal sidebar content")
	}
	if app.focusTarget() != FocusTree {
		t.Fatalf("focusTarget=%v want FocusTree", app.focusTarget())
	}
	if id := app.rootHost.HitTest(activitybar.Width+2, 5); id != activitylist.ListID {
		t.Fatalf("HitTest list=%q want %q", id, activitylist.ListID)
	}
}

func TestToggleActivitySameViewHidesContent(t *testing.T) {
	app := newFullTestApp(t)
	model := filetree.NewFileTreeModel(t.TempDir())
	app.Tree = NewTreeState(model, 24)
	app.sidebarScreen.SetModel(model)
	app.toggleActivity(activityAgents)
	if !app.Tree.Visible {
		t.Fatal("first toggle should show the agents list")
	}
	app.toggleActivity(activityAgents)
	if app.Tree.Visible {
		t.Fatal("second toggle of the same view should hide sidebar content")
	}
}

func TestActivityBarClickSelectsView(t *testing.T) {
	app := newFullTestApp(t)
	model := filetree.NewFileTreeModel(t.TempDir())
	app.Tree = NewTreeState(model, 24)
	app.sidebarScreen.SetModel(model)
	app.paintShellFrame()

	res := app.rootHost.Handle(tea.MouseClickMsg{
		X: 1, Y: app.shellTopRow() + activitybar.RowStride, Button: tea.MouseLeft,
	})
	if res.Action == nil || res.Action.ID != activitybar.ActionSelect {
		t.Fatalf("action=%v want %s", res.Action, activitybar.ActionSelect)
	}
	view, ok := res.Action.Payload.(activitybar.View)
	if !ok || view != activitybar.ViewAgents {
		t.Fatalf("payload=%v want agents", res.Action.Payload)
	}
	_ = app.handleHostAction(res.Action)
	if app.activityView != activityAgents {
		t.Fatalf("activityView=%v want agents", app.activityView)
	}
}

func TestTerminalOverlayIDsAreDynamicKeepRoute(t *testing.T) {
	id := ui.TerminalOverlayID("t1")
	if id != "terminal/t1" {
		t.Fatalf("id=%q", id)
	}
	if !ui.IsTerminalOverlay(id) {
		t.Fatal("expected terminal overlay")
	}
	got, ok := ui.TerminalIDFromOverlay(id)
	if !ok || got != "t1" {
		t.Fatalf("TerminalIDFromOverlay=%q %v", got, ok)
	}
	if ui.IsTerminalOverlay(ui.OverlayHelp) {
		t.Fatal("help is not a terminal overlay")
	}
	if ui.DefaultOverlayPolicy().Outside != ui.OutsideKeepAndRoute {
		t.Fatal("floating terminals use DefaultOverlayPolicy KeepRoute")
	}
}

func TestAgentSidebarProjectsAgentForest(t *testing.T) {
	app := newFullTestApp(t)
	root := app.ws.AgentMgr().AddOrchestrator("root", workspace.NewAgentState("root", nil, nil))
	root.Category = "default"
	app.ws.AgentMgr().AddSubAgent("child-a", "root", "Research", "model-a", "researcher", workspace.NewAgentState("child-a", nil, nil))
	app.ws.AgentMgr().AddSubAgent("child-b", "root", "Build", "model-b", "crafter", workspace.NewAgentState("child-b", nil, nil))

	items := app.agentSidebarItems()
	if len(items) < 1 || items[0].ID != sidebarItemNewAgent || !items[0].Command {
		t.Fatalf("new agent command=%#v", items)
	}
	if len(items) != 4 {
		t.Fatalf("items=%#v", items)
	}
	if items[1].Depth != 0 || items[1].Label != "Main Agent" {
		t.Fatalf("root row=%#v", items[1])
	}
	if items[2].Depth != 1 || items[2].TreeLast || items[2].Label != "Research" {
		t.Fatalf("first child row=%#v", items[2])
	}
	if items[3].Depth != 1 || !items[3].TreeLast || items[3].Label != "Build" {
		t.Fatalf("last child row=%#v", items[3])
	}
}

func TestActivityPanelsShareConfiguredSidebarWidth(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 120
	app.Tree = NewTreeState(filetree.NewFileTreeModel(t.TempDir()), 30)
	app.Tree.Visible = true
	app.sidebarScreen.SetModel(app.Tree.Model)
	app.activityView = activityFiles
	fileWidth := app.sidebarPanelWidth()
	app.activityView = activityAgents
	agentWidth := app.sidebarPanelWidth()
	if fileWidth != 30 || agentWidth != fileWidth || app.Tree.Width != 30 {
		t.Fatalf("file=%d agents=%d stored=%d", fileWidth, agentWidth, app.Tree.Width)
	}
}

// Clicking the icon of the view already on screen collapses the sidebar, and
// clicking again brings it back, for every destination, through the real
// mouse path rather than by calling toggleActivity directly.
func TestRailClickTogglesSidebarForEveryView(t *testing.T) {
	for _, view := range activitybar.Views() {
		t.Run(view.Label(), func(t *testing.T) {
			app := newFullTestApp(t)
			app.width, app.height = 120, 40
			model := filetree.NewFileTreeModel(t.TempDir())
			app.Tree = NewTreeState(model, 24)
			app.sidebarScreen.SetModel(model)
			// Start collapsed so the sequence reads the same for the
			// default view (Explorer) as for the others.
			app.Tree.Visible = false
			app.paintShellFrame()

			railY := func(v activitybar.View) int {
				for i, candidate := range activitybar.Views() {
					if candidate == v {
						return app.shellTopRow() + i*activitybar.RowStride
					}
				}
				t.Fatalf("unknown view %v", v)
				return 0
			}
			click := func(y int) {
				t.Helper()
				m, cmd := app.dispatchMouseClick(tea.MouseClickMsg{X: 2, Y: y, Button: tea.MouseLeft})
				app = m.(*App)
				if cmd != nil {
					cmd()
				}
				app.paintShellFrame()
			}

			click(railY(view))
			if !app.sidebarContentVisible() || app.activityView != view {
				t.Fatalf("first click should open %s: visible=%v view=%v",
					view.Label(), app.sidebarContentVisible(), app.activityView)
			}
			click(railY(view))
			if app.sidebarContentVisible() {
				t.Fatalf("clicking the active %s icon must collapse the sidebar", view.Label())
			}
			click(railY(view))
			if !app.sidebarContentVisible() {
				t.Fatalf("clicking %s again must reopen the sidebar", view.Label())
			}
		})
	}
}

// The rail's active marker has to follow the panel, not just the selection:
// a collapsed sidebar that still shows a lit marker looks stuck.
func TestRailMarkerFollowsSidebarVisibility(t *testing.T) {
	app := newFullTestApp(t)
	model := filetree.NewFileTreeModel(t.TempDir())
	app.Tree = NewTreeState(model, 24)
	app.Tree.Visible = true
	app.sidebarScreen.SetModel(model)

	app.refreshActivityList()
	if !app.activityBar.PanelOpen() {
		t.Fatal("an open sidebar must mark its rail entry active")
	}
	app.ToggleSidebar()
	app.refreshActivityList()
	if app.activityBar.PanelOpen() {
		t.Fatal("Ctrl+B collapse must clear the rail's active marker")
	}
}

func TestTerminalPickerDoesNotChangeSidebar(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 40
	model := filetree.NewFileTreeModel(t.TempDir())
	app.Tree = NewTreeState(model, 24)
	app.sidebarScreen.SetModel(model)
	app.setActivity(activitySkills, true)
	app.paintShellFrame()

	app.openTerminalPicker()
	if app.activityView != activitySkills || !app.Tree.Visible {
		t.Fatal("terminal picker replaced the selected sidebar")
	}
	if app.overlayHost.Window(ui.OverlayContextMenu) == nil {
		t.Fatal("terminal picker did not open")
	}
}

func TestNewAgentRowActivatesOnSingleClick(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 40
	model := filetree.NewFileTreeModel(t.TempDir())
	app.Tree = NewTreeState(model, 24)
	app.sidebarScreen.SetModel(model)
	app.setActivity(activityAgents, true)
	app.paintShellFrame()

	rect, ok := app.rootHost.ScreenRect(activitylist.ScreenID)
	if !ok {
		t.Fatal("agents panel is not mounted")
	}
	row := -1
	for y := 0; y < rect.H; y++ {
		if item, hit := app.activityList.ItemAtLocalY(y); hit && item.ID == sidebarItemNewAgent {
			row = rect.Y + y
			break
		}
	}
	if row < 0 {
		t.Fatal("New Agent row not found in the Agents panel")
	}

	res := app.rootHost.Handle(tea.MouseClickMsg{X: rect.X + 2, Y: row, Button: tea.MouseLeft})
	if res.Action == nil || res.Action.ID != activitylist.ActionActivate {
		t.Fatalf("single click must fire the command immediately: %#v", res.Action)
	}
	item, ok := res.Action.Payload.(ui.ListItem)
	if !ok || item.ID != sidebarItemNewAgent || !item.Command {
		t.Fatalf("payload=%#v", res.Action.Payload)
	}
}

func TestEveryActivityDestinationHasContextMenu(t *testing.T) {
	app := newFullTestApp(t)
	for _, view := range activitybar.Views() {
		menu := app.buildActivityRailContextMenu(view)
		if menu == nil || len(menu.Sections) < 2 {
			t.Fatalf("view=%v menu=%#v", view, menu)
		}
		if !strings.Contains(menu.Title, view.Label()) {
			t.Fatalf("view=%v title=%q", view, menu.Title)
		}
		menu.Build()
		if len(menu.FlatItems) == 0 {
			t.Fatalf("view=%v has no usable context actions", view)
		}
	}
}

func TestActivityBarGearOpensApplicationSettings(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 40
	model := filetree.NewFileTreeModel(t.TempDir())
	app.Tree = NewTreeState(model, 24)
	app.sidebarScreen.SetModel(model)
	app.paintShellFrame()

	rect, ok := app.rootHost.ScreenRect(activitybar.ScreenID)
	if !ok || rect.H < activitybar.RowStride*5 {
		t.Fatalf("activity rail too short for a settings gear: %+v", rect)
	}
	y := rect.Y + rect.H - 1
	m, cmd := app.dispatchMouseClick(tea.MouseClickMsg{X: 2, Y: y, Button: tea.MouseLeft})
	app = m.(*App)
	if cmd != nil {
		cmd()
	}
	win := app.windowManager.Window("workspace-settings")
	if win == nil {
		t.Fatal("gear click must open settings")
	}
	if win.Title != "Application Settings" {
		t.Fatalf("title=%q want Application Settings", win.Title)
	}
}

func TestActivitySettingsContextMenu(t *testing.T) {
	app := newFullTestApp(t)
	menu := app.buildActivitySettingsContextMenu()
	if menu == nil || !strings.Contains(menu.Title, "Settings") {
		t.Fatalf("menu=%#v", menu)
	}
	menu.Build()
	if len(menu.FlatItems) != 2 {
		t.Fatalf("items=%#v", menu.FlatItems)
	}
}
