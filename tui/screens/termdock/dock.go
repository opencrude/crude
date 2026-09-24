package termdock

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/screens/shellterm"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

const (
	ScreenID  ui.ComponentID = "termdock"
	DividerID ui.ComponentID = "termdock/divider"
	RailID    ui.ComponentID = "termdock/rail"
)

const (
	PanelWorkspace = "workspace"
	PanelProblems  = "problems"
	PanelNotices   = "notices"
	PanelTerminal  = "terminal"
)

const (
	dividerH = 1

	// Terminal mode reserves one action row below the divider and still retains
	// the shared minimum PTY grid.
	MinRenderableHeight = dividerH + 1 + workspace.MinTerminalRows
)

const (
	ActionNew = "terminal.new"

	iconNew   = "+"
	iconFloat = "⇱"
	iconClose = "✕"
)

type headerAction struct {
	id    string
	glyph string
	rect  ui.Rect
}

type panelEntry struct {
	id      string
	content ui.Component
}

// Screen is the workspace bottom panel. Status views are passive retained
// components; Terminal is an instance view with a vertical terminal rail.
type Screen struct {
	ui.Base
	th          *theme.Theme
	tokens      *ui.TokenSet
	split       *ui.SplitModel
	divider     *ui.SplitDivider
	rail        *ui.List
	railScroll  *ui.ScrollbarControl
	panels      map[string]panelEntry
	activePanel string
	open        bool

	order      []string
	screens    map[string]*shellterm.Screen
	attention  map[string]bool
	active     string
	focused    bool
	renderable bool
	railW      int

	actions     []headerAction
	hoverAction int
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	split := ui.NewSplitModel(ui.Vertical)
	split.SetMinSizes(8, MinRenderableHeight)
	s := &Screen{
		Base:        ui.NewBase(ScreenID),
		th:          th,
		tokens:      tokens,
		split:       split,
		divider:     ui.NewSplitDivider(DividerID, split),
		rail:        ui.NewList(RailID, tokens),
		panels:      make(map[string]panelEntry),
		screens:     make(map[string]*shellterm.Screen),
		attention:   make(map[string]bool),
		hoverAction: -1,
	}
	s.divider.SetTokens(tokens)
	s.divider.SetColorRole(ui.ColorBorder)
	s.divider.SetFill(true)
	s.divider.SetTransparent(true)
	s.rail.SetActivateAction("terminal.select")
	s.rail.SetDefaultRowBackground(ui.ColorBg)
	s.railScroll = ui.NewScrollbarControl(ui.NewID(RailID, "scroll"), s.rail.Scroll(), tokens)
	s.railScroll.SetSurfaceBackground(ui.ColorBg)
	return s
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	s.divider.SetTokens(tokens)
	s.rail.SetTokens(tokens)
	s.railScroll.SetTokens(tokens)
	for _, panel := range s.panels {
		if consumer, ok := panel.content.(interface{ SetTokens(*ui.TokenSet) }); ok {
			consumer.SetTokens(tokens)
		}
	}
	for _, child := range s.screens {
		if child != nil {
			child.SetTokens(tokens)
		}
	}
}

func (s *Screen) SetTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	s.th = th
	s.SetTokens(ui.NewTokenSet(th))
}

func (s *Screen) SetPanel(id string, content ui.Component) {
	if id == "" || content == nil || id == PanelTerminal {
		return
	}
	s.panels[id] = panelEntry{id: id, content: content}
	if consumer, ok := content.(interface{ SetTokens(*ui.TokenSet) }); ok {
		consumer.SetTokens(s.tokens)
	}
}

func (s *Screen) Panel(id string) ui.Component {
	return s.panels[id].content
}

func (s *Screen) ShowPanel(id string) bool {
	if id == PanelTerminal {
		if s.Empty() {
			return false
		}
	} else if _, ok := s.panels[id]; !ok {
		return false
	}
	s.activePanel = id
	s.open = true
	s.syncRail()
	return true
}

func (s *Screen) TogglePanel(id string) bool {
	if s.open && s.activePanel == id {
		s.open = false
		s.focused = false
		return false
	}
	return s.ShowPanel(id)
}

func (s *Screen) ClosePanel()          { s.open, s.focused = false, false }
func (s *Screen) IsOpen() bool         { return s.open }
func (s *Screen) ActivePanel() string  { return s.activePanel }
func (s *Screen) TerminalActive() bool { return s.open && s.activePanel == PanelTerminal }

func (s *Screen) Empty() bool { return len(s.order) == 0 }

func (s *Screen) IDs() []string {
	out := make([]string, len(s.order))
	copy(out, s.order)
	return out
}

func (s *Screen) Contains(id string) bool {
	_, ok := s.screens[id]
	return ok
}

func (s *Screen) ActiveID() string { return s.active }

func (s *Screen) Active() *shellterm.Screen {
	if s.active == "" {
		return nil
	}
	return s.screens[s.active]
}

