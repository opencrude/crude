// Package shelltabs provides the native persistent main application tab bar.
package shelltabs

import (
	"fmt"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/window"
)

const (
	ScreenID ui.ComponentID = "shelltabs"
	TabsID   ui.ComponentID = "shelltabs/tabs"
)

// RailInset is the empty column the main tab bar leaves between the navigation
// rail's border and its first tab label. Only the label row takes it: the
// baseline keeps spanning the whole content column from the rail, so the rule
// stays put and nothing is left floating to its left. Only this bar takes it;
// the dock, deck, and mode strips sit inside their own chrome and would read as
// misaligned with it.
const RailInset = 1

// TabProjection is one tab row for the shell tab bar. Tab bars paint labels
// only: the window tab icon is never drawn (tofu risk), so it is not plumbed
// into the strip.
type TabProjection struct {
	ID        string
	Label     string
	Closable  bool
	Attention bool
}

// Screen hosts the main tab strip. The strip owns its own baseline row and
// fills the whole content column handed to it, so the first label, the active
// underline, and the baseline all start on the same rail column.
type Screen struct {
	ui.Base
	th         *theme.Theme
	tokens     *ui.TokenSet
	tabs       *ui.Tabs
	width      int
	newButton  *newSessionButton
	newEnabled bool
	barFocused bool
}

// New creates a shell tab bar screen.
func New(th *theme.Theme) *Screen {
	return NewWithID(th, ScreenID)
}

// NewWithID creates an independently mounted main tab strip.
func NewWithID(th *theme.Theme, id ui.ComponentID) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	t := ui.NewTabs(ui.NewID(id, "tabs"), tokens)
	t.SetChrome(ui.TabsShell)
	b := &newSessionButton{Button: ui.NewButton(ui.NewID(id, "new"), "+", "tabs.new"), tokens: tokens}
	return &Screen{
		Base:      ui.NewBase(id),
		th:        th,
		tokens:    tokens,
		tabs:      t,
		newButton: b,
	}
}

// SetTokens updates runtime theme tokens.
func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	s.tabs.SetTokens(tokens)
	s.newButton.SetTokens(tokens)
}

// SetTheme replaces the theme used for spacing and colors.
func (s *Screen) SetTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	s.th = th
	s.SetTokens(ui.NewTokenSet(th))
	s.tabs.SetChrome(ui.TabsShell)
}

// TabsControl returns the underlying Tabs component.
func (s *Screen) TabsControl() *ui.Tabs { return s.tabs }

// SetDraggable opts this main strip into shared drag/drop gesture actions.
func (s *Screen) SetDraggable(v bool) { s.tabs.SetDraggable(v) }

// SetNewButton opts into the retained trailing New session control.
func (s *Screen) SetNewButton(v bool) { s.newEnabled = v }

// NewButtonBounds returns screen-local geometry, empty when not enabled.
func (s *Screen) NewButtonBounds() ui.Rect {
	if !s.newEnabled {
		return ui.Rect{}
	}
	return s.newButton.Bounds()
}

// SetProjection updates tab items for the next paint.
func (s *Screen) SetProjection(items []TabProjection, activeID string, hoverIndex int, barFocused bool) {
	s.barFocused = barFocused
	tabItems := make([]ui.TabItem, len(items))
	for i, item := range items {
		tabItems[i] = ui.TabItem{
			ID:        item.ID,
			Label:     item.Label,
			Closable:  item.Closable,
			Attention: item.Attention,
		}
	}
	s.tabs.SetItems(tabItems, activeID)
	s.tabs.SyncHover(hoverIndex)
	s.tabs.SetBarFocused(barFocused)
}

// SetProjectionFromTabs projects a main-view group's independently selected tabs.
func (s *Screen) SetProjectionFromTabs(tabs []window.Tab, active window.TabID, focused bool) {
	items := make([]TabProjection, len(tabs))
	for i, tab := range tabs {
		label := tab.DisplayTitle
		if label == "" {
			label = tab.Title
		}
		items[i] = TabProjection{ID: TabIDString(tab.ID), Label: label, Closable: tab.Closable, Attention: tabAttention(tab)}
	}
	s.SetProjection(items, TabIDString(active), s.tabs.HoverIndex(), focused)
}

// SetProjectionFromWindow builds projections from window tab state.
func (s *Screen) SetProjectionFromWindow(w *window.Window, hoverIndex int, barFocused bool) {
	if w == nil || !w.IsOpen() || w.TabCount() == 0 {
		s.SetProjection(nil, "", -1, false)
		return
	}
	tabs := w.Tabs()
	items := make([]TabProjection, len(tabs))
	activeID := ""
	for i, tab := range tabs {
		label := tab.DisplayTitle
		if label == "" {
			label = tab.Title
		}
		id := TabIDString(tab.ID)
		items[i] = TabProjection{
			ID:        id,
			Label:     label,
			Closable:  tab.Closable,
			Attention: tabAttention(tab),
		}
		if i == w.ActiveIndex() {
			activeID = id
		}
	}
	s.SetProjection(items, activeID, hoverIndex, barFocused)
}

