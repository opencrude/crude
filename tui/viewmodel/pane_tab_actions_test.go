package viewmodel

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/internal/testterminal"
	"crude/tui/screens/activitylist"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/workspace"
)

func TestEmptySplitCloseButton(t *testing.T) {
	for _, route := range []string{"mouse", "keyboard", "shortcut"} {
		t.Run(route, func(t *testing.T) {
			app, ws := newRealShellApp(t)
			defer ws.Close()
			paneJourneySize(app, 80, 24)
			home := app.Win.ActiveGroup()
			paneJourneyKey(app, 'l', tea.ModCtrl|tea.ModAlt)
			empty := app.Win.ActiveGroup()
			if empty == home {
				t.Fatal("split shortcut did not select an empty pane")
			}
			body := app.mainPanes.groups[empty].body
			painted := strings.Split(paneJourneyText(app, body), "\n")
			px, py := -1, -1
			for row, text := range painted {
				if col := strings.Index(text, "[ Close Pane ]"); col >= 0 {
					px, py = body.X+col+2, body.Y+row
					break
				}
			}
			if px < 0 {
				t.Fatalf("empty split did not paint Close Pane:\n%s", strings.Join(painted, "\n"))
			}
			for _, action := range []string{"New Session", "Open File"} {
				if !strings.Contains(strings.Join(painted, "\n"), action) {
					t.Fatalf("empty split lost %q", action)
				}
			}
			switch route {
			case "mouse":
				// Close the empty pane under the pointer, not the focused sibling.
				paneJourneyKey(app, '[', tea.ModAlt)
				_, _ = app.Update(tea.MouseClickMsg{X: px, Y: py, Button: tea.MouseLeft})
				_, _ = app.Update(tea.MouseReleaseMsg{X: px, Y: py, Button: tea.MouseLeft})
				_ = app.View()
			case "keyboard":
				// Reverse traversal wraps from New Session to Close Pane.
				paneJourneyKey(app, tea.KeyTab, tea.ModShift)
				paneJourneyKey(app, tea.KeyEnter, 0)
			case "shortcut":
				paneJourneyKey(app, 'w', tea.ModCtrl)
			}
			if app.Quit || !slices.Equal(app.Win.GroupIDs(), []window.GroupID{home}) ||
				app.Win.ActiveGroup() != home || !app.launcherHostVisible() {
				t.Fatal("closing an empty pane did not return to the remaining workspace home")
			}
			if strings.Contains(paneJourneyText(app, app.mainPanes.groups[home].body), "[ Close Pane ]") {
				t.Fatal("unsplit workspace home offers a Close Pane button")
			}
			paneJourneyKey(app, 'w', tea.ModCtrl)
			if app.Quit || app.Win.ActiveGroup() != home || !app.launcherHostVisible() {
				t.Fatal("Close on unsplit workspace home quit the app or changed the layout")
			}
		})
	}
}

func TestPaneTabsMenuMixedBatchCancelAndResume(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 180, 50)
	leftFile := openEditorTabs(t, app, "left.go")[0]
	app.AddOrchestrator("clicked")
	clicked := app.Win.ActiveTab().ID
	dirtyPath := openEditorTabs(t, app, "dirty.go")[0]
	dirty := app.EditorState.Tabs[dirtyPath].Editor
	dirty.InsertRune('x')
	app.AddOrchestrator("later")
	later := app.Win.ActiveTab().ID
	node := ws.AgentMgr.Get("later")
	node.State.Running, node.State.Streaming = true, true
	tailPath := openEditorTabs(t, app, "tail.go")[0]
	tail := app.Win.ActiveTab().ID
	group := app.Win.ActiveGroup()
	sibling := app.Win.SplitGroup(group, ui.Horizontal, false)
	app.focusMainGroup(sibling)
	neighborPath := openEditorTabs(t, app, "neighbor.go")[0]
	neighbor := app.Win.ActiveTab().ID

	paneOpenTabMenu(t, app, clicked)
	if app.Win.ActiveGroup() != sibling {
		t.Fatal("opening a tab menu focused the clicked pane")
	}
	if selected := app.Win.GroupActiveTab(group); selected == nil || selected.ID != tail {
		t.Fatal("opening a tab menu selected the clicked tab")
	}
	checkTabsSection(t, app, "Close other tabs", "Close tabs to the left", "Close tabs to the right", "Close all tabs")
	paneChooseMenu(t, app, "Close tabs to the right", true)
	if app.DirtyPromptPath != dirtyPath || !dirty.Modified() || app.Win.GroupOf(later) != group || app.Win.GroupOf(tail) != group {
		t.Fatal("batch did not stop at the dirty tab before closing later tabs")
	}
	paneJourneyKey(app, tea.KeyEsc, 0)
	if app.DirtyPromptPath != "" || !dirty.Modified() || app.Win.GroupOf(later) != group || app.Win.GroupOf(tail) != group {
		t.Fatal("Cancel did not preserve the dirty tab and the rest of the batch")
	}
	paneOpenTabMenu(t, app, clicked)
	paneChooseMenu(t, app, "Close tabs to the right", false)
	paneJourneyKey(app, 'd', 0)
	if app.EditorState.Tabs[dirtyPath] != nil || app.EditorState.Tabs[tailPath] != nil ||
		app.Win.IndexOf(later) >= 0 || !app.AgentView.IsHidden("later") {
		t.Fatal("Discard did not close the dirty tab and resume the mixed batch")
	}
	if ws.AgentMgr.Get("later") != node || !node.State.Running || !node.State.Streaming || node.State.Cancelling {
		t.Fatal("closing the agent tab altered its run")
	}
	if !slices.EqualFunc(app.Win.GroupTabs(group), []string{app.editorTabTitle(leftFile), app.agentTabTitle("clicked")},
		func(tab window.Tab, title string) bool { return tab.Title == title }) ||
		app.Win.GroupOf(neighbor) != sibling || app.EditorState.Tabs[neighborPath] == nil {
		t.Fatal("right-only close changed the anchor, its left neighbor, or the other pane")
	}
	if selected := app.Win.GroupActiveTab(group); selected == nil || selected.ID != clicked {
		t.Fatal("remaining selection did not return to the last used tab")
	}
	if active := app.Win.ActiveTab(); active == nil || active.ID != neighbor {
		t.Fatal("closing tabs in the other pane moved focus")
	}
	assertMainTabContentFocus(t, app, FocusEditor)
	paneJourneyKey(app, '[', tea.ModAlt)
	assertMainTabContentFocus(t, app, FocusInput)
}

