package viewmodel

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/termdock"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/tui/window"
	ttypes "crude/types"
)

func paneTabPoint(t *testing.T, app *App, tab window.TabID) (int, int) {
	t.Helper()
	g := app.mainPanes.groups[app.Win.GroupOf(tab)]
	r, ok := app.screenRectOf(g.tabs.TabsControl().ID())
	if !ok {
		t.Fatal("tab not mounted")
	}
	index := slices.IndexFunc(app.Win.GroupTabs(g.id), func(candidate window.Tab) bool { return candidate.ID == tab })
	for x := 0; x < r.W; x++ {
		if hit, closeHit := g.tabs.TabsControl().HitTest(x); hit == index && !closeHit {
			return r.X + x, r.Y
		}
	}
	t.Fatal("tab has no painted hit target")
	return 0, 0
}

func paneOpenTabMenu(t *testing.T, app *App, tab window.TabID) {
	t.Helper()
	_ = app.View()
	x, y := paneTabPoint(t, app, tab)
	_, _ = app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseRight})
	_ = app.View()
	if app.LastContextMenu == nil {
		t.Fatal("right-click did not open tab menu")
	}
}

func paneMenuLabel(st *ui.ContextMenuState) string {
	if st == nil {
		return ""
	}
	si, ii, ok := st.FlatItemAt(st.Cursor)
	if !ok {
		return ""
	}
	return st.Sections[si].Items[ii].Label
}

// Navigate the actual popup with keys; do not call the callbacks or mutate its
// cursor. Pointer activation uses the list's displayed/scroll-offset row.
func paneChooseMenu(t *testing.T, app *App, label string, pointer bool) {
	t.Helper()
	st := app.LastContextMenu
	if st == nil {
		t.Fatalf("missing menu for %q", label)
	}
	for i := 0; i < len(st.FlatItems) && paneMenuLabel(st) != label; i++ {
		paneJourneyKey(app, tea.KeyDown, 0)
	}
	if paneMenuLabel(st) != label {
		t.Fatalf("missing action %q: %s", label, st)
	}
	if !pointer {
		paneJourneyKey(app, tea.KeyEnter, 0)
		return
	}
	fw := app.windowManager.Window("context-menu")
	cw := fw.Content.(*ui.ComponentWidget)
	list := ui.ContextMenuList(cw)
	r, ok := cw.Host().ScreenRect(ui.ComponentID(ui.ContextMenuListID))
	if !ok {
		t.Fatal("menu list not mounted")
	}
	x, y := fw.Position()
	row := list.SelectedIndex() - list.Scroll().Offset()
	if row < 0 || row >= r.H {
		t.Fatalf("selected action not visible: row %d bounds %+v", row, r)
	}
	_, _ = app.Update(tea.MouseClickMsg{X: x + r.X + 1, Y: y + r.Y + row, Button: tea.MouseLeft})
	_, _ = app.Update(tea.MouseReleaseMsg{X: x + r.X + 1, Y: y + r.Y + row, Button: tea.MouseLeft})
	_ = app.View()
}

func paneMenuText(t *testing.T, app *App) string {
	t.Helper()
	fw := app.windowManager.Window("context-menu")
	if fw == nil {
		t.Fatal("menu missing")
	}
	w, h := fw.Size()
	buf := surface.NewCellBuffer(w, h)
	host := fw.Content.(*ui.ComponentWidget).Host()
	host.Resize(w, h)
	host.Layout()
	host.Paint(buf)
	return bufferPlain(buf)
}

func paneRequireMenuBounds(t *testing.T, app *App) {
	t.Helper()
	fw := app.windowManager.Window("context-menu")
	x, y := fw.Position()
	w, h := fw.Size()
	insets := app.frameInsets()
	if x < insets.Left || y < insets.Top || x+w > app.width-insets.Right || y+h > app.height-insets.Bottom {
		t.Fatalf("menu escaped shell: %d,%d %dx%d", x, y, w, h)
	}
}

