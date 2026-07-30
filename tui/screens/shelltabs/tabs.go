// Package shelltabs provides the native persistent main application tab bar.
package shelltabs

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/window"
)

const (
	ScreenID ui.ComponentID = "shelltabs"
	TabsID   ui.ComponentID = "shelltabs/tabs"
)

// TabProjection is one tab row for the shell tab bar.
type TabProjection struct {
	ID        string
	Label     string
	Icon      string
	Closable  bool
	Attention bool
}

// Screen paints the main tab strip (tabs row + separator) via Host.Paint.
type Screen struct {
	ui.Base
	th         *theme.Theme
	tokens     *ui.TokenSet
	tabs       *ui.Tabs
	barFocused bool
	width      int
}

// New creates a shell tab bar screen.
func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	t := ui.NewTabs(TabsID, tokens)
	t.SetChrome(ui.TabsShell)
	return &Screen{
		Base:   ui.NewBase(ScreenID),
		th:     th,
		tokens: tokens,
		tabs:   t,
	}
}

// SetTokens updates runtime theme tokens.
func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	s.tabs.SetTokens(tokens)
}

// SetTheme replaces the theme used for spacing and colors.
func (s *Screen) SetTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	s.th = th
	s.tokens = ui.NewTokenSet(th)
	s.tabs.SetTokens(s.tokens)
	s.tabs.SetChrome(ui.TabsShell)
}

// TabsControl returns the underlying Tabs component.
func (s *Screen) TabsControl() *ui.Tabs { return s.tabs }

// SetProjection updates tab items for the next paint.
func (s *Screen) SetProjection(items []TabProjection, activeID string, hoverIndex int, barFocused bool) {
	tabItems := make([]ui.TabItem, len(items))
	for i, item := range items {
		tabItems[i] = ui.TabItem{
			ID:        item.ID,
			Label:     item.Label,
			Icon:      item.Icon,
			Closable:  item.Closable,
			Attention: item.Attention,
		}
	}
	s.tabs.SetItems(tabItems, activeID)
	s.tabs.SyncHover(hoverIndex)
	s.barFocused = barFocused
	s.tabs.SetBarFocused(barFocused)
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
			Icon:      tab.Icon,
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

func (s *Screen) Children() []ui.Component { return []ui.Component{s.tabs} }

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	h := 2
	if cs.MaxH > 0 && cs.MaxH < h {
		h = cs.MaxH
	}
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: h})
}

func (s *Screen) Layout(_ ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	if r.W > 0 {
		s.width = r.W
	}
	s.tabs.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: 0, W: r.W, H: 1})
}

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	b := s.Bounds()
	if b.Empty() || buf == nil || b.H < 2 {
		return
	}
	border := s.color(ui.ColorBorder)
	accent := s.color(ui.ColorAccent)
	sep := "─"
	sepFG := border
	if s.barFocused {
		sep = "━"
		sepFG = accent
	}
	sepY := oy + b.Y + 1
	buf.WriteString(ox+b.X, sepY, strings.Repeat(sep, max(0, b.W)), sepFG, cell.NoColor, 0)
}

func (s *Screen) Handle(_ ui.EventContext, _ tea.Msg) ui.EventResult { return ui.EventResult{} }

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case "tabs.activate", "tabs.close", "tabs.enter":
		return ui.EventResult{Handled: true, Action: &action}
	}
	return ui.EventResult{Action: &action}
}

func (s *Screen) HitTest(localX int) (int, bool) {
	return s.tabs.HitTest(localX)
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Tab bar", Value: s.tabs.ActiveID()}
}

func (s *Screen) color(role ui.SemanticColor) cell.Color {
	if s.tokens != nil {
		return s.tokens.Color(role)
	}
	return cell.NoColor
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
