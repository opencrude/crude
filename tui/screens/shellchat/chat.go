// Package shellchat provides the native agent-chat message-list shell surface.
//
// PR6: chatprojection.Renderer exports message rows; shellchat owns scroll/selection/compose.
package shellchat

import (
	"strings"

	"crude/tui/chatprojection"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

const (
	ScreenID    ui.ComponentID = "shellchat"
	AreaID      ui.ComponentID = "shellchat/area"
	ScrollbarID ui.ComponentID = "shellchat/scrollbar"
)

type chatLayout struct {
	padLR         int
	mainSBVisible bool
}

// Screen paints composed agent-chat viewport rows through a native
// ui.VirtualList RowPainter. ScrollModel, main scrollbar drag, and mouse
// text selection live here; chatprojection.Renderer exports message rows.
type Screen struct {
	ui.Base
	th             *theme.Theme
	tokens         *ui.TokenSet
	list           *ui.VirtualList
	scrollbar      *ui.ScrollbarControl
	selection      textSelection
	wheel          ui.WheelAccelState
	paneWheel      ui.WheelAccelState
	paneDrag       *paneGutterDrag
	paneHover      string
	rowsGeneration uint64
	onPaneScroll   func(string)
	rows           []chatprojection.MessageRow
	layout         chatLayout
	paintState     messagePaintState
	width          int
	height         int
}

// New creates an agent-chat shell screen.
func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	list := ui.NewVirtualList(AreaID, tokens)
	list.SetPointerPolicy(ui.PointerPolicyPassthrough)
	list.SetSelectionAnchorsScroll(false)
	list.SetBottomGravity(false)
	list.SetOpaqueRowFill(false)
	list.Scroll().SetAutoScroll(true)
	s := &Screen{
		Base:   ui.NewBase(ScreenID),
		th:     th,
		tokens: tokens,
		list:   list,
	}
	s.scrollbar = ui.NewScrollbarControl(ScrollbarID, list.Scroll(), tokens)
	// The nested pane gutter sits two columns left of the main track. Claim the
	// separator, but never expand across it and steal nested-pane interaction.
	s.scrollbar.SetPointerTargetSize(2)
	s.scrollbar.OnScroll(func(_, _ int) { s.syncScrollAfterUserScroll() })
	return s
}

// SetTheme replaces the active theme.
func (s *Screen) SetTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	s.th = th
	s.tokens = ui.NewTokenSet(th)
	s.list.SetTokens(s.tokens)
	if s.scrollbar != nil {
		s.scrollbar.SetTokens(s.tokens)
	}
}

// SetTokens updates runtime theme tokens.
func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	s.list.SetTokens(tokens)
	if s.scrollbar != nil {
		s.scrollbar.SetTokens(tokens)
	}
}

// VirtualList exposes the native list for tests and scroll binding.
func (s *Screen) VirtualList() *ui.VirtualList { return s.list }

// ScrollModel returns the authoritative chat scroll state.
func (s *Screen) ScrollModel() *ui.ScrollModel { return s.list.Scroll() }

// StickToBottom pins the viewport and arms auto-scroll.
func (s *Screen) StickToBottom() { s.list.StickToBottom() }

// SyncScrollMetrics updates viewport/content row counts before chat export render.
func (s *Screen) SyncScrollMetrics(viewportH, contentH int) {
	scroll := s.list.Scroll()
	scroll.SetViewport(viewportH)
	scroll.SetContent(contentH)
}

// ClearSelection drops an in-progress or completed mouse selection.
func (s *Screen) ClearSelection() { s.selection.Clear() }

// CancelPointerCapture ends selection and scrollbar drag without copying text.
func (s *Screen) CancelPointerCapture() {
	s.ClearSelection()
	s.CancelPaneGutterCapture()
	if s.scrollbar != nil {
		s.scrollbar.CancelPointerCapture()
	}
}

// HasActiveSelection reports a non-empty text selection range.
func (s *Screen) HasActiveSelection() bool { return s.selection.HasActive() }

// IsSelecting reports whether a mouse-drag selection is in progress.
func (s *Screen) IsSelecting() bool { return s.selection.IsSelecting() }

// IsScrollbarDragging reports main-chat scrollbar thumb drag.
func (s *Screen) IsScrollbarDragging() bool {
	return s.scrollbar != nil && s.scrollbar.IsDragging()
}

// SelectionRange returns normalized global line/col bounds.
func (s *Screen) SelectionRange() (startLine, startCol, endLine, endCol int) {
	return s.selection.Range()
}

// SelectedText returns text inside the current selection while dragging.
func (s *Screen) SelectedText() string { return s.selection.SelectedText(s.rows) }

// ViewportPaneLineMap maps viewport row → subpane line for inline-pane wheel routing.
func (s *Screen) ViewportPaneLineMap() []chatprojection.PaneLineInfo {
	scroll := s.list.Scroll()
	viewportH := scroll.Viewport()
	contentH := scroll.Content()
	if viewportH <= 0 {
		return nil
	}
	globalMap := s.paintState.paneLineMap
	visible := make([]chatprojection.PaneLineInfo, viewportH)
	scrollY := scroll.Offset()
	for i := range visible {
		globalLine := 0
		if contentH <= viewportH {
			if i >= contentH {
				continue
			}
			globalLine = i
		} else {
			globalLine = scrollY + i
			if globalLine >= contentH {
				continue
			}
		}
		if globalLine >= 0 && globalLine < len(globalMap) {
			visible[i] = globalMap[globalLine]
		}
	}
	return visible
}

func (s *Screen) Children() []ui.Component {
	if s.mainScrollbarVisible() {
		return []ui.Component{s.list, s.scrollbar}
	}
	return []ui.Component{s.list}
}