func paneResourceFixture(t *testing.T, app *App, kind window.ResourceKind) window.TabID {
	t.Helper()
	switch kind {
	case window.ResourceDocument:
		openEditorTabs(t, app, "target_marker.go")
		return app.Win.ActiveTab().ID
	case window.ResourceAgent:
		app.AddOrchestrator("pane-target-agent")
		app.appendActiveMessage(ttypes.ChatMessage{Role: ttypes.RoleAssistant, Content: "PANE_TARGET_TRANSCRIPT", Time: time.Unix(1, 0)})
		return app.Win.ActiveTab().ID
	case window.ResourcePanel:
		app.openPanelAsTab(termdock.PanelNotices)
		return app.Win.ActiveTab().ID
	case window.ResourceGitReview:
		v := app.openGitReviewTab(gitReviewResource("test", ""), "Target review")
		app.activateGitReviewTab(v)
		return v.tab
	case window.ResourceLocalDiff:
		openEditorTabs(t, app, "comparison_source.go")
		cmd := app.openLocalDiff(app.EditorState.Editor, localDiffSaved)
		if cmd == nil {
			t.Fatal("comparison missing")
		}
		_, _ = app.Update(cmd())
		return app.Win.ActiveTab().ID
	}
	t.Fatal("unsupported kind")
	return 0
}

func TestPaneContextResourceJourney(t *testing.T) {
	for _, kind := range []window.ResourceKind{window.ResourceDocument, window.ResourceAgent, window.ResourcePanel, window.ResourceGitReview, window.ResourceLocalDiff} {
		t.Run(string(kind), func(t *testing.T) {
			app, ws := newRealShellApp(t)
			defer ws.Close()
			paneJourneySize(app, 180, 56)
			openEditorTabs(t, app, "left_keep.go", "upper_keep.go")
			left := app.Win.ActiveGroup()
			upperTab := app.Win.ActiveTab().ID
			right := app.Win.SplitGroup(left, ui.Horizontal, false)
			app.Win.MoveTabToGroup(upperTab, right, 0)
			lower := app.Win.SplitGroup(right, ui.Vertical, false)
			tab := paneResourceFixture(t, app, kind)
			if kind == window.ResourceLocalDiff {
				view := app.localDiffs[tab]
				app.Win.MoveTabToGroup(view.returnTab, left, 0)
			}
			resource := app.Win.Tabs()[app.Win.IndexOf(tab)].Resource
			app.focusMainGroup(left)
			_ = app.View()
			paneOpenTabMenu(t, app, tab)
			for _, label := range []string{"Split Left", "Split Right", "Split Above", "Split Down", "Arrange Tab…", "Split Containing Layout…"} {
				menuItem(t, app.LastContextMenu, label)
			}
			paneChooseMenu(t, app, "Split Left", false)
			if app.Win.ActiveTab() != nil || len(app.Win.GroupIDs()) != 4 {
				t.Fatal("clicked resource did not split its own pane")
			}
			paneJourneyKey(app, 'w', tea.ModCtrl)
			if len(app.Win.GroupIDs()) != 3 || app.Win.GroupOf(tab) != lower {
				t.Fatal("empty split close moved resource")
			}
			paneOpenTabMenu(t, app, tab)
			paneChooseMenu(t, app, "Arrange Tab…", true)
			if app.LastContextMenu == nil || !strings.Contains(paneMenuText(t, app), "Entire layout") {
				t.Fatal("pointer activation destroyed scope follow-up")
			}
			paneChooseMenu(t, app, "Entire layout · 3 panes", false)
			if app.LastContextMenu == nil || !strings.Contains(paneMenuText(t, app), "Move tab Below") {
				t.Fatal("keyboard activation destroyed edge follow-up")
			}
			beforeCount := app.Win.TabCount()
			paneChooseMenu(t, app, "Move tab Below", true)
			if app.Win.TabCount() != beforeCount || app.Win.ActiveTab() == nil || app.Win.ActiveTab().ID != tab || app.Win.HasGroup(lower) {
				t.Fatal("arrange duplicated resource or left sole-tab source")
			}
			root := app.Win.PaneRoot()
			if root.Split == nil || root.Split.Dir() != ui.Vertical || root.Second.Group != app.Win.GroupOf(tab) {
				t.Fatal("move below root did not wrap surviving mixed-axis layout")
			}
			if app.Win.ActiveTab().Resource != resource {
				t.Fatal("resource identity changed")
			}
			if kind == window.ResourceDocument {
				paneJourneyRequireText(t, app, root.Second.Group, "target_marker.go")
			}
			if kind == window.ResourceAgent {
				paneJourneyRequireText(t, app, root.Second.Group, "PANE_TARGET_TRANSCRIPT")
			}
			paneOpenTabMenu(t, app, tab)
			closeLabel := "Close tab"
			if kind == window.ResourceAgent {
				closeLabel = "Minimize"
			}
			paneChooseMenu(t, app, closeLabel, false)
			if app.Win.IndexOf(tab) >= 0 {
				t.Fatal("resource close/minimize did not remove exact tab")
			}
			if kind == window.ResourceAgent && (ws.AgentMgr.Get("pane-target-agent") == nil || !app.AgentView.IsHidden("pane-target-agent")) {
				t.Fatal("close terminated instead of minimizing agent")
			}
			if kind == window.ResourcePanel && app.placementOf(termdock.PanelNotices) != placeDock {
				t.Fatal("surface close did not return to dock")
			}
		})
	}
}

