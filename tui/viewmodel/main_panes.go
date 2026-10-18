package viewmodel

import (
	"fmt"

	"crude/tui/cell"
	"crude/tui/input"
	"crude/tui/rendering"
	"crude/tui/screens/shellchat"
	"crude/tui/screens/shelleditor"
	"crude/tui/screens/shellinput"
	"crude/tui/screens/shelltabs"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/workspace"
)

const mainPaneID ui.ComponentID = "main-panes"

// Window owns topology, membership and selection. This tree retains only the
// corresponding controls and geometry; changing focus never clones a resource.
type mainPaneLayout struct {
	ui.Base
	app             *App
	groups          map[window.GroupID]*mainPaneGroup
	views           map[window.TabID]*mainPaneView
	agentViews      map[string]*mainPaneView
	dividers        map[uint64]*ui.SplitDivider
	children        []ui.Component
	placements      map[window.GroupID]ui.Rect
	dividerRects    map[uint64]ui.Rect
	dividerFirst    map[uint64]int
	drop            *paneDrop
	drag            *paneDrag
	windows         map[window.GroupID]*paneWindow
	compact         bool
	initialChatUsed bool
}

type panePart struct {
	component ui.Component
	rect      ui.Rect
}

type mainPaneGroup struct {
	ui.Base
	id                        window.GroupID
	tabs                      *shelltabs.Screen
	empty                     *emptyMainPane
	parts                     []panePart
	children                  []ui.Component
	body                      ui.Rect // screen coordinates, shared by drop preview and input policy
	screenX, screenY          int     // screen origin of the group's bounds
	chat, input, editor, deck ui.Rect
	view                      *mainPaneView
}

type mainPaneView struct {
	embedded                     *subPaneProjectionRegistry
	chat                         *shellchat.Screen
	input                        *shellinput.Screen
	editor                       *shelleditor.Screen
	deck                         *deckMount
	reflowContentW, reflowInnerW int
}

func (m *App) ensureMainPanes() *mainPaneLayout {
	if m.mainPanes == nil {
		m.mainPanes = &mainPaneLayout{
			Base: ui.NewBase(mainPaneID), app: m,
			groups:       make(map[window.GroupID]*mainPaneGroup),
			views:        make(map[window.TabID]*mainPaneView),
			agentViews:   make(map[string]*mainPaneView),
			dividers:     make(map[uint64]*ui.SplitDivider),
			placements:   make(map[window.GroupID]ui.Rect),
			dividerRects: make(map[uint64]ui.Rect),
			dividerFirst: make(map[uint64]int),
		}
	}
	return m.mainPanes
}

func (p *mainPaneLayout) group(id window.GroupID) *mainPaneGroup {
	if g := p.groups[id]; g != nil {
		return g
	}
	base := ui.ComponentID(fmt.Sprintf("main-panes/group-%d", id))
	tabs := shelltabs.NewWithID(p.app.Theme, ui.NewID(base, "tabs"))
	if len(p.groups) == 0 {
		tabs = p.app.tabScreen
	}
	tabs.SetNewButton(true)
	tabs.SetDraggable(true)
	g := &mainPaneGroup{Base: ui.NewBase(base), id: id, tabs: tabs, empty: newEmptyMainPane(ui.NewID(base, "empty"), id)}
	p.groups[id] = g
	return g
}

