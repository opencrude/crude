package viewmodel

import (
	"fmt"
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
	beforeTab window.TabID
	edge      string
	rect      ui.Rect
}

type paneFilePickerOwner struct {
	group      window.GroupID
	generation uint64
}

func (m *App) emptyPaneActive() bool {
	return m.Win != nil && m.Win.ActiveTab() == nil && (m.Win.TabCount() > 0 || len(m.Win.GroupIDs()) > 1)
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
	menu := &ui.ContextMenuState{Title: "Pane"}
	for _, action := range []struct{ label, id string }{
		{"New Session", "pane.new-session"}, {"Open File", "pane.open-file"},
		{"Split Right", "pane.split-right"}, {"Split Down", "pane.split-down"},
		{"Close Empty Pane", "pane.close"}, {"Merge All Panes", "pane.merge-all"},
	} {
		id := action.id
		menu.Sections = append(menu.Sections, ui.ContextMenuSection{Items: []ui.ContextMenuItem{{Label: action.label, Action: func() {
			if !paneHasGroup(m.Win.PaneRoot(), group) {
				return
			}
			m.focusMainGroup(group)
			cmd, _ := m.handleMainPaneCommand(id)
			m.runDetachedAsyncCmd(cmd)
		}}}})
	}
	return menu
}

// openMainPaneMenu keeps compacted groups reachable by mouse as well as keys.
func (m *App) openMainPaneMenu() tea.Cmd {
	menu := &ui.ContextMenuState{Title: "Panes"}
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
		menu.Sections = append(menu.Sections, ui.ContextMenuSection{Items: []ui.ContextMenuItem{{Label: label, Action: func() { m.focusMainGroup(id) }}}})
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
	g := m.ensureMainPanes().group(m.Win.ActiveGroup())
	r := g.Bounds()
	if (dir == ui.Horizontal && r.W < 49) || (dir == ui.Vertical && r.H < 15) {
		m.Feedback.NotifyInfo("Not enough room to split this pane; enlarge the terminal or merge a pane")
		return
	}
	group := m.Win.SplitGroup(g.id, dir, before)
	if group == 0 {
		return
	}
	if tab != 0 {
		m.Win.MoveTabToGroup(tab, group, 0)
	}
	m.focusMainGroup(group)
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
	if m.windowManager != nil && m.windowManager.Focused() != nil {
		return nil, true
	}
	if m.sessionDeckBlocking() && !m.sessionDeckAllowsNavigation() {
		return nil, true
	}
	switch id {
	case "pane.split-right":
		m.splitMainGroup(ui.Horizontal, false, 0)
	case "pane.split-down":
		m.splitMainGroup(ui.Vertical, false, 0)
	case "pane.move-right", "pane.move-down":
		if tab := m.Win.ActiveTab(); tab != nil {
			dir := ui.Horizontal
			if id == "pane.move-down" {
				dir = ui.Vertical
			}
			m.splitMainGroup(dir, false, tab.ID)
		}
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
	m.openPicker(m.RootPath, filepickerscreen.ModeOpenFile)
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
		if !paneHasGroup(m.Win.PaneRoot(), group) {
			return nil, true
		}
		m.focusMainGroup(group)
		if a.ID == "pane.open-file" {
			m.openPaneFilePicker(group)
			return nil, true
		}
		return m.launcherNewSession(), true
	case "tabs.activate", "tabs.close", "tabs.enter":
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
		drop := m.mainPaneDrop(window.TabID(id), gesture.X, gesture.Y)
		if a.ID == "tabs.drag" {
			p.drop = drop
			return nil, true
		}
		p.drop = nil
		if drop == nil {
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
	r := m.mainPanes.screenRect(g.Bounds(), ui.Rect{W: g.Bounds().W, H: g.Bounds().H})
	d := &paneDrop{tab: tab, group: g.id, rect: g.body}
	if y < g.body.Y {
		i, _ := g.tabs.HitTest(x - r.X)
		tabs := m.Win.GroupTabs(g.id)
		if i >= 0 && i < len(tabs) {
			d.beforeTab = tabs[i].ID
		}
		return m.resolvePaneDrop(d)
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
	if m.Win.GroupOf(tab) == g.id && len(m.Win.GroupTabs(g.id)) == 1 {
		return nil
	}
	dir := ui.Horizontal
	total := r.W
	if d.edge == "top" || d.edge == "bottom" {
		dir = ui.Vertical
		total = r.H
	}
	minimum := 49
	if dir == ui.Vertical {
		minimum = 15
	}
	if total < minimum {
		return nil
	}
	return m.resolvePaneDrop(d)
}

func (d *paneDrop) placement() window.TabPlacement {
	dir := ui.Horizontal
	if d.edge == "top" || d.edge == "bottom" {
		dir = ui.Vertical
	}
	return window.TabPlacement{Tab: d.tab, Group: d.group, BeforeTab: d.beforeTab, Split: d.edge != "", Direction: dir, Before: d.edge == "left" || d.edge == "top"}
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
	for _, a := range []struct{ label, id string }{
		{"Split Right", "pane.split-right"}, {"Split Down", "pane.split-down"},
		{"Move Tab to Right Split", "pane.move-right"}, {"Move Tab to Lower Split", "pane.move-down"},
		{"Move Tab to Next Pane", "pane.move-next"}, {"Merge All Panes", "pane.merge-all"},
	} {
		id := a.id
		items = append(items, ui.ContextMenuItem{Label: a.label, Action: func() {
			if index := m.Win.IndexOf(tab); index >= 0 {
				m.Win.SwitchTo(index)
				m.handleTabSwitch()
			}
			cmd, _ := m.handleMainPaneCommand(id)
			m.runDetachedAsyncCmd(cmd)
		}})
	}
	return ui.ContextMenuSection{SectionLabel: "Panes", Items: items}
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