func TestPaneContextStaleOwnerNeverFallsThrough(t *testing.T) {
	for _, change := range []string{"close", "move", "workspace", "replacement"} {
		t.Run(change, func(t *testing.T) {
			app, ws := newRealShellApp(t)
			defer ws.Close()
			paneJourneySize(app, 160, 42)
			paths := openEditorTabs(t, app, "clicked.go", "unrelated.go")
			clicked := app.Win.TabByTitle(app.editorTabTitle(paths[0])).ID
			other := app.Win.ActiveTab().ID
			paneOpenTabMenu(t, app, clicked)
			switch change {
			case "close":
				app.Win.CloseTabByID(clicked)
			case "move":
				g := app.Win.SplitGroup(app.Win.ActiveGroup(), ui.Horizontal, false)
				app.Win.MoveTabToGroup(clicked, g, 0)
			case "workspace":
				app.workspaceSwitchGen.Add(1)
			case "replacement":
				app.Win = window.New(app.Theme)
				other = app.Win.OpenTab("replacement", "", true).ID
			}
			app.Win.SwitchTo(app.Win.IndexOf(other))
			app.handleTabSwitch()
			beforeTabs, tree := app.Win.Tabs(), paneTopology(app.Win.PaneRoot())
			paneChooseMenu(t, app, "Move Tab to Right Split", false)
			if !reflect.DeepEqual(beforeTabs, app.Win.Tabs()) || !slices.Equal(tree, paneTopology(app.Win.PaneRoot())) {
				t.Fatal("stale clicked menu acted on current resource/layout")
			}
		})
	}
}

func TestPaneContextOwnerSurvivesReorderButNotStaleScope(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 180, 52)
	paths := openEditorTabs(t, app, "clicked.go", "second.go", "third.go")
	clicked := app.Win.TabByTitle(app.editorTabTitle(paths[0])).ID
	original := app.Win.ActiveGroup()
	paneOpenTabMenu(t, app, clicked)
	app.Win.MoveTab(app.Win.IndexOf(clicked), 2)
	paneChooseMenu(t, app, "Open in Window", false)
	if !app.Win.IsFloating(app.Win.GroupOf(clicked)) || app.Win.GroupOf(app.Win.TabByTitle(app.editorTabTitle(paths[2])).ID) != original {
		t.Fatal("reordered menu acted on captured index instead of exact resource")
	}
	app.dockPaneWindow(app.Win.GroupOf(clicked))
	app.focusMainGroup(original)
	right := app.Win.SplitGroup(original, ui.Horizontal, false)
	app.focusMainGroup(original)
	_ = app.View()
	paneOpenTabMenu(t, app, clicked)
	paneChooseMenu(t, app, "Arrange Tab…", false)
	paneChooseMenu(t, app, "Entire layout · 2 panes", false)
	app.Win.RemoveEmptyGroup(right)
	before := app.Win.Tabs()
	tree := paneTopology(app.Win.PaneRoot())
	paneChooseMenu(t, app, "Move tab Below", false)
	if !reflect.DeepEqual(before, app.Win.Tabs()) || !slices.Equal(tree, paneTopology(app.Win.PaneRoot())) {
		t.Fatal("stale exact scope rebound to replacement tree")
	}
}

