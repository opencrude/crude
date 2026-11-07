package viewmodel

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	filepickerscreen "crude/tui/screens/filepicker"
	"crude/tui/ui"
	"crude/tui/window"
)

// paneDrop is a proposed destination in the main area. Its source is either
// a dragged tab or, when path is set, a file dragged from the file tree.
type paneDrop struct {
	tab       window.TabID
	path      string
	group     window.GroupID
	node      uint64
	beforeTab window.TabID
	edge      string
	rect      ui.Rect
	scope     string
}

// The press owns the source; the last motion owns the proposed destination.
// Release may confirm that exact proposal, never retarget a changed layout.
type paneDrag struct {
	owner    paneMenuOwner
	topology []uint64
	bounds   ui.Rect
}

func paneTopology(n *window.PaneNode) []uint64 {
	var ids []uint64
	var visit func(*window.PaneNode)
	visit = func(n *window.PaneNode) {
		if n == nil {
			return
		}
		ids = append(ids, n.ID, uint64(n.Group))
		visit(n.First)
		visit(n.Second)
	}
	visit(n)
	return ids
}

func (d *paneDrag) valid(m *App, tab window.TabID) bool {
	return d != nil && d.owner.tab == tab && d.owner.valid(m) &&
		d.bounds == m.mainPanes.Bounds() && slices.Equal(d.topology, paneTopology(m.Win.PaneRoot()))
}

func (m *App) cancelMainPaneDrag() {
	if p := m.mainPanes; p != nil {
		p.drop, p.drag = nil, nil
		for _, g := range p.groups {
			g.tabs.TabsControl().CancelPointerCapture()
		}
		for _, pw := range p.windows {
			pw.frame.Host().CancelPointerCapture()
		}
	}
	if m.rootHost != nil {
		m.rootHost.CancelPointerCapture()
	}
}

// A menu belongs to the clicked tab's original group, not whatever is active
// when it is used. Moving/closing that tab or switching workspace invalidates it.
type paneMenuOwner struct {
	win        *window.Window
	group      window.GroupID
	tab        window.TabID
	generation uint64
}

func (m *App) paneOwner(tab window.TabID, group window.GroupID) paneMenuOwner {
	return paneMenuOwner{m.Win, group, tab, m.workspaceSwitchGen.Load()}
}

func (o paneMenuOwner) valid(m *App) bool {
	return m.Win == o.win && m.workspaceSwitchGen.Load() == o.generation && m.Win.HasGroup(o.group) &&
		(o.tab == 0 || m.Win.GroupOf(o.tab) == o.group)
}

func (o paneMenuOwner) action(m *App, action func()) func() {
	return func() {
		if o.valid(m) {
			action()
		}
	}
}

type paneFilePickerOwner struct {
	group      window.GroupID
	generation uint64
}

func (m *App) emptyPaneActive() bool {
	return m.Win != nil && m.Win.ActiveTab() == nil && !m.mainAreaEmpty()
}

func (m *App) mainPaneDragActive() bool {
	if m.mainPanes == nil {
		return false
	}
	for _, g := range m.mainPanes.groups {
		if g.tabs.TabsControl().Dragging() {
			return true
		}
	}
	return false
}

func (m *App) emptyPaneMenu() *ui.ContextMenuState {
	group := m.Win.ActiveGroup()
	owner := m.paneOwner(0, group)
	menu := &ui.ContextMenuState{Title: "Pane", Sections: []ui.ContextMenuSection{{}}}
	for _, action := range []struct{ label, id string }{
		{"New Session", "pane.new-session"}, {"Open File", "pane.open-file"},
		{"Split Left", "pane.split-left"}, {"Split Right", "pane.split-right"},
		{"Split Up", "pane.split-up"}, {"Split Down", "pane.split-down"},
		{"Split Containing Layout…", "pane.split-layout"},
		{"Close Empty Pane", "pane.close"}, {"Merge All Panes", "pane.merge-all"},
	} {
		id := action.id
		menu.Sections[0].Items = append(menu.Sections[0].Items, ui.ContextMenuItem{Label: action.label, Action: owner.action(m, func() {
			m.focusMainGroup(group)
			cmd, _ := m.handleMainPaneCommand(id)
			m.runDetachedAsyncCmd(cmd)
		})})
	}
	return menu
}

// openMainPaneMenu keeps compacted groups reachable by mouse as well as keys.
func (m *App) openMainPaneMenu() tea.Cmd {
	menu := &ui.ContextMenuState{Title: "Panes", Sections: []ui.ContextMenuSection{{}}}
	for i, group := range m.Win.GroupIDs() {
		label := fmt.Sprintf("Pane %d · Empty", i+1)
		if tab := m.Win.GroupActiveTab(group); tab != nil {
			title := tab.DisplayTitle
			if title == "" {
				title = tab.Title
			}
			label = fmt.Sprintf("Pane %d · %s", i+1, title)
		}
		id := group
		menu.Sections[0].Items = append(menu.Sections[0].Items, ui.ContextMenuItem{Label: label, Action: m.paneOwner(0, id).action(m, func() { m.focusMainGroup(id) })})
	}
	return m.openContextMenuState(tea.Mouse{X: m.frameLeftCol() + 8, Y: m.shellTopRow()}, menu)
}