func (p *mainPaneLayout) view(tab *window.Tab) *mainPaneView {
	if tab == nil {
		return nil
	}
	if v := p.views[tab.ID]; v != nil {
		return v
	}
	base := ui.ComponentID(fmt.Sprintf("main-panes/view-%d", tab.ID))
	v := &mainPaneView{}
	if p.app.isAgentTabTitle(tab.Title) {
		agentID := p.app.agentIDFromTabTitle(tab.Title)
		if retained := p.agentViews[agentID]; retained != nil {
			p.views[tab.ID] = retained
			return retained
		}
		v.embedded = p.app.subPaneProjections.ForOwner(agentID)
		if proj := p.app.subPaneProjections.GetExisting(agentID); proj != nil {
			proj.renderer.SetSubPaneContentFn(p.app.subPaneContentForRegistry(v.embedded))
		}
		first := !p.initialChatUsed
		p.initialChatUsed = true
		if first {
			v.chat, v.input = p.app.chatScreen, p.app.inputScreen
		} else {
			v.chat = shellchat.NewWithID(p.app.Theme, ui.NewID(base, "chat"))
			v.input = shellinput.NewWithID(p.app.Theme, ui.NewID(base, "input"))
		}
		v.deck = newDeckMount(p.app)
		v.deck.Base = ui.NewBase(ui.NewID(base, "deck"))
		_, v.deck.projection = p.app.sessionProjectionForAgent(p.app.agentIDFromTabTitle(tab.Title))
		if node := p.app.ws.AgentMgr().Get(agentID); node != nil && node.State != nil && node.State.Completed {
			v.chat.ScrollModel().SetAutoScroll(false)
			v.chat.ScrollModel().ScrollTo(0)
		}
		p.agentViews[agentID] = v
	} else if p.app.isEditorTabTitle(tab.Title) {
		first := true
		for _, old := range p.views {
			if old.editor != nil {
				first = false
				break
			}
		}
		if first {
			v.editor = p.app.editorScreen
		} else {
			v.editor = shelleditor.NewWithID(p.app.Theme, ui.NewID(base, "editor"))
		}
		v.editor.Bind(p.app.editorForWindowTab(tab.Title))
	}
	p.views[tab.ID] = v
	return v
}

// bindMainPaneFocus publishes aliases for existing keyboard/action consumers.
// No active session or editor is ever changed while preparing other panes.
func (m *App) bindMainPaneFocus() {
	if m.Win == nil {
		return
	}
	p := m.ensureMainPanes()
	g := p.group(m.Win.ActiveGroup())
	m.tabScreen = g.tabs
	if v := p.view(m.Win.ActiveTab()); v != nil {
		if v.chat != nil {
			m.chatScreen, m.inputScreen = v.chat, v.input
		}
		if v.editor != nil {
			m.editorScreen = v.editor
		}
	}
}

func (p *mainPaneLayout) resolve(r ui.Rect) {
	p.SetBounds(r)
	clear(p.placements)
	clear(p.dividerRects)
	root := p.app.Win.PaneRoot()
	w, h := paneMinimum(root)
	p.compact = (r.W < w || r.H < h) && len(p.app.Win.GroupIDs()) > 1
	if p.compact {
		p.placements[p.app.Win.ActiveGroup()] = ui.Rect{W: r.W, H: r.H}
		return
	}
	var walk func(*window.PaneNode, ui.Rect)
	walk = func(n *window.PaneNode, rect ui.Rect) {
		if n == nil {
			return
		}
		if n.Split == nil {
			p.placements[n.Group] = rect
			return
		}
		a, b, d, first := paneSplitGeometry(n, rect)
		divider := p.dividers[n.ID]
		if divider == nil {
			divider = ui.NewSplitDivider(ui.ComponentID(fmt.Sprintf("main-panes/divider-%d", n.ID)), n.Split)
			p.dividers[n.ID] = divider
		}
		divider.SetTokens(p.app.rootHost.Tokens())
		divider.SetCurrentFirst(first)
		p.dividerFirst[n.ID] = first
		p.dividerRects[n.ID] = d
		walk(n.First, a)
		walk(n.Second, b)
	}
	walk(root, ui.Rect{W: r.W, H: r.H})
}

// paneSplitGeometry is shared by actual and prospective layouts, so source
// collapse, nested minimums and rounding cannot disagree with the preview.
func paneSplitGeometry(n *window.PaneNode, rect ui.Rect) (a, b, d ui.Rect, first int) {
	fw, fh := paneMinimum(n.First)
	sw, sh := paneMinimum(n.Second)
	total, minFirst, minSecond := rect.W, fw, sw
	if n.Split.Dir() == ui.Vertical {
		total, minFirst, minSecond = rect.H, fh, sh
	}
	n.Split.SetMinSizes(minFirst, minSecond)
	first, gap, second := n.Split.Resolve(total)
	a, b, d = rect, rect, rect
	if n.Split.Dir() == ui.Horizontal {
		a.W = first
		d.X, d.W = rect.X+first, gap
		b.X, b.W = d.X+gap, second
	} else {
		a.H = first
		d.Y, d.H = rect.Y+first, gap
		b.Y, b.H = d.Y+gap, second
	}
	return
}

func paneGroupRect(n *window.PaneNode, group window.GroupID, r ui.Rect) ui.Rect {
	if n == nil {
		return ui.Rect{}
	}
	if n.Split == nil {
		if n.Group == group {
			return r
		}
		return ui.Rect{}
	}
	a, b, _, _ := paneSplitGeometry(n, r)
	if found := paneGroupRect(n.First, group, a); !found.Empty() {
		return found
	}
	return paneGroupRect(n.Second, group, b)
}

