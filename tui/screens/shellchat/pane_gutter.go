package shellchat

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/chatprojection"
	"crude/tui/surface"
	"crude/tui/ui"
)

type paneGutterDrag struct {
	paneID  string
	control *ui.ScrollbarControl
}

func (s *Screen) paneGutterState(paneID string) (PaneGutterState, bool) {
	if s == nil || paneID == "" || s.paintState.compose.PaneGutters == nil {
		return PaneGutterState{}, false
	}
	st, ok := s.paintState.compose.PaneGutters[paneID]
	return st, ok && st.Visible && st.Scroll != nil
}

func (s *Screen) nestedGutterX() int {
	return NestedGutterX(s.paintState.compose, s.list.Bounds().W)
}

func (s *Screen) paneViewportSpan(paneID string) (start, end int, ok bool) {
	mapRows := s.ViewportPaneLineMap()
	start = -1
	for i, info := range mapRows {
		if info.PaneID != paneID {
			continue
		}
		if start < 0 {
			start = i
		}
		end = i
	}
	return start, end, start >= 0
}

func (s *Screen) paneTrackLocalY(paneID string, viewportY int) (localY int, trackH int, ok bool) {
	start, end, ok := s.paneViewportSpan(paneID)
	if !ok {
		return 0, 0, false
	}
	st, ok := s.paneGutterState(paneID)
	if !ok {
		return 0, 0, false
	}
	trackH = st.TrackH
	if trackH <= 0 {
		trackH = st.Scroll.Viewport()
	}
	if trackH < 1 {
		trackH = 1
	}
	span := end - start + 1
	if span < 1 {
		span = 1
	}
	localY = viewportY - start
	if localY < 0 {
		localY = 0
	}
	if localY >= span {
		localY = span - 1
	}
	if localY >= trackH {
		localY = trackH - 1
	}
	return localY, trackH, true
}

func (s *Screen) viewportPaneInfo(localY int) (chatprojection.PaneLineInfo, bool) {
	mapRows := s.ViewportPaneLineMap()
	if localY < 0 || localY >= len(mapRows) {
		return chatprojection.PaneLineInfo{}, false
	}
	info := mapRows[localY]
	if info.PaneID == "" {
		return chatprojection.PaneLineInfo{}, false
	}
	return info, true
}

func (s *Screen) isNestedGutterHit(localX int) bool {
	gx := s.nestedGutterX()
	return gx >= 0 && localX >= gx-(3-ui.ScrollbarWidth) && localX <= gx
}

func (s *Screen) paintNestedGutter(buf *surface.CellBuffer, x, y int, paneInfo chatprojection.PaneLineInfo) {
	if buf == nil || paneInfo.PaneID == "" {
		return
	}
	st, ok := s.paneGutterState(paneInfo.PaneID)
	if !ok {
		return
	}
	bg := cell.NoColor
	if s.paintState.compose.HasPaneBg != nil && s.paintState.compose.HasPaneBg(paneInfo.PaneID) && s.tokens != nil {
		bg = s.tokens.Color(ui.ColorSubPaneBg)
	}
	trackRow := paneInfo.LineIdx
	if trackRow < 0 {
		trackRow = 0
	}
	if st.TrackH <= 0 {
		st.TrackH = st.Scroll.Viewport()
	}
	emphasized := s.paneHover == paneInfo.PaneID || (s.paneDrag != nil && s.paneDrag.paneID == paneInfo.PaneID)
	buf.Set(x, y, ui.ScrollbarCellState(st.Scroll, st.TrackH, trackRow, s.tokens, bg, emphasized))
}

func (s *Screen) updatePaneGutterHover(ctx ui.EventContext) bool {
	before := s.paneHover
	s.paneHover = ""
	if s.isNestedGutterHit(ctx.LocalX) {
		if info, ok := s.viewportPaneInfo(ctx.LocalY); ok {
			if _, visible := s.paneGutterState(info.PaneID); visible {
				s.paneHover = info.PaneID
			}
		}
	}
	return before != s.paneHover
}

func (s *Screen) ClearPaneGutterHover() bool {
	if s == nil || s.paneHover == "" {
		return false
	}
	s.paneHover = ""
	return true
}