func (m *App) paneAt(x, y int) *mainPaneGroup {
	if m.mainPanes == nil {
		return nil
	}
	p := m.mainPanes
	for id, r := range p.placements {
		r.X += p.Bounds().X
		r.Y += p.Bounds().Y
		if r.Contains(x, y) {
			return p.groups[id]
		}
	}
	return nil
}

// Context targeting comes from the mounted strip under both coordinates, never
// the active strip alias (which may belong to another docked or floating pane).
func (m *App) paneTabAt(x, y int) int {
	if m.mainPanes == nil {
		return -1
	}
	ids := m.Win.GroupIDs()
	if m.windowManager != nil {
		if fw := m.windowManager.WindowAt(x, y); fw != nil {
			group, ok := paneGroupFromOverlay(ui.OverlayID(fw.ID))
			if !ok {
				return -1
			}
			ids = []window.GroupID{group}
		}
	}
	for _, id := range ids {
		g := m.mainPanes.groups[id]
		if g == nil {
			continue
		}
		r, ok := m.screenRectOf(g.tabs.ID())
		if !ok || !r.Contains(x, y) {
			continue
		}
		i, _ := g.tabs.HitTest(x - r.X)
		tabs := m.Win.GroupTabs(id)
		if i >= 0 && i < len(tabs) {
			return m.Win.IndexOf(tabs[i].ID)
		}
	}
	return -1
}

func (m *App) closePaneEditorTabs(owner paneMenuOwner, rightOnly bool) {
	if !owner.valid(m) {
		return
	}
	tabs := m.Win.GroupTabs(owner.group)
	var ids []window.TabID
	seen := !rightOnly
	for _, tab := range tabs {
		if tab.ID == owner.tab {
			seen = true
			continue
		}
		if seen && m.editorView(tab.ID) != nil {
			ids = append(ids, tab.ID)
		}
	}
	m.releaseTabBarFocus()
	m.closeEditorViews(ids)
	m.syncRootFocus()
}

// activatePaneAt is input policy, called after floating/modal ownership checks.
// Rebind before hit dispatch so a click never edits another pane's composer.
func (m *App) activatePaneAt(x, y int) {
	g := m.paneAt(x, y)
	if g == nil || g.id == m.Win.ActiveGroup() {
		return
	}
	m.focusMainGroup(g.id)
	m.configureShell()
	m.rootHost.MarkLayout()
	m.rootHost.Layout()
}

func (m *App) focusMainGroup(id window.GroupID) {
	if !m.Win.FocusGroup(id) {
		return
	}
	m.unfocus()
	m.handleTabSwitch()
	if projection := m.activeSessionDeck(); projection != nil && m.sessionDeckBlocking() {
		if panel := projection.deck.Active(); panel != nil {
			projection.deckFocused = true
			projection.deckFocus = panel.FocusID
		}
	}
	m.forceFrame()
}

func (m *App) splitMainGroup(dir ui.Direction, before bool, tab window.TabID) window.GroupID {
	scopes := m.Win.PaneScopes(m.Win.ActiveGroup())
	if len(scopes) != 0 {
		return m.splitMainPane(scopes[0].ID, dir, before, tab)
	}
	return 0
}

// splitEditor splits the focused pane. A file is shown again in the new pane
// at the same caret and scroll (one document, two views), as in established
// editors; any other tab leaves the new pane empty, ready for New Session or
// Open File.
func (m *App) splitEditor(dir ui.Direction, before bool) {
	view := m.editorView(m.EditorState.ActiveTab)
	if active := m.Win.ActiveTab(); active == nil || view == nil || active.ID != view.Tab {
		view = nil
	}
	target := m.splitMainGroup(dir, before, 0)
	if target != 0 && view != nil {
		m.showDocument(view.Path, target, true)
		m.focusMainGroup(target)
	}
}

// openBeside shows a file tab's document in the pane on that side, adding a
// split when there is none. The source tab stays where it is.
func (m *App) openBeside(tab window.TabID, dir ui.Direction, before bool) {
	view := m.editorView(tab)
	if view == nil {
		return
	}
	target := m.Win.Neighbor(m.Win.GroupOf(tab), dir, before)
	if target == 0 {
		m.Win.SwitchTo(m.Win.IndexOf(tab))
		target = m.splitMainGroup(dir, before, 0)
	}
	if target != 0 && m.showDocument(view.Path, target, true) != nil {
		m.focusMainGroup(target)
	}
}