func paneNodeRect(n *window.PaneNode, id uint64, r ui.Rect) ui.Rect {
	if n == nil {
		return ui.Rect{}
	}
	if n.ID == id {
		return r
	}
	if n.Split == nil {
		return ui.Rect{}
	}
	a, b, _, _ := paneSplitGeometry(n, r)
	if found := paneNodeRect(n.First, id, a); !found.Empty() {
		return found
	}
	return paneNodeRect(n.Second, id, b)
}

func paneMinimum(n *window.PaneNode) (int, int) {
	if n == nil || n.Split == nil {
		return 24, 7
	}
	aw, ah := paneMinimum(n.First)
	bw, bh := paneMinimum(n.Second)
	if n.Split.Dir() == ui.Horizontal {
		return aw + bw + 1, max(ah, bh)
	}
	return max(aw, bw), ah + bh + 1
}

func (p *mainPaneLayout) configure() {
	m := p.app
	p.children = p.children[:0]
	for _, id := range m.Win.GroupIDs() {
		g := p.group(id)
		r, visible := p.placements[id]
		if !visible {
			continue
		}
		p.configureGroup(g, r, p.Bounds().X+r.X, p.Bounds().Y+r.Y)
		p.children = append(p.children, g)
	}
	for id, rect := range p.dividerRects {
		d := p.dividers[id]
		d.Layout(ui.LayoutContext{}, rect)
		p.children = append(p.children, d)
	}
	// A close must release retained rendering references, not retain documents
	// through an unreachable pane cache until the workspace next switches.
	for id := range p.views {
		if m.Win.IndexOf(id) < 0 {
			delete(p.views, id)
		}
	}
	for id := range p.groups {
		if !m.Win.HasGroup(id) {
			delete(p.groups, id)
		}
	}
	for id := range p.dividers {
		if !paneHasNode(m.Win.PaneRoot(), id) {
			delete(p.dividers, id)
			delete(p.dividerFirst, id)
		}
	}
}

// configureGroup binds one group's tab strip and active content. r is the
// group's parent-local bounds; (x, y) is its screen origin, which docked
// groups take from the split layout and window groups from their window.
func (p *mainPaneLayout) configureGroup(g *mainPaneGroup, r ui.Rect, x, y int) {
	m, id := p.app, g.id
	g.screenX, g.screenY = x, y
	g.SetBounds(r)
	g.parts, g.children = g.parts[:0], g.children[:0]
	g.chat, g.input, g.editor, g.deck = ui.Rect{}, ui.Rect{}, ui.Rect{}, ui.Rect{}
	tab := m.Win.GroupActiveTab(id)
	activeID := window.TabID(0)
	if tab != nil {
		activeID = tab.ID
	}
	g.tabs.SetProjectionFromTabs(m.Win.GroupTabs(id), activeID, id == m.Win.ActiveGroup() && m.focusTarget() == FocusTabBar)
	if m.rootHost.Hovered() != g.tabs.TabsControl().ID() {
		g.tabs.TabsControl().SyncHover(-1)
	}
	g.tabs.SetTokens(m.rootHost.Tokens())
	tabH := 0
	// A window's title row already names its tab; a strip appears only once
	// the window holds several tabs to choose between.
	stripVisible := m.tabBarVisible()
	if m.Win.IsFloating(id) {
		stripVisible = m.Win.GroupTabCount(id) > 1 && m.Win.Layout() == window.TabHorizontal
	}
	if stripVisible {
		tabH = min(ui.StripHeight, r.H)
		g.add(g.tabs, ui.Rect{W: r.W, H: tabH})
	}
	body := ui.Rect{Y: tabH, W: r.W, H: max(0, r.H-tabH)}
	g.body = g.screen(body)
	g.view = p.view(tab)
	switch {
	case tab == nil && !m.Win.IsFloating(id) && m.mainAreaEmpty() && m.launcherScreen != nil:
		pad, inner := boundedContentGeometry(body.W, m.Theme.Spacing.PadLR)
		if inner >= 20 && body.H >= 3 {
			g.add(m.launcherScreen, ui.Rect{X: pad, Y: body.Y, W: inner, H: body.H})
		}
	case tab == nil:
		g.empty.SetTokens(m.rootHost.Tokens())
		g.empty.compact = p.compact
		g.add(g.empty, body)
	case tab.Resource.Kind == window.ResourcePanel:
		if content := m.termDock.Panel(tab.Resource.ID); content != nil && m.placementOf(tab.Resource.ID) == placeTab {
			g.add(content, body)
		}
	case m.isEditorTabTitle(tab.Title):
		ed := m.editorForWindowTab(tab.Title)
		g.view.editor.Bind(ed)
		pad := min(m.Theme.Spacing.PadLR, body.W/2)
		left := pad
		if m.sidebarHostVisible() || len(m.Win.GroupIDs()) > 1 || m.Win.IsFloating(id) {
			left = 0
		}
		g.view.editor.SetPadding(left, pad)
		g.view.editor.SetFocused(id == m.Win.ActiveGroup() && m.focusTarget() == FocusEditor)
		g.add(g.view.editor, body)
		g.editor = g.body
	case m.isAgentTabTitle(tab.Title):
		p.configureAgent(g, tab, body)
	case tab.Resource.Kind == window.ResourceGitReview:
		if view := m.gitReviews[tab.ID]; view != nil {
			g.add(view.screen, body)
		}
	case tab.Resource.Kind == window.ResourceLocalDiff:
		if view := m.localDiffs[tab.ID]; view != nil {
			g.add(view.screen, body)
		}
	}
}