// ContentSurfaceWidth is the chat surface width for compose InnerW and PadLR.
func ContentSurfaceWidth(innerW, padLR int, _ bool) int {
	return innerW + padLR*2
}

func (s *Screen) mainScrollbarVisible() bool {
	if s.scrollbar == nil || !s.layout.mainSBVisible || s.width <= 1 {
		return false
	}
	sb := s.scrollbarLayoutRect(ui.Rect{W: s.width, H: s.height})
	listW := s.listLayoutRect(ui.Rect{W: s.width, H: s.height}).W
	return sb.W > 0 && sb.X >= listW
}

func (s *Screen) listLayoutRect(r ui.Rect) ui.Rect {
	list := r
	if !s.layout.mainSBVisible || s.scrollbar == nil {
		return list
	}
	sb := s.scrollbarLayoutRect(r)
	if sb.W <= 0 || sb.X <= 0 || r.W <= 1 {
		return list
	}
	list.W = sb.X
	if list.W < 0 {
		list.W = 0
	}
	return list
}

func (s *Screen) scrollbarLayoutRect(r ui.Rect) ui.Rect {
	gw := ui.ScrollbarWidth
	sbX := r.W - s.layout.padLR - gw
	if sbX < 0 {
		sbX = r.W - gw
	}
	if sbX < 0 {
		return ui.Rect{}
	}
	return ui.Rect{X: sbX, Y: 0, W: gw, H: r.H}
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	h := s.list.Scroll().Content()
	if h < 1 {
		h = 1
	}
	if cs.MaxH > 0 && h > cs.MaxH {
		h = cs.MaxH
	}
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: h})
}

// Layout lays out children with PARENT-LOCAL rects. Host.paintNode paints
// children at (parent origin + child bounds), so a child whose bounds repeat
// the slot origin would be painted twice-offset (the shellchat double-origin
// bug that shifted chat content into the input prompt and status bar under
// the root host). Children must never see the screen's absolute slot rect.
func (s *Screen) Layout(_ ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	s.width = r.W
	s.height = r.H
	local := ui.Rect{X: 0, Y: 0, W: r.W, H: r.H}
	listRect := s.listLayoutRect(local)
	s.list.Layout(ui.LayoutContext{}, listRect)
	if s.mainScrollbarVisible() {
		s.scrollbar.SetScroll(s.list.Scroll())
		s.scrollbar.Layout(ui.LayoutContext{}, s.scrollbarLayoutRect(local))
	}
}

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	_ = ox
	_ = oy
	_ = buf
	_ = ctx
}

func (s *Screen) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if s == nil {
		return ui.EventResult{}
	}

	if evt.Kind == ui.EventCustom {
		if _, ok := evt.Custom.(types.SelectionTickMsg); ok {
			return s.handleSelectionTick()
		}
	}

	if s.paneDrag != nil {
		switch evt.Kind {
		case ui.EventMouseMotion:
			return s.handlePaneGutterMotion(ctx, evt.MouseMotion)
		case ui.EventMouseRelease:
			return s.handlePaneGutterRelease(ctx, evt.MouseRelease)
		}
	}

	if evt.Kind == ui.EventMouseClick {
		if res := s.handlePaneGutterClick(ctx, evt.MouseClick); res.Handled {
			return res
		}
	}

	switch evt.Kind {
	case ui.EventMouseClick:
		return s.handleSelectionClick(ctx, evt.MouseClick)
	case ui.EventMouseMotion:
		if s.selection.selecting {
			return s.handleSelectionMotion(ctx, evt.MouseMotion)
		}
		if changed := s.updatePaneGutterHover(ctx); changed || s.paneHover != "" {
			return ui.EventResult{Handled: s.paneHover != "", Invalidate: ui.InvalidatePaint}
		}
	case ui.EventMouseRelease:
		if s.selection.selecting {
			return s.handleSelectionRelease(ctx, evt.MouseRelease)
		}
	}

	if evt.Kind == ui.EventMouseWheel {
		if res := s.handlePaneWheel(ctx, evt.MouseWheel); res.Handled {
			return res
		}
	}

	if s.handleScrollInput(evt) {
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	return ui.EventResult{}
}

func (s *Screen) syncScrollAfterUserScroll() {
	scroll := s.list.Scroll()
	if scroll.NearBottom(0) {
		scroll.SetAutoScroll(true)
	} else {
		scroll.SetAutoScroll(false)
	}
}

// PaintTestBuffer lays out and paints into a fresh buffer for tests.
func (s *Screen) PaintTestBuffer(w, h int) *surface.CellBuffer {
	buf := surface.NewCellBuffer(w, h)
	s.Layout(ui.LayoutContext{}, ui.Rect{W: w, H: h})
	host := ui.NewHost(s.th, s)
	host.Resize(w, h)
	host.Layout()
	host.Paint(buf)
	return buf
}

// PlainRow extracts a plain-text row from a painted buffer for tests.
func PlainRow(buf *surface.CellBuffer, y, maxW int) string {
	if buf == nil {
		return ""
	}
	var b strings.Builder
	for x := 0; x < maxW && x < buf.Width(); x++ {
		c := buf.Get(x, y)
		if c.Char != 0 {
			b.WriteRune(c.Char)
		} else if !c.Transparent && c.Width > 0 {
			b.WriteRune(' ')
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// NonEmptyCellCount counts cells with a non-zero rune (excludes continuations).
func NonEmptyCellCount(buf *surface.CellBuffer) int {
	if buf == nil {
		return 0
	}
	n := 0
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Char != 0 && c.Width > 0 {
				n++
			}
		}
	}
	return n
}