// moveBeside moves a tab into the pane on that side, adding a split when
// there is none. A file already shown there keeps that view and closes this
// one, so a pane never holds the same file twice.
func (m *App) moveBeside(tab window.TabID, dir ui.Direction, before bool) {
	source := m.Win.GroupOf(tab)
	target := m.Win.Neighbor(source, dir, before)
	if target == 0 {
		m.Win.SwitchTo(m.Win.IndexOf(tab))
		m.splitMainGroup(dir, before, tab)
		return
	}
	// A file the target already shows keeps that view (Window absorbs the
	// moved one; releaseAbsorbedViews closes it).
	if m.Win.MoveTabToGroup(tab, target, 0) {
		m.focusMainGroup(target)
		m.syncPaneWindows()
	}
}

// splitWithAgent opens an agent beside the focused tab: the agent the user
// worked with last, else a new session in the new pane.
func (m *App) splitWithAgent(dir ui.Direction, before bool) tea.Cmd {
	agentID := m.lastAgentID()
	target := m.splitMainGroup(dir, before, 0)
	if target == 0 {
		return nil
	}
	if agentID == "" {
		return m.launcherNewSession()
	}
	m.restoreAgent(agentID)
	if tab := m.Win.TabByTitle(m.agentTabTitle(agentID)); tab != nil {
		if m.Win.GroupOf(tab.ID) != target && m.Win.MoveTabToGroup(tab.ID, target, 0) {
			m.focusMainGroup(target)
			m.syncPaneWindows()
		}
	}
	return nil
}

// splitWithFile opens a file beside the focused tab: the file the user edited
// last, else the file picker for the new pane.
func (m *App) splitWithFile(dir ui.Direction, before bool) {
	path := m.lastEditorPath()
	target := m.splitMainGroup(dir, before, 0)
	if target == 0 {
		return
	}
	if path == "" || m.showDocument(path, target, true) == nil {
		m.openPaneFilePicker(target)
		return
	}
	m.focusMainGroup(target)
}

// lastAgentID is the visible or minimized agent the user focused most
// recently, or the first agent tab.
func (m *App) lastAgentID() string {
	if id := m.lastFocusedAgent; id != "" && m.ws.AgentMgr().Get(id) != nil {
		return id
	}
	for _, tab := range m.Win.Tabs() {
		if m.isAgentTabTitle(tab.Title) {
			return m.agentIDFromTabTitle(tab.Title)
		}
	}
	return ""
}

// lastEditorPath is the open file the user focused most recently.
func (m *App) lastEditorPath() string {
	if path := m.lastFocusedEditorPath; path != "" && m.EditorState.Tabs[path] != nil {
		return path
	}
	for _, tab := range m.Win.Tabs() {
		if v := m.editorView(tab.ID); v != nil {
			return v.Path
		}
	}
	return ""
}

func (m *App) splitMainPane(node uint64, dir ui.Direction, before bool, tab window.TabID) window.GroupID {
	p := window.TabPlacement{Tab: tab, Node: node, Split: true, Direction: dir, Before: before}
	var root *window.PaneNode
	var target window.GroupID
	if tab == 0 {
		root, target = m.Win.PreviewSplitPane(node, dir, before)
	} else {
		root, target = m.Win.PreviewTabPlacement(p)
	}
	if target == 0 {
		return 0
	}
	w, h := paneMinimum(root)
	bounds := m.ensureMainPanes().Bounds()
	if w > bounds.W || h > bounds.H {
		m.Feedback.NotifyInfo("There is not enough room for another pane. Make the terminal larger or merge a pane.")
		return 0
	}
	if tab == 0 {
		target = m.Win.SplitPane(node, dir, before)
	} else {
		target = m.Win.PlaceTab(p)
	}
	if target != 0 {
		m.focusMainGroup(target)
		m.syncPaneWindows()
	}
	return target
}

func (m *App) cycleMainGroup(delta int, move bool) {
	ids := m.Win.GroupIDs()
	if len(ids) < 2 {
		return
	}
	for i, id := range ids {
		if id != m.Win.ActiveGroup() {
			continue
		}
		target := ids[(i+delta+len(ids))%len(ids)]
		if move {
			if tab := m.Win.ActiveTab(); tab != nil {
				m.Win.MoveTabToGroup(tab.ID, target, 0)
			}
		}
		m.focusMainGroup(target)
		return
	}
}