func paneHasNode(n *window.PaneNode, id uint64) bool {
	return n != nil && (n.ID == id || paneHasNode(n.First, id) || paneHasNode(n.Second, id))
}

func paneHasGroup(n *window.PaneNode, id window.GroupID) bool {
	return n != nil && (n.Group == id || paneHasGroup(n.First, id) || paneHasGroup(n.Second, id))
}

func (p *mainPaneLayout) configureAgent(g *mainPaneGroup, tab *window.Tab, body ui.Rect) {
	m, v := p.app, g.view
	id := m.agentIDFromTabTitle(tab.Title)
	node := m.ws.AgentMgr().Get(id)
	if node == nil {
		return
	}
	sess, proj := m.sessionProjectionForAgent(id)
	v.deck.projection = proj
	model := m.paneComposer(sess)
	inputH := 0
	if (node.State == nil || !node.State.Completed) && model != nil && (sess == nil || p.ownsSessionComposer(g.id, sess)) {
		inputH = min(model.ShellHeight(body.W), max(0, body.H/3))
	}
	deckH := 0
	// Session decks have one exact owner even when root and child are both
	// visible. Prefer the focused family member, otherwise its first pane.
	if proj != nil && proj.deck.IsOpen() && p.ownsSessionChrome(g.id, sess) {
		deckH = proj.deck.PreferredHeight(max(0, body.H-inputH-1))
	}
	chatH := max(0, body.H-inputH-deckH)
	chatRect := ui.Rect{Y: body.Y, W: body.W, H: chatH}
	g.chat = g.screen(chatRect)
	g.add(v.chat, chatRect)
	if deckH > 0 {
		r := ui.Rect{Y: body.Y + chatH, W: body.W, H: deckH}
		g.add(v.deck, r)
		g.deck = g.screen(r)
	}
	if inputH > 0 {
		model.SetSpinFrame(m.SpinFrame)
		v.input.Bind(model)
		v.input.SetWidth(body.W)
		r := ui.Rect{Y: body.Y + chatH + deckH, W: body.W, H: inputH}
		g.add(v.input, r)
		g.input = g.screen(r)
	}
}

func (p *mainPaneLayout) ownsSessionChrome(group window.GroupID, sess *workspace.Session) bool {
	if sess == nil {
		return false
	}
	m := p.app
	if active := m.Win.ActiveTab(); active != nil && m.isAgentTabTitle(active.Title) {
		owner, _ := m.sessionProjectionForAgent(m.agentIDFromTabTitle(active.Title))
		if owner == sess {
			return group == m.Win.ActiveGroup()
		}
	}
	for _, id := range m.Win.AllGroupIDs() {
		tab := m.Win.GroupActiveTab(id)
		if tab == nil || !m.isAgentTabTitle(tab.Title) {
			continue
		}
		owner, _ := m.sessionProjectionForAgent(m.agentIDFromTabTitle(tab.Title))
		if owner == sess {
			return group == id
		}
	}
	return false
}