func (s *Screen) SetFocused(v bool) { s.focused = v && s.open }

func (s *Screen) OwnsFocus(id ui.ComponentID) bool {
	if id == ScreenID || id == RailID || id == DividerID {
		return true
	}
	if panel := s.activeContent(); panel != nil && panel.ID() == id {
		return true
	}
	for _, child := range s.screens {
		if child != nil && child.ID() == id {
			return true
		}
	}
	return strings.HasPrefix(string(id), "status-overview/")
}

func (s *Screen) PreferredFocus() ui.ComponentID {
	if s.TerminalActive() {
		if child := s.Active(); child != nil {
			return child.ID()
		}
	}
	if panel := s.activeContent(); panel != nil {
		return panel.ID()
	}
	return ScreenID
}

func (s *Screen) PrepareResize(first, minMain, minDock int) {
	if minDock < MinRenderableHeight {
		minDock = MinRenderableHeight
	}
	s.split.SetMinSizes(minMain, minDock)
	s.divider.SetCurrentFirst(first)
}

func (s *Screen) Mount(id string, screen *shellterm.Screen) {
	if id == "" || screen == nil {
		return
	}
	if _, exists := s.screens[id]; !exists {
		s.order = append(s.order, id)
	}
	s.screens[id] = screen
	screen.SetFloating(false)
	screen.SetTokens(s.tokens)
	s.active = id
	delete(s.attention, id)
	s.ShowPanel(PanelTerminal)
}

func (s *Screen) Unmount(id string) *shellterm.Screen {
	screen := s.screens[id]
	if screen == nil {
		return nil
	}
	delete(s.screens, id)
	delete(s.attention, id)
	for i, existing := range s.order {
		if existing == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	if s.active == id {
		s.active = ""
		if len(s.order) > 0 {
			s.active = s.order[len(s.order)-1]
		}
	}
	if s.Empty() && s.activePanel == PanelTerminal {
		s.ClosePanel()
	}
	s.syncRail()
	return screen
}

func (s *Screen) SetActive(id string) bool {
	if _, ok := s.screens[id]; !ok {
		return false
	}
	s.active = id
	delete(s.attention, id)
	s.syncRail()
	return true
}

func (s *Screen) SetAttention(id string, v bool) {
	if _, ok := s.screens[id]; !ok {
		return
	}
	if v && id != s.active {
		s.attention[id] = true
	} else {
		delete(s.attention, id)
	}
	s.syncRail()
}

func (s *Screen) HasAttention(id string) bool { return s.attention[id] }

func (s *Screen) SetTitle(id, title string) {
	if title == "" {
		return
	}
	if child := s.screens[id]; child != nil {
		child.SetWindowTitle(title)
	}
	s.syncRail()
}

func (s *Screen) syncRail() {
	items := make([]ui.ListItem, 0, len(s.order))
	for _, id := range s.order {
		child := s.screens[id]
		if child == nil {
			continue
		}
		label := child.Title()
		if s.attention[id] {
			label = "● " + label
		}
		items = append(items, ui.ListItem{ID: id, Label: label})
	}
	s.rail.SetItemsStable(items)
	if s.active != "" {
		for index, item := range s.rail.Items() {
			if item.ID == s.active {
				s.rail.SelectIndex(index)
				break
			}
		}
	}
}

func (s *Screen) activeContent() ui.Component {
	if !s.open {
		return nil
	}
	if s.activePanel == PanelTerminal {
		return s.Active()
	}
	return s.panels[s.activePanel].content
}

func (s *Screen) Children() []ui.Component {
	if !s.open || !s.renderable {
		return nil
	}
	out := []ui.Component{s.divider}
	if child := s.activeContent(); child != nil {
		out = append(out, child)
	}
	if s.TerminalActive() && len(s.order) > 1 && s.railW > 0 {
		out = append(out, s.rail)
		if s.railScroll.Needed() {
			out = append(out, s.railScroll)
		}
	}
	return out
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	s.renderable = s.open && r.W > 0 && r.H >= MinRenderableHeight
	if !s.renderable {
		s.actions = s.actions[:0]
		s.hoverAction = -1
		return
	}
	s.divider.Layout(ctx, ui.Rect{X: 0, Y: 0, W: r.W, H: dividerH})
	bodyY, bodyH := dividerH, max(0, r.H-dividerH)
	s.railW = 0

	if s.TerminalActive() {
		s.actions = s.actions[:0]
		glyphs := []headerAction{{id: ActionNew, glyph: iconNew}, {id: shellterm.ActionPromote, glyph: iconFloat}, {id: shellterm.ActionClose, glyph: iconClose}}
		actionsW := 3 * len(glyphs)
		x := max(0, r.W-actionsW-1)
		for i := range glyphs {
			glyphs[i].rect = ui.Rect{X: x + 3*i, Y: bodyY, W: 3, H: 1}
		}
		s.actions = append(s.actions, glyphs...)
		if len(s.order) > 1 && r.W >= 50 {
			s.railW = min(35, max(8, r.W/4))
			railH := max(0, bodyH-1)
			railListW := s.railW
			showScroll := s.railScroll.NeededForViewport(railH)
			if showScroll && railListW > 1 {
				railListW--
			}
			s.rail.Layout(ctx, ui.Rect{X: r.W - s.railW, Y: bodyY + 1, W: railListW, H: railH})
			if showScroll {
				s.railScroll.Layout(ctx, ui.Rect{X: r.W - 1, Y: bodyY + 1, W: 1, H: railH})
			} else {
				s.railScroll.Layout(ctx, ui.Rect{})
			}
		}
		contentW := r.W
		if s.railW > 0 {
			contentW -= s.railW + 1
		}
		if child := s.Active(); child != nil {
			child.Layout(ctx, ui.Rect{X: 0, Y: bodyY + 1, W: max(0, contentW), H: max(0, bodyH-1)})
		}
		return
	}

	s.actions = s.actions[:0]
	s.railScroll.Layout(ctx, ui.Rect{})
	if child := s.activeContent(); child != nil {
		child.Layout(ctx, ui.Rect{X: 0, Y: bodyY, W: r.W, H: bodyH})
	}
}

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	r := s.Bounds()
	bg, muted, fg, border := cell.NoColor, cell.NoColor, cell.NoColor, cell.NoColor
	if s.tokens != nil {
		bg = s.tokens.Color(ui.ColorBg)
		muted = s.tokens.Color(ui.ColorMuted)
		fg = s.tokens.Color(ui.ColorFg)
		border = s.tokens.Color(ui.ColorBorder)
	}
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	if !s.open || !s.renderable || !s.TerminalActive() {
		return
	}
	if s.railW > 0 {
		x := ox + r.X + r.W - s.railW - 1
		for y := oy + r.Y + dividerH; y < oy+r.Y+r.H; y++ {
			buf.Set(x, y, cell.NewCell(ui.FirstRune(s.tokens.Glyphs().BorderV, '│'), border, bg, 0))
		}
	}
	for i, a := range s.actions {
		color := muted
		if i == s.hoverAction {
			color = fg
		}
		buf.WriteString(ox+r.X+a.rect.X, oy+r.Y+a.rect.Y, " "+a.glyph+" ", color, bg, 0)
	}
}