func (m *App) handleMainPaneCommand(id string) (tea.Cmd, bool) {
	if id == "nav.back" || id == "nav.forward" {
		// A diff document keeps Alt+Left/Right for its previous/next file.
		if m.focusedPopup() != nil || m.focusedDiffDocument() {
			return nil, false
		}
		return m.navigateHistory(id == "nav.back"), true
	}
	if !strings.HasPrefix(id, "pane.") {
		return nil, false
	}
	// Pane commands do not bypass a modal window or the terminal's keys.
	if m.focusedPopup() != nil {
		return nil, true
	}
	switch id {
	case "pane.split-left", "pane.split-right", "pane.split-up", "pane.split-down":
		m.splitEditor(paneEdge(strings.TrimPrefix(id, "pane.split-")))
	case "pane.move-left", "pane.move-right", "pane.move-up", "pane.move-down",
		"pane.open-left", "pane.open-right", "pane.open-up", "pane.open-down":
		active := m.Win.ActiveTab()
		if active == nil {
			return nil, true
		}
		dir, before := paneEdge(strings.TrimPrefix(strings.TrimPrefix(id, "pane.move-"), "pane.open-"))
		if strings.HasPrefix(id, "pane.open-") {
			m.openBeside(active.ID, dir, before)
		} else {
			m.moveBeside(active.ID, dir, before)
		}
	case "pane.split-agent-right", "pane.split-agent-down":
		return m.splitWithAgent(paneEdge(strings.TrimPrefix(id, "pane.split-agent-"))), true
	case "pane.split-file-right", "pane.split-file-down":
		m.splitWithFile(paneEdge(strings.TrimPrefix(id, "pane.split-file-")))
	case "pane.arrange", "pane.split-layout":
		tab := window.TabID(0)
		if id == "pane.arrange" {
			if active := m.Win.ActiveTab(); active != nil {
				tab = active.ID
			} else {
				return nil, true
			}
		}
		m.openPaneScopeMenu(m.paneOwner(tab, m.Win.ActiveGroup()), id == "pane.arrange")
	case "pane.next":
		m.cycleMainGroup(1, false)
	case "pane.previous":
		m.cycleMainGroup(-1, false)
	case "pane.move-next":
		m.cycleMainGroup(1, true)
	case "pane.move-previous":
		m.cycleMainGroup(-1, true)
	case "pane.tab-left", "pane.tab-right":
		tabs := m.Win.GroupTabs(m.Win.ActiveGroup())
		if active := m.Win.ActiveTab(); active != nil {
			for i, tab := range tabs {
				if tab.ID != active.ID {
					continue
				}
				next := i - 1
				if id == "pane.tab-right" {
					next = i + 1
				}
				if next >= 0 && next < len(tabs) {
					m.Win.MoveTab(m.Win.IndexOf(tab.ID), m.Win.IndexOf(tabs[next].ID))
				}
				break
			}
			m.releaseTabBarFocus()
		}
	case "pane.merge-all":
		m.Win.MergeAllGroups()
		m.focusMainGroup(m.Win.ActiveGroup())
	case "pane.close":
		if m.Win.RemoveEmptyGroup(m.Win.ActiveGroup()) {
			m.focusMainGroup(m.Win.ActiveGroup())
		}
	case "pane.new-session":
		return m.launcherNewSession(), true
	case "pane.open-file":
		m.openPaneFilePicker(m.Win.ActiveGroup())
	case "pane.grow", "pane.shrink":
		delta := 3
		if id == "pane.shrink" {
			delta = -3
		}
		m.resizeMainGroup(delta)
	default:
		return nil, false
	}
	return nil, true
}

func (m *App) resizeMainGroup(delta int) {
	id := m.Win.ActiveGroup()
	var visit func(*window.PaneNode) bool
	visit = func(n *window.PaneNode) bool {
		if n == nil || n.Split == nil {
			return false
		}
		if visit(n.First) || visit(n.Second) {
			return true
		}
		if n.First.Group != id && n.Second.Group != id {
			return false
		}
		p := m.ensureMainPanes()
		if d := p.dividers[n.ID]; d != nil {
			if n.Second.Group == id {
				delta = -delta
			}
			n.Split.NudgeFirst(delta, p.dividerFirst[n.ID])
		}
		return true
	}
	visit(m.Win.PaneRoot())
	m.forceFrame()
}

func (m *App) openPaneFilePicker(group window.GroupID) {
	m.focusMainGroup(group)
	m.openPicker(m.ws.RootPath(), filepickerscreen.ModeOpenFile)
	m.paneFilePicker = &paneFilePickerOwner{group: group, generation: m.workspaceSwitchGen.Load()}
	if win := m.overlayHost.Window(ui.OverlayFilePicker); win != nil {
		win.Title = "Open File in Pane"
		if frame, ok := win.Content.(*ui.WindowFrameWidget); ok {
			frame.Frame().SetTitle(win.Title)
			win.MarkContentDirty()
		}
	}
}