func TestPaneTabsMenuKeepsAgentDescendantInAnotherPane(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 40)
	app.AddOrchestrator("parent")
	parentTab := app.Win.ActiveTab().ID
	ws.AgentMgr.AddSubAgent("child", "parent", "Child", "model", "researcher", workspace.NewAgentState("child", nil, nil))
	app.restoreAgent("child")
	childTab := app.Win.ActiveTab().ID
	group := app.Win.ActiveGroup()
	sibling := app.Win.SplitGroup(group, ui.Horizontal, false)
	app.Win.MoveTabToGroup(childTab, sibling, 0)
	app.focusMainGroup(sibling)
	paneOpenTabMenu(t, app, parentTab)
	paneChooseMenu(t, app, "Close all tabs", false)
	app.syncAgentWindowTabs()
	_ = app.View()
	if app.Win.IndexOf(parentTab) >= 0 || !app.AgentView.IsHidden("parent") {
		t.Fatal("bulk close did not minimize the parent tab")
	}
	if app.Win.GroupOf(childTab) != sibling || app.AgentView.IsHidden("child") || app.AgentView.ActiveID() != "child" {
		t.Fatal("bulk close affected the descendant in the other pane")
	}
	app.restoreAgent("child")
	if app.Win.TabByTitle(app.agentTabTitle("parent")) != nil {
		t.Fatal("reopening a child reopened its independently minimized parent's tab")
	}
	memory := app.captureViewMemory()
	restored := NewAgentViewState(ws.AgentMgr)
	restored.Remember(memory.MinimizedAgents, memory.OpenedAgents, memory.MinimizedAgentTabs)
	if !restored.IsHidden("parent") || restored.IsHidden("child") || restored.VisibleCount() != 1 {
		t.Fatal("client view memory did not retain the independent tab choices")
	}
}

func TestPaneTabsMenuResumedBatchSkipsTabMovedToSibling(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 180, 50)
	app.AddOrchestrator("anchor")
	anchor := app.Win.ActiveTab().ID
	dirtyPath := openEditorTabs(t, app, "dirty.go")[0]
	app.EditorState.Tabs[dirtyPath].Editor.InsertRune('x')
	app.AddOrchestrator("moved")
	moved := app.Win.ActiveTab().ID
	app.AddOrchestrator("stays")
	stays := app.Win.ActiveTab().ID
	group := app.Win.ActiveGroup()
	neighbor := app.Win.SplitGroup(group, ui.Horizontal, false)
	app.focusMainGroup(group)
	paneOpenTabMenu(t, app, anchor)
	paneChooseMenu(t, app, "Close tabs to the right", false)
	if app.DirtyPromptPath != dirtyPath {
		t.Fatal("batch did not open the dirty decision")
	}
	// Model a placement change while the dirty decision is open.
	app.Win.MoveTabToGroup(moved, neighbor, 0)
	paneJourneyKey(app, 'd', 0)
	if app.Win.GroupOf(moved) != neighbor || app.AgentView.IsHidden("moved") ||
		app.Win.IndexOf(stays) >= 0 || !app.AgentView.IsHidden("stays") {
		t.Fatal("resume did not limit the remaining targets to the original pane")
	}
}