func TestPaneContextGroupFileCloseSafety(t *testing.T) {
	for _, label := range []string{"Close Other Files in Pane", "Close Files to the Right"} {
		t.Run(label, func(t *testing.T) {
			app, ws := newRealShellApp(t)
			defer ws.Close()
			paneJourneySize(app, 180, 50)
			app.AddOrchestrator("keep-agent")
			agent := app.Win.ActiveTab().ID
			paths := openEditorTabs(t, app, "earlier.go", "clicked.go", "dirty.go", "later.go", "neighbor.go")
			clicked := app.Win.TabByTitle(app.editorTabTitle(paths[1])).ID
			dirty := app.EditorState.Tabs[paths[2]].Editor
			dirty.SetCursorPosition(0, 0)
			dirty.InsertRune('x')
			group := app.Win.ActiveGroup()
			neighbor := app.Win.SplitGroup(group, ui.Horizontal, false)
			app.Win.MoveTabToGroup(app.Win.TabByTitle(app.editorTabTitle(paths[4])).ID, neighbor, 0)
			app.focusMainGroup(neighbor)
			_ = app.View()
			paneOpenTabMenu(t, app, clicked)
			paneChooseMenu(t, app, label, true)
			if app.DirtyPromptPath != paths[2] || !dirty.Modified() || app.EditorState.Tabs[paths[3]] == nil {
				t.Fatal("bulk close bypassed dirty decision or continued past it")
			}
			paneJourneyKey(app, tea.KeyEsc, 0)
			if app.DirtyPromptPath != "" || app.EditorState.Tabs[paths[2]] == nil || app.EditorState.Tabs[paths[3]] == nil || app.Win.GroupOf(agent) != group || app.AgentView.IsHidden("keep-agent") || app.EditorState.Tabs[paths[4]] == nil {
				t.Fatal("cancel lost dirty/later/neighbor/agent resource")
			}
			if label == "Close Files to the Right" && app.EditorState.Tabs[paths[0]] == nil {
				t.Fatal("right-only close consumed earlier file")
			}
			paneOpenTabMenu(t, app, clicked)
			paneChooseMenu(t, app, label, false)
			paneJourneyKey(app, 'd', 0)
			if app.EditorState.Tabs[paths[2]] != nil || app.EditorState.Tabs[paths[3]] != nil || app.EditorState.Tabs[paths[1]] == nil || app.EditorState.Tabs[paths[4]] == nil || ws.AgentMgr.Get("keep-agent") == nil {
				t.Fatal("confirmed bulk close crossed group/resource boundary or did not resume")
			}
		})
	}
}