func (m *App) handleMainPaneAction(a *ui.Action) (tea.Cmd, bool) {
	if a == nil {
		return nil, false
	}
	switch a.ID {
	case "tabs.new", "pane.new-session", "pane.open-file":
		group, ok := a.Payload.(window.GroupID)
		if !ok {
			group = m.Win.ActiveGroup()
		}
		if !paneHasGroup(m.Win.PaneRoot(), group) && !m.Win.IsFloating(group) {
			return nil, true
		}
		m.focusMainGroup(group)
		if a.ID == "pane.open-file" {
			m.openPaneFilePicker(group)
			return nil, true
		}
		return m.launcherNewSession(), true
	case "tabs.activate", "tabs.close", "tabs.enter":
		if a.ID == "tabs.activate" {
			if key, ok := a.Payload.(string); ok {
				id, err := strconv.ParseUint(key, 10, 64)
				group := m.Win.GroupOf(window.TabID(id))
				p := m.ensureMainPanes()
				if g := p.groups[group]; err == nil && g != nil && g.tabs.TabsControl().ActivatedByPointer() {
					p.drag = &paneDrag{owner: m.paneOwner(window.TabID(id), group), topology: paneTopology(m.Win.PaneRoot()), bounds: p.Bounds()}
					p.drop = nil
				}
			}
		}
		return m.handleTabBarHostResult(ui.EventResult{Action: a}), true
	case "tabs.reorder":
		reorder, ok := a.Payload.(ui.TabReorder)
		if !ok {
			return nil, true
		}
		id, err := strconv.ParseUint(reorder.ID, 10, 64)
		if err != nil {
			return nil, true
		}
		p := m.ensureMainPanes()
		valid := p.drag.valid(m, window.TabID(id))
		p.drop, p.drag = nil, nil
		if !valid {
			return nil, true
		}
		var before uint64
		if reorder.BeforeID != "" {
			if before, err = strconv.ParseUint(reorder.BeforeID, 10, 64); err != nil {
				return nil, true
			}
		}
		group := m.Win.GroupOf(window.TabID(id))
		if target := m.Win.PlaceTab(window.TabPlacement{Tab: window.TabID(id), Group: group, BeforeTab: window.TabID(before)}); target != 0 {
			m.focusMainGroup(target)
		}
		return nil, true
	case "tabs.drag", "tabs.drop":
		gesture, ok := a.Payload.(ui.TabDrag)
		if !ok {
			return nil, true
		}
		id, err := strconv.ParseUint(gesture.ID, 10, 64)
		if err != nil {
			return nil, true
		}
		p := m.ensureMainPanes()
		if !p.drag.valid(m, window.TabID(id)) {
			m.cancelMainPaneDrag()
			return nil, true
		}
		if a.ID == "tabs.drag" {
			// Over its own strip the tab reorders in place; pane placement
			// starts only once the pointer leaves the strip.
			p.drop = nil
			p.dragAt = [2]int{gesture.X, gesture.Y}
			if !gesture.InStrip {
				p.drop = m.mainPaneDrop(window.TabID(id), gesture.X, gesture.Y)
			}
			return nil, true
		}
		drop := p.drop
		p.drop, p.drag = nil, nil
		if drop == nil {
			return nil, true
		}
		confirmed := m.mainPaneDrop(window.TabID(id), gesture.X, gesture.Y)
		if confirmed == nil || confirmed.placement() != drop.placement() || confirmed.rect != drop.rect {
			return nil, true
		}
		if target := m.Win.PlaceTab(drop.placement()); target != 0 {
			m.focusMainGroup(target)
		}
		return nil, true
	}
	return nil, false
}

// tabsAnimating reports a tab strip that is dragging or sliding tabs.
func (p *mainPaneLayout) tabsAnimating() bool {
	if p == nil {
		return false
	}
	for _, g := range p.groups {
		if g.tabs.TabsControl().NeedsAnimation() {
			return true
		}
	}
	return false
}

// repaintAnimatingTabs damages only the strips whose tabs are in motion.
func (m *App) repaintAnimatingTabs() {
	p := m.mainPanes
	if p == nil {
		return
	}
	for id, g := range p.groups {
		control := g.tabs.TabsControl()
		if !control.NeedsAnimation() {
			continue
		}
		if pw := p.windows[id]; pw != nil {
			pw.frame.MarkContentDirty()
		} else if m.rootHost != nil {
			m.rootHost.MarkComponentPaint(control.ID())
		}
	}
}

// dockedPaneAt is the docked pane under a drag pointer; floating windows on
// top of it take no drops.
func (m *App) dockedPaneAt(x, y int) *mainPaneGroup {
	if m.windowManager != nil {
		for _, w := range m.windowManager.Windows() {
			if w.Visible() && w.ContainsPoint(x, y) {
				return nil
			}
		}
	}
	return m.paneAt(x, y)
}

// paneEdgeZone is the split side for a drop at a pane-local point: the outer
// quarters split, the middle does not. Tab and file drops share this rule.
func paneEdgeZone(r ui.Rect, xl, yl int) string {
	switch {
	case xl < r.W/4:
		return "left"
	case xl >= r.W-r.W/4:
		return "right"
	case yl < max(ui.StripHeight+1, r.H/4):
		return "top"
	case yl >= r.H-r.H/4:
		return "bottom"
	}
	return ""
}

