// Package shelltabs provides the native persistent main application tab bar.
package shelltabs

import (
	"fmt"

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
// stays put and nothing is left floating to its left. Only this bar takes it —
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
	th     *theme.Theme
	tokens *ui.TokenSet
	tabs   *ui.Tabs
	width  int
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
			Closable:  item.Closable,
			Attention: item.Attention,
		}
	}
	s.tabs.SetItems(tabItems, activeID)
	s.tabs.SyncHover(hoverIndex)
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
	h := ui.StripHeight
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
	// The strip spans the whole content column so its baseline runs from the
	// navigation rail to the right edge; only the labels take RailInset, which
	// gives the first tab breathing room without moving the rule.
	s.tabs.SetLabelInset(RailInset)
	s.tabs.Layout(ui.LayoutContext{}, ui.Rect{
		X: 0,
		Y: 0,
		W: max(0, r.W),
		H: min(ui.StripHeight, max(1, r.H)),
	})
}

func (s *Screen) Handle(_ ui.EventContext, _ ui.Event) ui.EventResult { return ui.EventResult{} }

// Paint is intentionally empty: the strip control paints its own labels and
// baseline (bleeding into the parent-published rule row), so the screen has no
// chrome left of its own to draw.
func (s *Screen) Paint(*surface.CellBuffer, int, int, ui.PaintContext) {}

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case "tabs.activate", "tabs.close", "tabs.enter":
		return ui.EventResult{Handled: true, Action: &action}
	}
	return ui.EventResult{Action: &action}
}

// HitTest resolves screen-local X against the tabs control, translating
// through the control's padded bounds so callers work in slot coordinates.
func (s *Screen) HitTest(localX int) (int, bool) {
	return s.tabs.HitTest(localX - s.tabs.Bounds().X)
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Tab bar", Value: s.tabs.ActiveID()}
}
