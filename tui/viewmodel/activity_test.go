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

	app.toggleActivity(activityTerminals)
	app.paintShellFrame()
	if app.activityView != activityTerminals {
		t.Fatalf("activityView=%v want terminals", app.activityView)
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
	app.toggleActivity(activityGit)
	if !app.Tree.Visible {
		t.Fatal("first toggle should show git list")
	}
	app.toggleActivity(activityGit)
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
		X: 1, Y: sidebarTopRow + activitybar.RowStride, Button: tea.MouseLeft,
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
	root := app.AgentMgr.AddOrchestrator("root", nil, nil, nil, nil, workspace.NewAgentState("root", nil, nil))
	root.Category = "default"
	app.AgentMgr.AddSubAgent("child-a", "root", "Research", "model-a", "researcher", workspace.NewAgentState("child-a", nil, nil))
	app.AgentMgr.AddSubAgent("child-b", "root", "Build", "model-b", "crafter", workspace.NewAgentState("child-b", nil, nil))

	items := app.agentSidebarItems()
	if len(items) != 3 {
		t.Fatalf("items=%#v", items)
	}
	if items[0].Depth != 0 || items[0].Label != "Main Agent" {
		t.Fatalf("root row=%#v", items[0])
	}
	if items[1].Depth != 1 || items[1].TreeLast || items[1].Label != "Research" {
		t.Fatalf("first child row=%#v", items[1])
	}
	if items[2].Depth != 1 || !items[2].TreeLast || items[2].Label != "Build" {
		t.Fatalf("last child row=%#v", items[2])
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