func (m *App) mainPaneDrop(tab window.TabID, x, y int) *paneDrop {
	if m.Win.IndexOf(tab) < 0 {
		return nil
	}
	g := m.dockedPaneAt(x, y)
	if g == nil {
		return nil
	}
	r := g.screen(ui.Rect{W: g.Bounds().W, H: g.Bounds().H})
	d := &paneDrop{tab: tab, group: g.id, rect: g.body}
	// A strip always inserts/reorders, including its outermost columns.
	if y < g.body.Y {
		i, _ := g.tabs.HitTest(x - r.X)
		tabs := m.Win.GroupTabs(g.id)
		if i >= 0 && i < len(tabs) {
			d.beforeTab = tabs[i].ID
		}
		return m.resolvePaneDrop(d)
	}
	// A one-cell rim selects a containing layout; the broader interior edge
	// zones retain ordinary leaf splits. Outermost first makes the complete
	// layout reachable even where several ancestor edges coincide.
	if !m.mainPanes.compact {
		scopes := m.Win.PaneScopes(g.id)
		for i := len(scopes) - 1; i > 0; i-- {
			n := scopes[i]
			scopeRect := paneNodeRect(m.Win.PaneRoot(), n.ID, m.mainPanes.Bounds())
			edge := ""
			switch {
			case x == scopeRect.X:
				edge = "left"
			case x == scopeRect.X+scopeRect.W-1:
				edge = "right"
			case y == scopeRect.Y+ui.StripHeight:
				edge = "top"
			case y == scopeRect.Y+scopeRect.H-1:
				edge = "bottom"
			}
			if edge != "" {
				d.node, d.edge, d.scope = n.ID, edge, m.paneScopeLabel(n, g.id)
				return m.resolvePaneDrop(d)
			}
		}
	}
	// Edges split, middle merges. Preview uses the exact half-cell rounding
	// of SplitModel, including the reserved divider, before committing anything.
	d.edge = paneEdgeZone(r, x-r.X, y-r.Y)
	return m.resolvePaneDrop(d)
}

// fileDropAt proposes where a file dragged from the tree opens: over a tab
// strip or the middle of a pane it opens in that pane; over an edge zone it
// opens in a new split on that side, previewed with the exact split geometry.
// A pane too small to split opens the file in itself from every zone.
func (m *App) fileDropAt(path string, x, y int) *paneDrop {
	g := m.dockedPaneAt(x, y)
	if g == nil {
		return nil
	}
	here := &paneDrop{path: path, group: g.id, rect: g.body}
	r := g.screen(ui.Rect{W: g.Bounds().W, H: g.Bounds().H})
	scopes := m.Win.PaneScopes(g.id)
	if y < g.body.Y || len(scopes) == 0 {
		return here
	}
	edge := paneEdgeZone(r, x-r.X, y-r.Y)
	if edge == "" {
		return here
	}
	dir, before := paneEdge(edge)
	root, target := m.Win.PreviewSplitPane(scopes[0].ID, dir, before)
	bounds := m.mainPanes.Bounds()
	if w, h := paneMinimum(root); target == 0 || w > bounds.W || h > bounds.H {
		return here
	}
	rect := paneGroupRect(root, target, bounds)
	if rect.Empty() {
		return here
	}
	return &paneDrop{path: path, group: g.id, node: scopes[0].ID, edge: edge, rect: rect}
}

// openFileDrop opens a file at a confirmed fileDropAt destination.
func (m *App) openFileDrop(d *paneDrop) tea.Cmd {
	group := d.group
	if d.edge != "" {
		dir, before := paneEdge(d.edge)
		if group = m.Win.SplitPane(d.node, dir, before); group == 0 {
			return nil
		}
	}
	m.focusMainGroup(group)
	cmd, err := m.openFileFromTree(d.path)
	if err != nil {
		m.Feedback.NotifyError("Cannot open " + filepath.Base(d.path) + ": " + err.Error())
	}
	return cmd
}

func (d *paneDrop) placement() window.TabPlacement {
	dir, before := paneEdge(d.edge)
	return window.TabPlacement{Tab: d.tab, Group: d.group, Node: d.node, BeforeTab: d.beforeTab, Split: d.edge != "", Direction: dir, Before: before}
}

func paneEdge(edge string) (ui.Direction, bool) {
	dir := ui.Horizontal
	if edge == "top" || edge == "up" || edge == "bottom" || edge == "down" {
		dir = ui.Vertical
	}
	return dir, edge == "left" || edge == "top" || edge == "up"
}

func (m *App) resolvePaneDrop(d *paneDrop) *paneDrop {
	root, target := m.Win.PreviewTabPlacement(d.placement())
	if target == 0 {
		return nil
	}
	bounds := m.mainPanes.Bounds()
	w, h := paneMinimum(root)
	if w > bounds.W || h > bounds.H {
		return nil
	}
	d.rect = paneGroupRect(root, target, bounds)
	if d.rect.Empty() {
		return nil
	}
	return d
}

// paneDropLive reports whether the gesture that proposed the drop is still
// in progress, so a cancelled or finished drag never leaves a preview behind.
func (m *App) paneDropLive(d *paneDrop) bool {
	p := m.mainPanes
	if d.path != "" {
		state, ok := m.sidebarScreen.DragState()
		return ok && state.Source == d.path && !state.InTree
	}
	for _, g := range p.groups {
		if g.tabs.TabsControl().Dragging() {
			return true
		}
	}
	return false
}