func TestPaneTabsMenuClosesComparisonWithoutFollowingItsSource(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 180, 50)
	paths := openEditorTabs(t, app, "left.go", "source.go")
	group := app.Win.ActiveGroup()
	left := app.Win.TabByTitle(app.editorTabTitle(paths[0])).ID
	source := app.Win.ActiveTab().ID
	review := app.openGitReviewTab(gitReviewResource("test", ""), "Review")
	app.activateGitReviewTab(review)
	neighbor := app.Win.SplitGroup(group, ui.Horizontal, false)
	app.Win.MoveTabToGroup(source, neighbor, 0)
	app.focusMainGroup(group)
	paneOpenTabMenu(t, app, left)
	paneChooseMenu(t, app, "Close tabs to the right", false)
	if app.Win.IndexOf(review.tab) >= 0 || app.gitReviews[review.tab] != nil ||
		app.Win.GroupOf(source) != neighbor || app.Win.ActiveGroup() != group ||
		app.Win.ActiveTab() == nil || app.Win.ActiveTab().ID != left {
		t.Fatal("closing a review changed its source pane or did not release the review")
	}
	assertMainTabContentFocus(t, app, FocusEditor)
}

func TestPaneTabsMenuReturnsTerminalToDockWithoutChangingOpenPanel(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 45)
	app.AddOrchestrator("anchor")
	anchor := app.Win.ActiveTab().ID
	command, args := testterminal.Command(t, "idle")
	info, err := createAppTerminal(t, app, workspace.TerminalCreateOpts{Command: command, Args: args, CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	app.openTerminalAsTab(info.ID)
	terminal := app.Win.ActiveTab().ID
	if !app.termDock.ShowPanel(termdock.PanelNotices) {
		t.Fatal("could not open the Notices dock")
	}
	paneOpenTabMenu(t, app, anchor)
	paneChooseMenu(t, app, "Close other tabs", false)
	if app.Win.IndexOf(terminal) >= 0 || !app.termDock.Contains(info.ID) ||
		!app.termDock.IsOpen() || app.termDock.ActivePanel() != termdock.PanelNotices {
		t.Fatal("close did not return the terminal to the dock or changed Notices")
	}
	if after, ok := app.terminalInfo(info.ID); !ok || !after.Alive || after.PID != info.PID {
		t.Fatal("closing the terminal tab changed its running PTY")
	}
}

func TestAgentsListOffersTabActionsForItsOwnPane(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 40)
	app.AddOrchestrator("first")
	first := app.Win.ActiveTab().ID
	app.AddOrchestrator("other")
	other := app.Win.ActiveTab().ID
	node := ws.AgentMgr.Get("other")
	node.State.Running = true
	group := app.Win.ActiveGroup()
	sibling := app.Win.SplitGroup(group, ui.Horizontal, false)
	app.focusMainGroup(sibling)
	neighbor := openEditorTabs(t, app, "neighbor.go")[0]
	paneJourneyKey(app, 'a', tea.ModCtrl|tea.ModShift)
	r, ok := app.rootHost.ScreenRect(activitylist.ListID)
	if !ok || r.Empty() {
		t.Fatal("Agents list is not visible")
	}
	for y := r.Y; y < r.Y+r.H; y++ {
		point := tea.Mouse{X: r.X + 1, Y: y}
		if item, ok := app.activityItemAtMouse(point); ok && item.ID == "agent:first" {
			point.Button = tea.MouseRight
			app.Update(tea.MouseClickMsg(point))
			app.View()
			break
		}
	}
	if app.LastContextMenu == nil {
		t.Fatal("the agent row did not open a menu")
	}
	checkTabsSection(t, app, "Close other tabs", "Close tabs to the left", "Close tabs to the right", "Close all tabs")
	paneChooseMenu(t, app, "Close other tabs", false)
	if app.Win.GroupOf(first) != group || app.Win.TabByID(other) != nil || ws.AgentMgr.Get("other") != node || !node.State.Running || app.EditorState.ActiveView != neighbor || app.Win.ActiveGroup() != sibling {
		t.Fatal("agent menu closed or focused the wrong tab, or stopped the agent")
	}
}

func checkTabsSection(t *testing.T, app *App, labels ...string) {
	t.Helper()
	for _, section := range app.LastContextMenu.Sections {
		if section.SectionLabel == "Tabs" {
			for _, item := range section.Items {
				if strings.Contains(strings.ToLower(item.Label), "file") {
					t.Fatalf("file-only action %q is in the shared Tabs section", item.Label)
				}
			}
			for _, label := range labels {
				if !slices.ContainsFunc(section.Items, func(item ui.ContextMenuItem) bool { return item.Label == label }) {
					t.Errorf("Tabs section is missing %q", label)
				}
			}
			return
		}
	}
	t.Fatal("tab menu has no Tabs section")
}