// TabIDString is the stable Tabs control ID for a window tab.
func TabIDString(id window.TabID) string {
	return fmt.Sprintf("%d", uint64(id))
}

func tabAttention(tab window.Tab) bool {
	switch tab.State {
	case window.TabError, window.TabModified, window.TabLoading:
		return true
	default:
		return false
	}
}

func (s *Screen) Children() []ui.Component {
	if s.newEnabled {
		return []ui.Component{s.tabs, s.newButton}
	}
	return []ui.Component{s.tabs}
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	h := ui.StripHeight
	if cs.MaxH > 0 && cs.MaxH < h {
		h = cs.MaxH
	}
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: h})
}

func (s *Screen) Layout(_ ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	s.width = max(0, r.W)
	// The strip spans the whole content column so its baseline runs from the
	// navigation rail to the right edge; only the labels take RailInset, which
	// gives the first tab breathing room without moving the rule.
	s.tabs.SetLabelInset(RailInset)
	buttonW := 0
	if s.newEnabled {
		buttonW = min(s.width, rendering.VisibleWidth("  +  "))
	}
	tabW := s.width - buttonW
	s.tabs.SetRuleFullBleed(s.newEnabled)
	s.tabs.SetRuleBleed(0, s.width)
	s.tabs.Layout(ui.LayoutContext{}, ui.Rect{
		X: 0,
		Y: 0,
		W: tabW,
		H: min(ui.StripHeight, max(0, r.H)),
	})
	// Content-sized tabs leave their action immediately after the last tab;
	// overflowing tabs consume tabW, pinning the action to the trailing edge.
	x := min(tabW, s.tabs.LabelRowEnd())
	if s.newEnabled {
		// Do not leave a tab hit target underneath a content-adjacent button.
		bounds := s.tabs.Bounds()
		bounds.W = x
		s.tabs.Layout(ui.LayoutContext{}, bounds)
	}
	// Match the tabs' label band. The second row belongs exclusively to the
	// strip baseline; filling it makes the action hang below the other tabs.
	s.newButton.Layout(ui.LayoutContext{}, ui.Rect{X: x, W: buttonW, H: min(1, max(0, r.H))})
}

func (s *Screen) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if s.newEnabled && s.barFocused && evt.Kind == ui.EventKey && (evt.Key.String() == "+" || evt.Key.String() == "n") {
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "tabs.new"}}
	}
	return ui.EventResult{}
}

// Paint is intentionally empty: the strip control paints its own labels and
// baseline (bleeding into the parent-published rule row), so the screen has no
// chrome left of its own to draw.
func (s *Screen) Paint(*surface.CellBuffer, int, int, ui.PaintContext) {}

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case "tabs.activate", "tabs.close", "tabs.enter", "tabs.drag", "tabs.drop", "tabs.new":
		return ui.EventResult{Handled: true, Action: &action}
	}
	return ui.EventResult{Action: &action}
}

// newSessionButton uses native Button activation and Host focus/hover, with
// compact glyph-only chrome so even a one-column strip retains the action.
type newSessionButton struct {
	*ui.Button
	tokens *ui.TokenSet
}

func (b *newSessionButton) SetTokens(tokens *ui.TokenSet) {
	b.tokens = tokens
	b.Button.SetTokens(tokens)
}

func (b *newSessionButton) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	r := b.Bounds()
	if r.Empty() || b.tokens == nil {
		return
	}
	// Pointer hover gets no treatment of its own: the strip is two rows of
	// cells, so any fill either stops short of the bar's bottom rule or hangs
	// past it. The glyph alone carries the action; keyboard focus still needs
	// its accent chip to be locatable without a pointer.
	fg, bg := b.tokens.Color(ui.ColorAccent), b.tokens.Color(ui.ColorBg)
	if ctx.Focused {
		bg = b.tokens.Color(ui.ColorAccent)
		fg = cell.ContrastInk(bg)
	}
	// Fill only this action's label-row bounds. ui.Tabs is the sole owner of
	// every baseline cell, including the rule beneath the action.
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	buf.WriteString(ox+r.X+(r.W-1)/2, oy+r.Y, "+", fg, bg, cell.AttrBold)
}

func (b *newSessionButton) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleButton, Label: "New session"}
}

// HitTest resolves screen-local X against the tabs control, translating
// through the control's padded bounds so callers work in slot coordinates.
func (s *Screen) HitTest(localX int) (int, bool) {
	return s.tabs.HitTest(localX - s.tabs.Bounds().X)
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Tab bar", Value: s.tabs.ActiveID()}
}