func TestPaneContextLargeScopeMenuScrollAndCompactRestore(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 180, 50)
	app.AddOrchestrator("source")
	source := app.Win.ActiveTab().ID
	for i := 0; i < 24; i++ {
		ids := app.Win.GroupIDs()
		app.Win.SplitGroup(ids[i%len(ids)], ui.Direction(i%2), false)
		app.AddOrchestrator(fmt.Sprintf("leaf-%02d", i))
	}
	app.focusMainGroup(app.Win.GroupOf(source))
	paneJourneySize(app, 60, 20)
	topology, tabs := paneTopology(app.Win.PaneRoot()), app.Win.Tabs()
	if !app.mainPanes.compact {
		t.Fatal("large topology did not compact")
	}
	paneOpenTabMenu(t, app, source)
	paneChooseMenu(t, app, "Arrange Tab…", false)
	paneRequireMenuBounds(t, app)
	st := app.LastContextMenu
	last := st.Sections[len(st.Sections)-1].Items[len(st.Sections[len(st.Sections)-1].Items)-1].Label
	for paneMenuLabel(st) != last {
		paneJourneyKey(app, tea.KeyDown, 0)
	}
	if !strings.Contains(paneMenuText(t, app), "Pane 25") {
		t.Fatalf("final scope %q not painted after scrolling:\n%s", last, paneMenuText(t, app))
	}
	paneChooseMenu(t, app, last, true)
	if app.LastContextMenu == nil || !strings.Contains(paneMenuText(t, app), "Move tab Below") {
		t.Fatal("last scrolled scope not pointer reachable")
	}
	paneJourneyKey(app, tea.KeyEsc, 0)
	if !slices.Equal(topology, paneTopology(app.Win.PaneRoot())) || !reflect.DeepEqual(tabs, app.Win.Tabs()) {
		t.Fatal("scope browsing/compact presentation changed topology")
	}
	// The deliberately deep tree fits only a very large grid; resolve checks
	// restoration without allocating an enormous screen surface.
	w, h := paneMinimum(app.Win.PaneRoot())
	app.mainPanes.resolve(ui.Rect{X: 3, Y: 4, W: w + 100, H: h + 10})
	if app.mainPanes.compact || len(app.mainPanes.placements) != 25 || !slices.Equal(topology, paneTopology(app.Win.PaneRoot())) {
		t.Fatal("enlargement failed to restore all exact leaves")
	}
}

func TestPaneContextTopFloatingHitAuthority(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 180, 52)
	paths := openEditorTabs(t, app, "dock.go", "front.go", "front_keep.go", "back.go", "back_keep.go")
	ids := make([]window.TabID, len(paths))
	for i, path := range paths {
		ids[i] = app.Win.TabByTitle(app.editorTabTitle(path)).ID
	}
	app.openTabInWindow(ids[1])
	front := app.Win.GroupOf(ids[1])
	app.Win.MoveTabToGroup(ids[2], front, 0)
	app.openTabInWindow(ids[3])
	back := app.Win.GroupOf(ids[3])
	app.Win.MoveTabToGroup(ids[4], back, 0)
	_ = app.View()
	// Put both floating strips exactly over the docked strip, then raise the
	// older-created front window. Creation-order or group-map hit tests fail.
	dockStrip, _ := app.screenRectOf(app.mainPanes.groups[app.Win.DockedGroup()].tabs.ID())
	for _, group := range []window.GroupID{front, back} {
		pw := app.mainPanes.windows[group]
		pw.fw.SetPosition(dockStrip.X-1, dockStrip.Y-1)
	}
	app.focusMainGroup(front)
	app.windowManager.FocusWindow(string(paneWindowID(front)))
	_ = app.View()
	x, y := paneTabPoint(t, app, ids[1])
	if app.paneTabAt(x, y) != app.Win.IndexOf(ids[1]) {
		t.Fatal("underlying strip won floating hit")
	}
	// Resize-edge hover must likewise belong only to the raised window.
	fx, fy := app.mainPanes.windows[front].fw.Position()
	_, _ = app.Update(tea.MouseMotionMsg{X: fx, Y: fy + 5, Button: tea.MouseNone})
	if app.mainPanes.windows[front].fw.HoverEdge() == 0 || app.mainPanes.windows[back].fw.HoverEdge() != 0 {
		t.Fatal("floating hover used creation order instead of top-window authority")
	}
	_, _ = app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseRight})
	_ = app.View()
	paneChooseMenu(t, app, "Close tab", false)
	if app.Win.IndexOf(ids[1]) >= 0 || app.Win.IndexOf(ids[3]) < 0 || app.Win.IndexOf(ids[0]) < 0 {
		t.Fatal("context close targeted underlying floating/docked tab")
	}
}

