package termdock

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/screens/shellterm"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

const (
	ScreenID  ui.ComponentID = "termdock"
	DividerID ui.ComponentID = "termdock/divider"
	TabsID    ui.ComponentID = "termdock/tabs"
	HeaderID  ui.ComponentID = "termdock/header"
)

const (
	PanelWorkspace = "workspace"
	PanelProblems  = "problems"
	PanelNotices   = "notices"
	PanelTerminal  = "terminal"
	PanelMemory    = "memory"
	PanelBoard     = "board"
	PanelGit       = "git"
)

const (
	dividerH = 1

	MinRenderableHeight = dividerH + ui.StripHeight + workspace.MinTerminalRows
	terminalLabelWidth  = 16
)

const (
	ActionNew  = "terminal.new"
	ActionHide = "workspace.panel.hide"
	// ActionOpenAsTab and ActionOpenAsWindow move the active surface out of
	// the dock (payload: panel ID).
	ActionOpenAsTab    = "workspace.panel.tab"
	ActionOpenAsWindow = "workspace.panel.window"

	iconNew   = "+"
	iconFloat = "⇱"
	iconTab   = "⧉"
	iconHide  = "⌄"
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

type headerControl struct {
	ui.Base
	owner *Screen
}

type Screen struct {
	ui.Base
	th          *theme.Theme
	tokens      *ui.TokenSet
	split       *ui.SplitModel
	divider     *ui.SplitDivider
	tabs        *ui.Tabs
	header      *headerControl
	panels      map[string]panelEntry
	activePanel string
	open        bool

	order      []string
	screens    map[string]*shellterm.Screen
	attention  map[string]bool
	active     string
	focused    bool
	renderable bool

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
		tabs:        ui.NewTabs(TabsID, tokens),
		panels:      make(map[string]panelEntry),
		screens:     make(map[string]*shellterm.Screen),
		attention:   make(map[string]bool),
		hoverAction: -1,
	}
	s.divider.SetTokens(tokens)
	s.divider.SetColorRole(ui.ColorBorder)
	s.divider.SetFill(true)
	s.divider.SetTransparent(true)
	s.tabs.SetChrome(ui.TabsShell)
	s.tabs.SetRuleFullBleed(true)
	s.header = &headerControl{Base: ui.NewBase(HeaderID), owner: s}
	return s
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	s.divider.SetTokens(tokens)
	s.tabs.SetTokens(tokens)
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
	s.syncTabs()
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

func (s *Screen) Number(id string) int {
	for index, existing := range s.order {
		if existing == id {
			return index + 1
		}
	}
	return 0
}

func (s *Screen) Active() *shellterm.Screen {
	if s.active == "" {
		return nil
	}
	return s.screens[s.active]
}

func (s *Screen) SetFocused(v bool) { s.focused = v && s.open }

func (s *Screen) OwnsFocus(id ui.ComponentID) bool {
	if id == ScreenID || id == TabsID || id == DividerID {
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
	for _, prefix := range []string{"status-overview/", "memorybrowser", "boardbrowser", "gitpanel"} {
		if strings.HasPrefix(string(id), prefix) {
			return true
		}
	}
	return false
}

func (s *Screen) PreferredFocus() ui.ComponentID {
	if s.TerminalActive() {
		if child := s.Active(); child != nil {
			return child.ID()
		}
	}
	if panel := s.activeContent(); panel != nil {
		if preferred, ok := panel.(interface{ PreferredFocus() ui.ComponentID }); ok {
			return preferred.PreferredFocus()
		}
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
	s.syncTabs()
	return screen
}

func (s *Screen) SetActive(id string) bool {
	if _, ok := s.screens[id]; !ok {
		return false
	}
	s.active = id
	delete(s.attention, id)
	s.syncTabs()
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
	s.syncTabs()
}

func (s *Screen) HasAttention(id string) bool { return s.attention[id] }

func (s *Screen) SetTitle(id, title string) {
	if title == "" {
		return
	}
	if child := s.screens[id]; child != nil {
		child.SetWindowTitle(title)
	}
	s.syncTabs()
}

func (s *Screen) syncTabs() {
	items := make([]ui.TabItem, 0, len(s.order))
	for index, id := range s.order {
		child := s.screens[id]
		if child == nil {
			continue
		}
		number := strconv.Itoa(index + 1)
		suffix := " " + number
		label := rendering.TruncateVisual(child.Title(), max(1, terminalLabelWidth-rendering.VisibleWidth(suffix)), "…") + suffix
		items = append(items, ui.TabItem{ID: id, Label: label, CompactLabel: "T " + number, Attention: s.attention[id]})
	}
	s.tabs.SetItems(items, s.active)
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
	out := []ui.Component{}
	if child := s.activeContent(); child != nil {
		out = append(out, child)
	}
	if s.TerminalActive() {
		out = append(out, s.tabs)
	}
	if !s.header.Bounds().Empty() {
		out = append(out, s.header)
	}
	out = append(out, s.divider)
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
		s.header.Layout(ctx, ui.Rect{})
		return
	}
	s.divider.Layout(ctx, ui.Rect{X: 0, Y: 0, W: r.W, H: dividerH})
	bodyY, bodyH := dividerH, max(0, r.H-dividerH)

	if s.TerminalActive() {
		glyphs := []headerAction{{id: "terminal.picker", glyph: "≡"}, {id: ActionNew, glyph: iconNew}, {id: shellterm.ActionPromote, glyph: iconFloat}, {id: shellterm.ActionClose, glyph: iconClose}, {id: ActionHide, glyph: iconHide}}
		s.layoutHeader(ctx, r.W, bodyY, glyphs)
		s.tabs.SetRuleBleed(0, r.W)
		s.tabs.Layout(ctx, ui.Rect{Y: bodyY, W: max(0, r.W-s.header.Bounds().W-1), H: ui.StripHeight})
		if child := s.Active(); child != nil {
			child.Layout(ctx, ui.Rect{Y: bodyY + ui.StripHeight, W: r.W, H: max(0, bodyH-ui.StripHeight)})
		}
		return
	}

	s.actions = s.actions[:0]
	s.header.Layout(ctx, ui.Rect{})
	if child := s.activeContent(); child != nil {
		glyphs := []headerAction{{id: ActionOpenAsTab, glyph: iconTab}, {id: ActionOpenAsWindow, glyph: iconFloat}, {id: ActionHide, glyph: iconHide}}
		s.layoutHeader(ctx, r.W, bodyY, glyphs)
		if panel, ok := child.(interface{ SetDockHeaderInset(int) }); ok {
			panel.SetDockHeaderInset(s.header.Bounds().W)
		}
		child.Layout(ctx, ui.Rect{Y: bodyY, W: r.W, H: bodyH})
	}
}

func (s *Screen) layoutHeader(ctx ui.LayoutContext, width, top int, glyphs []headerAction) {
	actionsW := 0
	for index := range glyphs {
		glyphs[index].rect = ui.Rect{W: rendering.VisibleWidth(glyphs[index].glyph) + 2, H: 1}
		actionsW += glyphs[index].rect.W
	}
	for len(glyphs) > 0 && actionsW > max(0, width-1) {
		actionsW -= glyphs[0].rect.W
		glyphs = glyphs[1:]
	}
	left := 0
	for index := range glyphs {
		glyphs[index].rect.X = left
		left += glyphs[index].rect.W
	}
	s.actions = append(s.actions[:0], glyphs...)
	s.header.Layout(ctx, ui.Rect{X: width - actionsW, Y: top, W: actionsW, H: 1})
	if s.hoverAction >= len(s.actions) {
		s.hoverAction = -1
	}
}

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	r := s.Bounds()
	bg := cell.NoColor
	if s.tokens != nil {
		bg = s.tokens.Color(ui.ColorBg)
	}
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
}

func (s *Screen) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if !s.renderable {
		return ui.EventResult{}
	}
	if evt.IsKey() && evt.Key.Code == tea.KeyEsc && !s.TerminalActive() {
		s.ClosePanel()
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "workspace.panel.closed"}, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
	}
	ctx = ui.ChildEventContext(ctx, s.header.Bounds())
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
	case ActionHide, "terminal.picker":
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: id}, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	case ActionOpenAsTab, ActionOpenAsWindow:
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: id, Payload: s.activePanel}, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
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
	case "tabs.activate":
		if !s.TerminalActive() {
			return ui.EventResult{Action: &action}
		}
		id, _ := action.Payload.(string)
		if s.SetActive(id) {
			if child := s.Active(); child != nil {
				return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: child.ID()}, Invalidate: ui.InvalidateLayout | ui.InvalidateFocus | ui.InvalidatePaint}
			}
		}
		return ui.EventResult{Handled: true}
	case "tabs.enter":
		if !s.TerminalActive() {
			return ui.EventResult{Action: &action}
		}
		if child := s.Active(); child != nil {
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: child.ID()}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
		return ui.EventResult{Handled: true}
	case "workspace.panel.closed":
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return ui.EventResult{Action: &action}
}

func (header *headerControl) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	owner := header.owner
	rect := header.Bounds()
	for index, action := range owner.actions {
		color := owner.tokens.Color(ui.ColorMuted)
		if index == owner.hoverAction {
			color = owner.tokens.Color(ui.ColorFg)
		}
		buf.WriteString(ox+rect.X+action.rect.X, oy+rect.Y, " "+action.glyph+" ", color, owner.tokens.Color(ui.ColorBg), 0)
	}
}

func (header *headerControl) PointerPolicy() ui.PointerPolicy { return ui.PointerPolicyPassthrough }

func (header *headerControl) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Dock actions"}
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