func (p *mainPaneLayout) ownsSessionComposer(group window.GroupID, sess *workspace.Session) bool {
	m := p.app
	eligible := func(id window.GroupID) bool {
		tab := m.Win.GroupActiveTab(id)
		if tab == nil || !m.isAgentTabTitle(tab.Title) {
			return false
		}
		agent := m.agentIDFromTabTitle(tab.Title)
		node := m.ws.AgentMgr().Get(agent)
		if node == nil || node.State != nil && node.State.Completed {
			return false
		}
		owner, _ := m.sessionProjectionForAgent(agent)
		return owner == sess
	}
	if eligible(m.Win.ActiveGroup()) {
		return group == m.Win.ActiveGroup()
	}
	for _, id := range m.Win.AllGroupIDs() {
		if eligible(id) {
			return group == id
		}
	}
	return false
}

func (p *mainPaneLayout) configureMessages() {
	for _, id := range p.app.Win.AllGroupIDs() {
		if _, docked := p.placements[id]; !docked && p.windows[id] == nil {
			continue
		}
		g := p.groups[id]
		if g != nil && g.view != nil && g.view.chat != nil && !g.chat.Empty() {
			tab := p.app.Win.GroupActiveTab(id)
			if tab != nil {
				p.app.configurePaneChat(p.app.agentIDFromTabTitle(tab.Title), g.view, g.chat)
			}
		}
	}
}

func (p *mainPaneLayout) SetTokens(t *ui.TokenSet) {
	// Include unmounted retained tabs. Reopening a warm view must never revive
	// old colors, while geometry/scroll remain owned by that same view.
	for _, g := range p.groups {
		g.tabs.SetTheme(p.app.Theme)
		g.tabs.SetTokens(t)
		g.empty.SetTokens(t)
	}
	for _, v := range p.views {
		if v.chat != nil {
			v.chat.SetTheme(p.app.Theme)
			v.chat.SetTokens(t)
		}
		if v.input != nil {
			v.input.SetTheme(p.app.Theme)
			v.input.SetTokens(t)
		}
		if v.editor != nil {
			v.editor.SetTheme(p.app.Theme)
			v.editor.SetTokens(t)
		}
	}
	for _, v := range p.agentViews {
		v.chat.SetTheme(p.app.Theme)
		v.input.SetTheme(p.app.Theme)
	}
}

func (m *App) paneComposer(sess *workspace.Session) *input.Model {
	if sess == nil || sess == m.composerHydratedFor {
		return &m.Input
	}
	if owner := m.composerDraftOwners[sess]; owner != nil {
		return owner.input
	}
	return nil
}

func (g *mainPaneGroup) add(c ui.Component, r ui.Rect) {
	if c == nil || r.Empty() {
		return
	}
	g.parts = append(g.parts, panePart{c, r})
	g.children = append(g.children, c)
}

// screen converts a group-local rect to screen coordinates.
func (g *mainPaneGroup) screen(child ui.Rect) ui.Rect {
	child.X += g.screenX
	child.Y += g.screenY
	return child
}
func (p *mainPaneLayout) Children() []ui.Component { return p.children }
func (p *mainPaneLayout) Measure(_ ui.LayoutContext, c ui.Constraints) ui.Size {
	return c.ClampSize(ui.Size{W: c.MaxW, H: c.MaxH})
}
func (p *mainPaneLayout) Layout(ctx ui.LayoutContext, r ui.Rect) {
	p.SetBounds(r)
	for _, c := range p.children {
		c.Layout(ctx, c.Bounds())
	}
}
func (*mainPaneLayout) Paint(*surface.CellBuffer, int, int, ui.PaintContext) {}
func (*mainPaneLayout) Handle(ui.EventContext, ui.Event) ui.EventResult      { return ui.EventResult{} }
func (*mainPaneLayout) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Main view panes"}
}
func (g *mainPaneGroup) Children() []ui.Component { return g.children }
func (g *mainPaneGroup) Measure(_ ui.LayoutContext, c ui.Constraints) ui.Size {
	return c.ClampSize(ui.Size{W: c.MaxW, H: c.MaxH})
}
func (g *mainPaneGroup) Layout(ctx ui.LayoutContext, r ui.Rect) {
	g.SetBounds(r)
	for _, part := range g.parts {
		part.component.Layout(ctx, part.rect)
	}
}
func (*mainPaneGroup) Paint(*surface.CellBuffer, int, int, ui.PaintContext) {}
func (*mainPaneGroup) Handle(ui.EventContext, ui.Event) ui.EventResult      { return ui.EventResult{} }
func (g *mainPaneGroup) HandleAction(_ ui.EventContext, a ui.Action) ui.EventResult {
	if a.ID == "tabs.new" {
		a.Payload = g.id
	}
	return ui.EventResult{Handled: true, Action: &a}
}
func (*mainPaneGroup) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Tab group"}
}