func TestPaneDragStaleFences(t *testing.T) {
	for _, change := range []string{"source", "target", "workspace", "resize", "release-retarget"} {
		t.Run(change, func(t *testing.T) {
			app, ws := newRealShellApp(t)
			defer ws.Close()
			paneJourneySize(app, 180, 50)
			paths := openEditorTabs(t, app, "source.go", "keep.go", "target.go")
			source := app.Win.TabByTitle(app.editorTabTitle(paths[0])).ID
			targetTab := app.Win.TabByTitle(app.editorTabTitle(paths[2])).ID
			left := app.Win.ActiveGroup()
			right := app.Win.SplitGroup(left, ui.Horizontal, false)
			app.Win.MoveTabToGroup(targetTab, right, 0)
			app.focusMainGroup(left)
			_ = app.View()
			r := app.mainPanes.groups[right].body
			x, y := r.X+r.W/2, r.Y+r.H/2
			paneJourneyDrag(t, app, source, x, y)
			if app.mainPanes.drop == nil {
				t.Fatal("drag not armed")
			}
			switch change {
			case "source":
				app.Win.MoveTabToGroup(source, right, 0)
			case "target":
				app.Win.SplitGroup(right, ui.Vertical, false)
			case "workspace":
				app.workspaceSwitchGen.Add(1)
			case "resize":
				paneJourneySize(app, 164, 44)
			case "release-retarget":
				x = app.mainPanes.groups[left].body.X + 2
			}
			before, tree := app.Win.Tabs(), paneTopology(app.Win.PaneRoot())
			_, _ = app.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
			_ = app.View()
			if !reflect.DeepEqual(before, app.Win.Tabs()) || !slices.Equal(tree, paneTopology(app.Win.PaneRoot())) {
				t.Fatal("stale drop recomputed another resource or target")
			}
		})
	}
}

func TestPaneFloatingDragEscapeCancelsWindowHost(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 180, 52)
	paths := openEditorTabs(t, app, "dock.go", "float.go", "keep.go")
	tab := app.Win.TabByTitle(app.editorTabTitle(paths[1])).ID
	keep := app.Win.ActiveTab().ID
	app.openTabInWindow(tab)
	group := app.Win.GroupOf(tab)
	app.Win.MoveTabToGroup(keep, group, 0)
	_ = app.View()
	x, y := paneTabPoint(t, app, tab)
	target := app.mainPanes.groups[app.Win.DockedGroup()].body
	tx, ty := target.X+2, target.Y+4
	_, _ = app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	_, _ = app.Update(tea.MouseMotionMsg{X: tx, Y: ty, Button: tea.MouseLeft})
	_ = app.View()
	if !app.mainPaneDragActive() {
		t.Fatal("window drag not active")
	}
	paneJourneyKey(app, tea.KeyEsc, 0)
	_, _ = app.Update(tea.MouseReleaseMsg{X: tx, Y: ty, Button: tea.MouseLeft})
	_ = app.View()
	if app.Win.GroupOf(tab) != group || app.mainPaneDragActive() || app.paneWindowHost(group).HasPointerCapture() || app.mainPanes.drop != nil {
		t.Fatal("Esc left floating drag/capture alive or committed it")
	}
}

func paneTestShape(n *window.PaneNode) string {
	if n.Split == nil {
		return fmt.Sprintf("g%d", n.Group)
	}
	return fmt.Sprintf("%d(%s,%s)", n.Split.Dir(), paneTestShape(n.First), paneTestShape(n.Second))
}