func (s *Screen) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if !s.renderable {
		return ui.EventResult{}
	}
	if evt.IsKey() && evt.Key.Code == tea.KeyEsc && !s.TerminalActive() {
		s.ClosePanel()
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "workspace.panel.closed"}, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
	}
	switch evt.Kind {
	case ui.EventMouseMotion:
		hover := s.actionAt(ctx.LocalX, ctx.LocalY)
		if hover != s.hoverAction {
			s.hoverAction = hover
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
	case ui.EventMouseClick:
		if evt.MouseClick.Button == tea.MouseLeft {
			if i := s.actionAt(ctx.LocalX, ctx.LocalY); i >= 0 {
				return s.runHeaderAction(s.actions[i].id)
			}
		}
	}
	return ui.EventResult{}
}

func (s *Screen) actionAt(lx, ly int) int {
	for i, a := range s.actions {
		if a.rect.Contains(lx, ly) {
			return i
		}
	}
	return -1
}

func (s *Screen) runHeaderAction(id string) ui.EventResult {
	switch id {
	case ActionNew:
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionNew}, Invalidate: ui.InvalidatePaint}
	case shellterm.ActionPromote, shellterm.ActionClose:
		if s.active == "" {
			return ui.EventResult{Handled: true}
		}
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: id, Payload: s.active}, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	}
	return ui.EventResult{Handled: true}
}

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case "terminal.select", "list.activate":
		item, ok := s.rail.Selected()
		if ok && s.SetActive(item.ID) {
			if child := s.Active(); child != nil {
				return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: child.ID()}, Invalidate: ui.InvalidateLayout | ui.InvalidateFocus | ui.InvalidatePaint}
			}
		}
		return ui.EventResult{Handled: true}
	case "workspace.panel.closed":
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return ui.EventResult{Action: &action}
}

func (s *Screen) Semantics() ui.Semantics {
	label := "Workspace panel"
	if s.TerminalActive() {
		if child := s.Active(); child != nil {
			label = child.Title()
		}
	} else if s.activePanel != "" {
		label = strings.ToUpper(s.activePanel[:1]) + s.activePanel[1:]
	}
	return ui.Semantics{Role: ui.RoleGroup, Label: label, Selected: s.focused}
}

var _ ui.Component = (*Screen)(nil)
var _ ui.Parent = (*Screen)(nil)
var _ ui.ActionHandler = (*Screen)(nil)