// paintPaneDropPreview tints the area a drop would fill and outlines it. The
// drag ghost at the pointer states what the drop does.
func (m *App) paintPaneDropPreview() {
	p := m.mainPanes
	if p == nil || p.drop == nil || m.mainSurf == nil {
		return
	}
	if p.drop.path == "" && !p.drag.valid(m, p.drop.tab) {
		// The layout changed under the drag: end it rather than retarget.
		m.cancelMainPaneDrag()
		return
	}
	if !m.paneDropLive(p.drop) {
		p.drop = nil
		return
	}
	r := p.drop.rect
	if r.Empty() {
		return
	}
	t := m.rootHost.Tokens()
	bg, edge := t.Color(ui.ColorHover), t.Color(ui.ColorAccent)
	buf := m.mainSurf.Buffer()
	ui.FillRect(buf, 0, 0, r, cell.BlankCell(bg))
	right, bottom := r.X+r.W-1, r.Y+r.H-1
	for x := r.X; x <= right; x++ {
		buf.Set(x, r.Y, cell.NewCell('─', edge, bg, 0))
		buf.Set(x, bottom, cell.NewCell('─', edge, bg, 0))
	}
	for y := r.Y; y <= bottom; y++ {
		buf.Set(r.X, y, cell.NewCell('│', edge, bg, 0))
		buf.Set(right, y, cell.NewCell('│', edge, bg, 0))
	}
	if r.W > 1 && r.H > 1 {
		buf.Set(r.X, r.Y, cell.NewCell('╭', edge, bg, 0))
		buf.Set(right, r.Y, cell.NewCell('╮', edge, bg, 0))
		buf.Set(r.X, bottom, cell.NewCell('╰', edge, bg, 0))
		buf.Set(right, bottom, cell.NewCell('╯', edge, bg, 0))
	}
}

func (m *App) paneMenuSection(tab window.TabID) ui.ContextMenuSection {
	items := []ui.ContextMenuItem{}
	owner := m.paneOwner(tab, m.Win.GroupOf(tab))
	floating := m.Win.IsFloating(m.Win.GroupOf(tab))
	if floating {
		items = append(items, ui.ContextMenuItem{Label: "Move to Main Area", Detail: "dock this tab", Action: owner.action(m, func() {
			m.Win.MoveTabToGroup(tab, m.Win.DockedGroup(), 0)
			m.unfocus()
			m.handleTabSwitch()
			m.syncPaneWindows()
			m.forceFrame()
		})})
		// Several windows can be open at once: a window holding more than one
		// tab can give any of them its own window.
		if m.Win.GroupTabCount(m.Win.GroupOf(tab)) > 1 {
			items = append(items, ui.ContextMenuItem{Label: "Open in New Window", Detail: "separate window", Action: owner.action(m, func() {
				m.openTabInWindow(tab)
			})})
		}
		return ui.ContextMenuSection{SectionLabel: "Window", Items: items}
	}
	items = append(items, ui.ContextMenuItem{Label: "Open in Window", Detail: "movable, resizable", Action: owner.action(m, func() {
		m.openTabInWindow(tab)
	})})
	type paneMenuAction struct{ label, detail, id string }
	var actions []paneMenuAction
	group := m.Win.GroupOf(tab)
	file := m.editorView(tab) != nil
	sides := []struct{ name, edge string }{{"Right", "right"}, {"Left", "left"}, {"Above", "up"}, {"Below", "down"}}
	// The pane on each side, when there is one: open or move the tab into it.
	for _, side := range sides {
		dir, before := paneEdge(side.edge)
		if m.Win.Neighbor(group, dir, before) == 0 {
			continue
		}
		pane := "Pane " + side.name
		if side.edge == "right" || side.edge == "left" {
			pane = side.name + " Pane"
		}
		if file {
			actions = append(actions, paneMenuAction{"Open in " + pane, "same file, its own view", "pane.open-" + side.edge})
		}
		actions = append(actions, paneMenuAction{"Move to " + pane, "", "pane.move-" + side.edge})
	}
	splitDetail := "new empty pane"
	if file {
		splitDetail = "same file, its own view"
	}
	actions = append(actions,
		paneMenuAction{"Split Right", splitDetail, "pane.split-right"}, paneMenuAction{"Split Down", splitDetail, "pane.split-down"},
		paneMenuAction{"Split Left", splitDetail, "pane.split-left"}, paneMenuAction{"Split Up", splitDetail, "pane.split-up"})
	if file {
		actions = append(actions, paneMenuAction{"Split Right with Agent", "last agent, or a new session", "pane.split-agent-right"})
	} else {
		actions = append(actions, paneMenuAction{"Split Right with File", "last file, or choose one", "pane.split-file-right"})
	}
	for _, side := range sides {
		dir, before := paneEdge(side.edge)
		if m.Win.Neighbor(group, dir, before) == 0 {
			actions = append(actions, paneMenuAction{"Move to New Split " + side.name, "", "pane.move-" + side.edge})
		}
	}
	actions = append(actions,
		paneMenuAction{"Arrange Tab…", "", "pane.arrange"}, paneMenuAction{"Split Containing Layout…", "", "pane.split-layout"},
		paneMenuAction{"Move Tab to Next Pane", "", "pane.move-next"}, paneMenuAction{"Move Tab to Previous Pane", "", "pane.move-previous"},
		paneMenuAction{"Merge All Panes", "", "pane.merge-all"})
	for _, a := range actions {
		id := a.id
		items = append(items, ui.ContextMenuItem{Label: a.label, Detail: a.detail, Action: owner.action(m, func() {
			m.Win.SwitchTo(m.Win.IndexOf(tab))
			m.handleTabSwitch()
			cmd, _ := m.handleMainPaneCommand(id)
			m.runDetachedAsyncCmd(cmd)
		})})
	}
	return ui.ContextMenuSection{SectionLabel: "Panes", Items: items}
}