func TestPaneArrangeMixedScopeAllEdges(t *testing.T) {
	for _, scope := range []string{"root", "ancestor", "unrelated"} {
		for _, edge := range []string{"Left", "Right", "Above", "Below"} {
			t.Run(scope+"/"+edge, func(t *testing.T) {
				app, ws := newRealShellApp(t)
				defer ws.Close()
				paneJourneySize(app, 200, 72)
				paths := openEditorTabs(t, app, "a.go", "b.go", "c.go", "source.go")
				ids := make([]window.TabID, len(paths))
				for i, path := range paths {
					ids[i] = app.Win.TabByTitle(app.editorTabTitle(path)).ID
				}
				a := app.Win.ActiveGroup()
				c := app.Win.SplitGroup(a, ui.Horizontal, false)
				app.Win.MoveTabToGroup(ids[2], c, 0)
				d := app.Win.SplitGroup(c, ui.Vertical, false)
				app.Win.MoveTabToGroup(ids[3], d, 0)
				b := app.Win.SplitGroup(a, ui.Vertical, false)
				app.Win.MoveTabToGroup(ids[1], b, 0)
				app.focusMainGroup(a)
				_ = app.View()
				ed := app.EditorState.Tabs[paths[3]].Editor
				ed.InsertRune('x')
				bytes := ed.Value()
				paneOpenTabMenu(t, app, ids[3])
				paneChooseMenu(t, app, "Arrange Tab…", false)
				label := "Entire layout · 4 panes"
				switch scope {
				case "ancestor":
					label = "Containing layout · panes 3–4"
				case "unrelated":
					label = "Containing layout · panes 1–2"
				}
				paneChooseMenu(t, app, label, true)
				paneChooseMenu(t, app, "Move tab "+edge, false)
				newGroup := app.Win.GroupOf(ids[3])
				leaf := func(g window.GroupID) string { return fmt.Sprintf("g%d", g) }
				branch := func(dir ui.Direction, first, second string) string {
					return fmt.Sprintf("%d(%s,%s)", dir, first, second)
				}
				wrap := func(old string) string {
					dir := ui.Horizontal
					if edge == "Above" || edge == "Below" {
						dir = ui.Vertical
					}
					if edge == "Left" || edge == "Above" {
						return branch(dir, leaf(newGroup), old)
					}
					return branch(dir, old, leaf(newGroup))
				}
				left := branch(ui.Vertical, leaf(a), leaf(b))
				want := ""
				switch scope {
				case "root":
					want = wrap(branch(ui.Horizontal, left, leaf(c)))
				case "ancestor":
					want = branch(ui.Horizontal, left, wrap(leaf(c)))
				case "unrelated":
					want = branch(ui.Horizontal, wrap(left), leaf(c))
				}
				if got := paneTestShape(app.Win.PaneRoot()); got != want {
					t.Fatalf("exact subtree move: got %s want %s", got, want)
				}
				if app.Win.HasGroup(d) || app.Win.TabCount() != 4 || app.EditorState.Editor != ed || ed.Value() != bytes || !ed.Modified() {
					t.Fatal("atomic placement lost source collapse, resource, dirty bytes or focused editor binding")
				}
				paneJourneyRequireText(t, app, newGroup, "source.go")
				paneJourneyKey(app, 'q', 0)
				if ed.Value() == bytes {
					t.Fatal("typing after arrange did not reach moved editor")
				}
			})
		}
	}
}

func TestPaneAncestorDragPreviewAllEdges(t *testing.T) {
	for _, edge := range []string{"left", "right", "top", "bottom"} {
		t.Run(edge, func(t *testing.T) {
			app, ws := newRealShellApp(t)
			defer ws.Close()
			paneJourneySize(app, 200, 64)
			paths := openEditorTabs(t, app, "keep.go", "source.go", "upper.go", "lower.go")
			source := app.Win.TabByTitle(app.editorTabTitle(paths[1])).ID
			left := app.Win.ActiveGroup()
			right := app.Win.SplitGroup(left, ui.Horizontal, false)
			app.Win.MoveTabToGroup(app.Win.TabByTitle(app.editorTabTitle(paths[2])).ID, right, 0)
			lower := app.Win.SplitGroup(right, ui.Vertical, false)
			app.Win.MoveTabToGroup(app.Win.TabByTitle(app.editorTabTitle(paths[3])).ID, lower, 0)
			app.focusMainGroup(left)
			_ = app.View()
			r := app.mainPanes.Bounds()
			x, y := r.X+r.W/2+2, r.Y+r.H/3
			switch edge {
			case "left":
				x = r.X
			case "right":
				x = r.X + r.W - 1
			case "top":
				x, y = r.X+r.W/4, r.Y+ui.StripHeight
			case "bottom":
				x, y = r.X+r.W/4, r.Y+r.H-1
			}
			tabs, topology := app.Win.Tabs(), paneTestShape(app.Win.PaneRoot())
			paneJourneyDrag(t, app, source, x, y)
			drop := app.mainPanes.drop
			if drop == nil || drop.node != app.Win.PaneRoot().ID || drop.edge != edge || !strings.Contains(paneJourneyText(app, drop.rect), "Entire layout") {
				t.Fatal("outer rim did not preview exact entire-layout edge")
			}
			if !reflect.DeepEqual(tabs, app.Win.Tabs()) || topology != paneTestShape(app.Win.PaneRoot()) {
				t.Fatal("preview mutated layout")
			}
			preview := drop.rect
			// Independently check the promised new half including the single
			// divider; comparing two users of paneSplitGeometry alone is not an oracle.
			want := r
			switch edge {
			case "left":
				want.W = r.W / 2
			case "right":
				want.X, want.W = r.X+r.W/2+1, r.W-r.W/2-1
			case "top":
				want.H = r.H / 2
			case "bottom":
				want.Y, want.H = r.Y+r.H/2+1, r.H-r.H/2-1
			}
			if preview != want {
				t.Fatalf("preview %+v does not reserve expected edge/divider %+v", preview, want)
			}
			_, _ = app.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
			_ = app.View()
			g := app.mainPanes.groups[app.Win.GroupOf(source)]
			actual := g.screen(ui.Rect{W: g.Bounds().W, H: g.Bounds().H})
			if actual != preview {
				t.Fatalf("preview %+v disagrees with commit %+v", preview, actual)
			}
			root := app.Win.PaneRoot()
			if (edge == "left" || edge == "top") && root.First.Group != g.id || (edge == "right" || edge == "bottom") && root.Second.Group != g.id {
				t.Fatal("commit placed wrong edge")
			}
		})
	}
}