type emptyMainPane struct {
	ui.Base
	group     window.GroupID
	tokens    *ui.TokenSet
	selection int
	hover     int
	compact   bool
	buttons   [2]*ui.Button
}

func newEmptyMainPane(id ui.ComponentID, group window.GroupID) *emptyMainPane {
	e := &emptyMainPane{Base: ui.NewBase(id), group: group, hover: -1}
	e.buttons[0] = ui.NewButton(ui.NewID(id, "session"), "New Session", "pane.new-session")
	e.buttons[1] = ui.NewButton(ui.NewID(id, "file"), "Open File", "pane.open-file")
	return e
}
func (e *emptyMainPane) SetTokens(t *ui.TokenSet) {
	e.tokens = t
	for _, b := range e.buttons {
		b.SetTokens(t)
		b.SetVariant(ui.VariantSecondary)
		b.SetSurfaceBackground(t.Color(ui.ColorBg))
	}
}
func (e *emptyMainPane) Measure(_ ui.LayoutContext, c ui.Constraints) ui.Size {
	return c.ClampSize(ui.Size{W: c.MaxW, H: c.MaxH})
}
func (e *emptyMainPane) Layout(ctx ui.LayoutContext, r ui.Rect) {
	e.SetBounds(r)
	y := max(1, (r.H-5)/2)
	for i, b := range e.buttons {
		b.Layout(ctx, ui.Rect{X: max(0, (r.W-19)/2), Y: min(max(0, r.H-1), y+1+i), W: min(19, r.W), H: 1})
	}
}
func (e *emptyMainPane) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	r := e.Bounds()
	if r.Empty() || e.tokens == nil {
		return
	}
	bg := e.tokens.Color(ui.ColorBg)
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	title := "Empty pane"
	if e.compact {
		title = "Empty pane · compact layout"
	}
	title = rendering.TruncateVisual(title, r.W, "")
	y := max(0, (r.H-5)/2)
	buf.WriteString(ox+r.X+max(0, (r.W-rendering.VisibleWidth(title))/2), oy+r.Y+y, title, e.tokens.Color(ui.ColorMuted), bg, 0)
	for i, b := range e.buttons {
		c := ctx
		c.Focused = ctx.Focused && i == e.selection
		c.Hovered = ctx.Hovered && e.hover == i
		b.Paint(buf, ox+r.X, oy+r.Y, c)
	}
	if r.H >= 8 {
		hint := rendering.TruncateVisual("N session · O file · Tab choose", r.W, "")
		buf.WriteString(ox+r.X+max(0, (r.W-rendering.VisibleWidth(hint))/2), oy+r.Y+min(r.H-1, y+5), hint, e.tokens.Color(ui.ColorMuted), bg, 0)
	}
}
func (e *emptyMainPane) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.Kind == ui.EventMouseMotion {
		e.hover = -1
		for i, b := range e.buttons {
			if b.Bounds().Contains(ctx.LocalX, ctx.LocalY) {
				e.hover = i
				break
			}
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	if evt.Kind == ui.EventMouseClick {
		for i, b := range e.buttons {
			if b.Bounds().Contains(ctx.LocalX, ctx.LocalY) {
				e.selection = i
				return ui.EventResult{Handled: true, Action: &ui.Action{ID: []string{"pane.new-session", "pane.open-file"}[i], Payload: e.group}}
			}
		}
	}
	if evt.Kind == ui.EventKey {
		switch evt.Key.String() {
		case "tab", "shift+tab", "up", "down", "left", "right":
			e.selection = 1 - e.selection
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		case "n":
			e.selection = 0
		case "o":
			e.selection = 1
		case "enter", "space":
		default:
			return ui.EventResult{}
		}
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: []string{"pane.new-session", "pane.open-file"}[e.selection], Payload: e.group}}
	}
	return ui.EventResult{}
}
func (*emptyMainPane) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Empty pane: New Session or Open File"}
}