func (m *App) paneScopeLabel(n *window.PaneNode, owner window.GroupID) string {
	ids := m.Win.GroupIDs()
	first, last, count := 0, 0, 0
	for i, id := range ids {
		if paneHasGroup(n, id) {
			if count == 0 {
				first = i + 1
			}
			last, count = i+1, count+1
		}
	}
	if n.Split == nil {
		if n.Group == owner {
			return "This pane"
		}
		label := fmt.Sprintf("Pane %d", first)
		if tab := m.Win.GroupActiveTab(n.Group); tab != nil {
			title := tab.DisplayTitle
			if title == "" {
				title = tab.Title
			}
			label += " · " + rendering.TruncateVisual(title, 24, "…")
		}
		return label
	}
	if n.ID == m.Win.PaneRoot().ID {
		return fmt.Sprintf("Entire layout · %d panes", count)
	}
	return fmt.Sprintf("Containing layout · panes %d–%d", first, last)
}

// Native follow-up menus expose exact topology scopes instead of guessing a
// parent from an axis. Arrange can target any subtree, not only the source path.
func (m *App) openPaneScopeMenu(owner paneMenuOwner, move bool) {
	if !owner.valid(m) {
		return
	}
	menu := &ui.ContextMenuState{Title: "Choose layout scope", Sections: []ui.ContextMenuSection{{SectionLabel: "Choose layout scope"}}}
	var nodes []*window.PaneNode
	if move {
		var visit func(*window.PaneNode)
		visit = func(n *window.PaneNode) {
			nodes = append(nodes, n)
			if n.Split != nil {
				visit(n.First)
				visit(n.Second)
			}
		}
		visit(m.Win.PaneRoot())
	} else {
		nodes = m.Win.PaneScopes(owner.group)
	}
	for _, n := range nodes {
		node, label := n.ID, m.paneScopeLabel(n, owner.group)
		menu.Sections[0].Items = append(menu.Sections[0].Items, ui.ContextMenuItem{Label: label, Action: owner.action(m, func() {
			if m.Win.Pane(node) != nil {
				m.openPaneEdgeMenu(owner, node, label, move)
			}
		})})
	}
	m.openContextMenuState(tea.Mouse{X: m.mainPanes.Bounds().X + 3, Y: m.mainPanes.Bounds().Y + 2}, menu)
}

func (m *App) openPaneEdgeMenu(owner paneMenuOwner, node uint64, label string, move bool) {
	menu := &ui.ContextMenuState{Title: label, Sections: []ui.ContextMenuSection{{SectionLabel: label}}}
	if n := m.Win.Pane(node); move && n != nil && n.Split == nil {
		group := n.Group
		menu.Sections[0].Items = append(menu.Sections[0].Items, ui.ContextMenuItem{Label: "Move into this pane", Action: owner.action(m, func() {
			if m.Win.Pane(node) != nil && m.Win.MoveTabToGroup(owner.tab, group, 0) {
				m.focusMainGroup(group)
				m.syncPaneWindows()
			}
		})})
	}
	for _, edge := range []struct{ label, edge string }{{"Left", "left"}, {"Right", "right"}, {"Above", "top"}, {"Below", "bottom"}} {
		dir, before := paneEdge(edge.edge)
		tab := window.TabID(0)
		verb := "Split "
		if move {
			tab, verb = owner.tab, "Move tab "
		}
		menu.Sections[0].Items = append(menu.Sections[0].Items, ui.ContextMenuItem{Label: verb + edge.label, Action: owner.action(m, func() {
			m.splitMainPane(node, dir, before, tab)
		})})
	}
	m.openContextMenuState(tea.Mouse{X: m.mainPanes.Bounds().X + 3, Y: m.mainPanes.Bounds().Y + 2}, menu)
}

func (m *App) mainPaneSummary() string {
	ids := m.Win.GroupIDs()
	if len(ids) < 2 {
		return ""
	}
	for i, id := range ids {
		if id == m.Win.ActiveGroup() {
			label := fmt.Sprintf("Pane %d/%d", i+1, len(ids))
			if m.mainPanes != nil && m.mainPanes.compact {
				label += " · compact"
			}
			return label
		}
	}
	return ""
}