func TestPaneAgentMinimizeLeavesDirtyFileAndTranscript(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 180, 50)
	agent := paneResourceFixture(t, app, window.ResourceAgent)
	paths := openEditorTabs(t, app, "dirty_keep.go")
	ed := app.EditorState.Editor
	paneJourneyKey(app, 'x', 0)
	bytes := ed.Value()
	paneOpenTabMenu(t, app, agent)
	paneChooseMenu(t, app, "Minimize", true)
	if app.EditorState.Tabs[paths[0]].Editor != ed || ed.Value() != bytes || !ed.Modified() || app.DirtyPromptPath != "" || ws.AgentMgr.Get("pane-target-agent") == nil {
		t.Fatal("agent minimize consumed dirty file or terminated agent")
	}
	app.restoreAgent("pane-target-agent")
	_ = app.View()
	paneJourneyRequireText(t, app, app.Win.ActiveGroup(), "PANE_TARGET_TRANSCRIPT")
}

func TestPaneContainingSplitAndMoveIntoFollowups(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 180, 56)
	paths := openEditorTabs(t, app, "left.go", "right.go")
	left := app.Win.ActiveGroup()
	right := app.Win.SplitGroup(left, ui.Horizontal, false)
	tab := app.Win.TabByTitle(app.editorTabTitle(paths[1])).ID
	app.Win.MoveTabToGroup(tab, right, 0)
	app.focusMainGroup(left)
	_ = app.View()
	paneOpenTabMenu(t, app, tab)
	paneChooseMenu(t, app, "Split Containing Layout…", true)
	paneChooseMenu(t, app, "Entire layout · 2 panes", false)
	paneChooseMenu(t, app, "Split Above", true)
	root := app.Win.PaneRoot()
	if root.Split == nil || root.Split.Dir() != ui.Vertical || root.First.Group != app.Win.ActiveGroup() || app.Win.ActiveTab() != nil || root.Second.Split.Dir() != ui.Horizontal {
		t.Fatal("containing-layout split did not wrap intact original layout with empty top pane")
	}
	empty := root.First.Group
	app.Win.RemoveEmptyGroup(empty)
	app.focusMainGroup(right)
	_ = app.View()
	paneOpenTabMenu(t, app, tab)
	paneChooseMenu(t, app, "Arrange Tab…", false)
	paneChooseMenu(t, app, "Pane 1 · left.go", true)
	paneChooseMenu(t, app, "Move into this pane", true)
	if app.Win.HasGroup(right) || app.Win.GroupOf(tab) != left || app.Win.TabCount() != 2 || app.Win.ActiveTab().ID != tab || app.Win.PaneRoot().Split != nil {
		t.Fatal("Move into existing pane failed exact identity/source collapse")
	}
	paneJourneyRequireText(t, app, left, "right.go")
}
