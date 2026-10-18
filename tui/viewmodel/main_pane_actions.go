package viewmodel

import (
	"fmt"
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

type paneDrop struct {
	tab       window.TabID
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
		{"Split Above", "pane.split-up"}, {"Split Down", "pane.split-down"},
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
	var paths []string
	seen := !rightOnly
	for _, tab := range tabs {
		if tab.ID == owner.tab {
			seen = true
			continue
		}
		if seen && m.isEditorTabTitle(tab.Title) {
			paths = append(paths, m.editorPathFromTabTitle(tab.Title))
		}
	}
	m.releaseTabBarFocus()
	m.closeEditorTabs(paths)
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

func (m *App) splitMainGroup(dir ui.Direction, before bool, tab window.TabID) {
	scopes := m.Win.PaneScopes(m.Win.ActiveGroup())
	if len(scopes) != 0 {
		m.splitMainPane(scopes[0].ID, dir, before, tab)
	}
}

func (m *App) splitMainPane(node uint64, dir ui.Direction, before bool, tab window.TabID) {
	p := window.TabPlacement{Tab: tab, Node: node, Split: true, Direction: dir, Before: before}
	var root *window.PaneNode
	var target window.GroupID
	if tab == 0 {
		root, target = m.Win.PreviewSplitPane(node, dir, before)
	} else {
		root, target = m.Win.PreviewTabPlacement(p)
	}
	if target == 0 {
		return
	}
	w, h := paneMinimum(root)
	bounds := m.ensureMainPanes().Bounds()
	if w > bounds.W || h > bounds.H {
		m.Feedback.NotifyInfo("Not enough room for this layout; enlarge the terminal or merge a pane")
		return
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
	if !strings.HasPrefix(id, "pane.") {
		return nil, false
	}
	// Pane commands do not bypass a modal decision or the terminal's keys.
	if m.focusedPopup() != nil {
		return nil, true
	}
	if m.sessionDeckBlocking() && !m.sessionDeckAllowsNavigation() {
		return nil, true
	}
	switch id {
	case "pane.split-left", "pane.split-right", "pane.split-up", "pane.split-down",
		"pane.move-left", "pane.move-right", "pane.move-up", "pane.move-down":
		dir, before := paneEdge(strings.TrimPrefix(strings.TrimPrefix(id, "pane.split-"), "pane.move-"))
		tab := window.TabID(0)
		if strings.HasPrefix(id, "pane.move-") {
			if active := m.Win.ActiveTab(); active != nil {
				tab = active.ID
			} else {
				return nil, true
			}
		}
		m.splitMainGroup(dir, before, tab)
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
			p.drop = m.mainPaneDrop(window.TabID(id), gesture.X, gesture.Y)
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

func (m *App) mainPaneDrop(tab window.TabID, x, y int) *paneDrop {
	if m.Win.IndexOf(tab) < 0 {
		return nil
	}
	if m.windowManager != nil {
		for _, w := range m.windowManager.Windows() {
			if w.Visible() && w.ContainsPoint(x, y) {
				return nil
			}
		}
	}
	g := m.paneAt(x, y)
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
	xl, yl := x-r.X, y-r.Y
	switch {
	case xl < r.W/4:
		d.edge = "left"
	case xl >= r.W-r.W/4:
		d.edge = "right"
	case yl < max(ui.StripHeight+1, r.H/4):
		d.edge = "top"
	case yl >= r.H-r.H/4:
		d.edge = "bottom"
	}
	if d.edge == "" {
		return m.resolvePaneDrop(d)
	}
	return m.resolvePaneDrop(d)
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

func (m *App) paintPaneDropPreview() {
	p := m.mainPanes
	if p == nil || p.drop == nil || m.mainSurf == nil {
		return
	}
	if !p.drag.valid(m, p.drop.tab) {
		m.cancelMainPaneDrag()
		return
	}
	armed := false
	for _, g := range p.groups {
		armed = armed || g.tabs.TabsControl().Dragging()
	}
	if !armed {
		p.drop = nil
		return
	}
	r := p.drop.rect
	if r.Empty() {
		return
	}
	t := m.rootHost.Tokens()
	bg, fg := t.Color(ui.ColorHover), t.Color(ui.ColorMuted)
	buf := m.mainSurf.Buffer()
	ui.FillRect(buf, 0, 0, r, cell.BlankCell(bg))
	label := "Move tab here"
	if p.drop.edge != "" {
		label = "Split " + p.drop.edge
		if p.drop.scope != "" {
			label += " · " + p.drop.scope
		}
	}
	label = rendering.TruncateVisual(label, r.W, "")
	buf.WriteString(r.X+max(0, (r.W-rendering.VisibleWidth(label))/2), r.Y+r.H/2, label, fg, bg, 0)
	for x := r.X; x < r.X+r.W; x++ {
		buf.Set(x, r.Y, cell.NewCell('─', t.Color(ui.ColorBorder), bg, 0))
		if r.H > 1 {
			buf.Set(x, r.Y+r.H-1, cell.NewCell('─', t.Color(ui.ColorBorder), bg, 0))
		}
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
	for _, a := range []struct{ label, id string }{
		{"Split Left", "pane.split-left"}, {"Split Right", "pane.split-right"},
		{"Split Above", "pane.split-up"}, {"Split Down", "pane.split-down"},
		{"Move Tab to Left Split", "pane.move-left"}, {"Move Tab to Upper Split", "pane.move-up"},
		{"Move Tab to Right Split", "pane.move-right"}, {"Move Tab to Lower Split", "pane.move-down"},
		{"Arrange Tab…", "pane.arrange"}, {"Split Containing Layout…", "pane.split-layout"},
		{"Move Tab to Next Pane", "pane.move-next"}, {"Move Tab to Previous Pane", "pane.move-previous"},
		{"Merge All Panes", "pane.merge-all"},
	} {
		id := a.id
		items = append(items, ui.ContextMenuItem{Label: a.label, Action: owner.action(m, func() {
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