func (s *Screen) handlePaneGutterClick(ctx ui.EventContext, msg tea.MouseClickMsg) ui.EventResult {
	if msg.Button != tea.MouseLeft || !s.isNestedGutterHit(ctx.LocalX) {
		return ui.EventResult{}
	}
	info, ok := s.viewportPaneInfo(ctx.LocalY)
	if !ok {
		return ui.EventResult{}
	}
	st, ok := s.paneGutterState(info.PaneID)
	if !ok || st.Scroll.MaxOffset() <= 0 {
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	s.selection.Clear()
	trackLocalY, trackH, ok := s.paneTrackLocalY(info.PaneID, ctx.LocalY)
	if !ok {
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	control := ui.NewScrollbarControl(ui.ComponentID("shellchat/pane-scrollbar/"+info.PaneID), st.Scroll, s.tokens)
	control.Layout(ui.LayoutContext{}, ui.Rect{W: ui.ScrollbarWidth, H: trackH})
	res := control.Handle(ui.EventContext{LocalY: trackLocalY}, ui.MouseClickEvent(msg))
	if control.IsDragging() {
		s.paneDrag = &paneGutterDrag{paneID: info.PaneID, control: control}
	}
	s.syncPaneScrollAfterUser(info.PaneID)
	return res
}

func (s *Screen) handlePaneGutterMotion(ctx ui.EventContext, msg tea.MouseMotionMsg) ui.EventResult {
	if s.paneDrag == nil {
		return ui.EventResult{}
	}
	st, ok := s.paneGutterState(s.paneDrag.paneID)
	if !ok {
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	trackLocalY, trackH, ok := s.paneTrackLocalY(s.paneDrag.paneID, ctx.LocalY)
	if !ok {
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	s.paneDrag.control.SetScroll(st.Scroll)
	s.paneDrag.control.Layout(ui.LayoutContext{}, ui.Rect{W: ui.ScrollbarWidth, H: trackH})
	res := s.paneDrag.control.Handle(ui.EventContext{LocalY: trackLocalY}, ui.MouseMotionEvent(msg))
	s.syncPaneScrollAfterUser(s.paneDrag.paneID)
	return res
}

func (s *Screen) handlePaneGutterRelease(ctx ui.EventContext, msg tea.MouseReleaseMsg) ui.EventResult {
	if s.paneDrag == nil {
		return ui.EventResult{}
	}
	res := s.paneDrag.control.Handle(ui.EventContext{LocalY: ctx.LocalY}, ui.MouseReleaseEvent(msg))
	s.paneDrag = nil
	return res
}

func (s *Screen) handlePaneWheel(ctx ui.EventContext, msg tea.MouseWheelMsg) ui.EventResult {
	info, ok := s.viewportPaneInfo(ctx.LocalY)
	if !ok {
		return ui.EventResult{}
	}
	gx := s.nestedGutterX()
	if gx >= 0 && ctx.LocalX >= gx+ui.ScrollbarWidth {
		// The blank separator and main scrollbar belong to the outer chat.
		return ui.EventResult{}
	}
	if s.isNestedGutterHit(ctx.LocalX) {
		return s.handlePaneGutterWheel(info.PaneID, msg)
	}
	// Wheel over pane content scrolls that pane only.
	if _, ok := s.paneGutterState(info.PaneID); !ok {
		return ui.EventResult{}
	}
	return s.handlePaneGutterWheel(info.PaneID, msg)
}

func (s *Screen) handlePaneGutterWheel(paneID string, msg tea.MouseWheelMsg) ui.EventResult {
	st, ok := s.paneGutterState(paneID)
	if !ok {
		return ui.EventResult{}
	}
	scroll := st.Scroll
	switch msg.Button {
	case tea.MouseWheelUp:
		n := s.paneWheel.WheelUp()
		if n > 0 {
			n = -n
		}
		scroll.ScrollBy(n)
		scroll.SetAutoScroll(false)
	case tea.MouseWheelDown:
		n := s.paneWheel.WheelDown()
		scroll.ScrollBy(n)
		if scroll.ShouldAutoScroll() {
			scroll.StickToBottom()
		}
	default:
		return ui.EventResult{}
	}
	s.syncPaneScrollAfterUser(paneID)
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
}

func (s *Screen) syncPaneScrollAfterUser(paneID string) {
	st, ok := s.paneGutterState(paneID)
	if !ok {
		return
	}
	if st.Scroll.NearBottom(0) {
		st.Scroll.SetAutoScroll(true)
	} else {
		st.Scroll.SetAutoScroll(false)
	}
}

func (s *Screen) IsPaneGutterDragging() bool {
	return s.paneDrag != nil
}

func (s *Screen) CancelPaneGutterCapture() {
	if s.paneDrag != nil && s.paneDrag.control != nil {
		s.paneDrag.control.CancelPointerCapture()
	}
	s.paneDrag = nil
}
