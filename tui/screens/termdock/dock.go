package termdock

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/screens/shellterm"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	ScreenID  ui.ComponentID = "termdock"
	DividerID ui.ComponentID = "termdock/divider"
	TabsID    ui.ComponentID = "termdock/tabs"
)

const (
	dividerH = 1
	tabsH    = 1
	chromeH  = dividerH + tabsH
)

// Header action ids (panel-title buttons, VS Code style).
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

// Screen is the workspace-level terminal dock: a vertical resize handle,
// closable tabs, and one active retained shellterm.Screen.
type Screen struct {
	ui.Base
	th        *theme.Theme
	tokens    *ui.TokenSet
	tabs      *ui.Tabs
	split     *ui.SplitModel
	divider   *ui.SplitDivider
	order     []string
	screens   map[string]*shellterm.Screen
	attention map[string]bool
	active    string
	focused   bool
	tabFocus  bool

	actions     []headerAction
	hoverAction int // index into actions, -1 = none
}

// New creates an empty terminal dock.
func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	split := ui.NewSplitModel(ui.Vertical)
	split.SetMinSizes(8, 6)
	s := &Screen{
		Base:        ui.NewBase(ScreenID),
		th:          th,
		tokens:      tokens,
		tabs:        ui.NewTabs(TabsID, tokens),
		split:       split,
		divider:     ui.NewSplitDivider(DividerID, split),
		screens:     make(map[string]*shellterm.Screen),
		attention:   make(map[string]bool),
		hoverAction: -1,
	}
	s.tabs.SetChrome(ui.TabsShell)
	s.divider.SetTokens(tokens)
	s.divider.SetColorRole(ui.ColorBorder)
	s.divider.SetFill(true)
	s.divider.SetTransparent(true)
	return s
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	if s.tabs != nil {
		s.tabs.SetTokens(tokens)
	}
	if s.divider != nil {
		s.divider.SetTokens(tokens)
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

func (s *Screen) SetFocused(v bool) {
	s.focused = v
	if s.tabs != nil {
		s.tabs.SetBarFocused(v && s.tabFocus)
	}
}

// OwnsFocus reports whether id is this dock or a mounted descendant.
func (s *Screen) OwnsFocus(id ui.ComponentID) bool {
	if id == ScreenID || id == TabsID || id == DividerID {
		return true
	}
	for _, child := range s.screens {
		if child != nil && child.ID() == id {
			return true
		}
	}
	return false
}

// PrepareResize records the first-pane size (screen Y of this dock) so the
// divider drag reports absolute main-axis coordinates.
func (s *Screen) PrepareResize(first, minMain, minDock int) {
	if s.split != nil {
		s.split.SetMinSizes(minMain, minDock)
	}
	if s.divider != nil {
		s.divider.SetCurrentFirst(first)
	}
}

// Mount retains screen under id and makes it active. The same Screen pointer
// can later Unmount into a floating window without reconstructing the emulator.
func (s *Screen) Mount(id string, screen *shellterm.Screen) {
	if id == "" || screen == nil {
		return
	}
	if s.screens == nil {
		s.screens = make(map[string]*shellterm.Screen)
	}
	if _, exists := s.screens[id]; !exists {
		s.order = append(s.order, id)
	}
	s.screens[id] = screen
	screen.SetFloating(false)
	if s.tokens != nil {
		screen.SetTokens(s.tokens)
	}
	s.active = id
	delete(s.attention, id)
	s.syncTabs()
}

// Unmount removes id from the dock without closing the emulator.
func (s *Screen) Unmount(id string) *shellterm.Screen {
	screen := s.screens[id]
	if screen == nil {
		return nil
	}
	delete(s.screens, id)
	delete(s.attention, id)
	for i, existing := range s.order {
		if existing != id {
			continue
		}
		s.order = append(s.order[:i], s.order[i+1:]...)
		break
	}
	if s.active == id {
		s.active = ""
		if len(s.order) > 0 {
			s.active = s.order[len(s.order)-1]
		}
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

// SetAttention marks a tab (bell in an unfocused terminal). Activating the
// tab clears it.
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

// HasAttention reports whether id has an unseen bell.
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
	for _, id := range s.order {
		child := s.screens[id]
		if child == nil {
			continue
		}
		items = append(items, ui.TabItem{
			ID:        id,
			Label:     child.Title(),
			Closable:  true,
			Attention: s.attention[id],
		})
	}
	s.tabs.SetItems(items, s.active)
}

func (s *Screen) Children() []ui.Component {
	if s.Empty() {
		return nil
	}
	out := []ui.Component{s.divider, s.tabs}
	if child := s.Active(); child != nil {
		out = append(out, child)
	}
	return out
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	if s.Empty() {
		return
	}
	s.divider.Layout(ctx, ui.Rect{X: 0, Y: 0, W: r.W, H: dividerH})

	// Panel-title actions live at the right end of the tab row.
	s.actions = s.actions[:0]
	glyphs := []headerAction{
		{id: ActionNew, glyph: iconNew},
		{id: shellterm.ActionPromote, glyph: iconFloat},
		{id: shellterm.ActionClose, glyph: iconClose},
	}
	actionsW := 3 * len(glyphs)
	x := r.W - actionsW - 1
	if x < 0 {
		x = 0
	}
	for i := range glyphs {
		glyphs[i].rect = ui.Rect{X: x + 3*i, Y: dividerH, W: 3, H: 1}
	}
	s.actions = append(s.actions, glyphs...)

	tabsW := max(0, r.W-actionsW-2)
	s.tabs.Layout(ctx, ui.Rect{X: 0, Y: dividerH, W: tabsW, H: tabsH})
	if child := s.Active(); child != nil {
		child.Layout(ctx, ui.Rect{X: 0, Y: chromeH, W: r.W, H: max(0, r.H-chromeH)})
	}
}

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	r := s.Bounds()
	bg, muted, fg := cell.NoColor, cell.NoColor, cell.NoColor
	if s.tokens != nil {
		bg = s.tokens.Color(ui.ColorBg)
		muted = s.tokens.Color(ui.ColorMuted)
		fg = s.tokens.Color(ui.ColorFg)
	}
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	if s.Empty() {
		return
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
	switch evt.Kind {
	case ui.EventMouseMotion:
		hover := s.actionAt(ctx.LocalX, ctx.LocalY)
		if hover != s.hoverAction {
			s.hoverAction = hover
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
		return ui.EventResult{Handled: true}
	case ui.EventMouseClick:
		if evt.MouseClick.Button == tea.MouseLeft {
			if i := s.actionAt(ctx.LocalX, ctx.LocalY); i >= 0 {
				return s.runHeaderAction(s.actions[i].id)
			}
		}
		s.tabFocus = false
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus}
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
		return ui.EventResult{
			Handled:    true,
			Action:     &ui.Action{ID: ActionNew},
			Invalidate: ui.InvalidatePaint,
		}
	case shellterm.ActionPromote, shellterm.ActionClose:
		if s.active == "" {
			return ui.EventResult{Handled: true}
		}
		return ui.EventResult{
			Handled:    true,
			Action:     &ui.Action{ID: id, Payload: s.active},
			Invalidate: ui.InvalidateLayout | ui.InvalidatePaint,
		}
	}
	return ui.EventResult{Handled: true}
}

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case "tabs.activate":
		id, _ := action.Payload.(string)
		if s.SetActive(id) {
			s.tabFocus = false
			if child := s.Active(); child != nil {
				return ui.EventResult{
					Handled:    true,
					Action:     &ui.Action{ID: "ui.focus", Payload: child.ID()},
					Invalidate: ui.InvalidateLayout | ui.InvalidateFocus | ui.InvalidatePaint,
				}
			}
		}
		return ui.EventResult{Handled: true}
	case "tabs.enter":
		s.tabFocus = false
		if child := s.Active(); child != nil {
			return ui.EventResult{
				Handled:    true,
				Action:     &ui.Action{ID: "ui.focus", Payload: child.ID()},
				Invalidate: ui.InvalidateFocus | ui.InvalidatePaint,
			}
		}
		return ui.EventResult{Handled: true}
	case "tabs.close":
		id, _ := action.Payload.(string)
		return ui.EventResult{
			Handled:    true,
			Action:     &ui.Action{ID: shellterm.ActionClose, Payload: id},
			Invalidate: ui.InvalidateLayout | ui.InvalidatePaint,
		}
	}
	return ui.EventResult{Action: &action}
}

func (s *Screen) Semantics() ui.Semantics {
	label := "Terminal dock"
	if child := s.Active(); child != nil {
		label = child.Title()
	}
	return ui.Semantics{Role: ui.RoleGroup, Label: label, Selected: s.focused}
}

var _ ui.Component = (*Screen)(nil)
var _ ui.Parent = (*Screen)(nil)
var _ ui.ActionHandler = (*Screen)(nil)
