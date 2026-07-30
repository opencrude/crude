package shellchat

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/chatprojection"
	"crude/tui/surface"
	"crude/tui/ui"
)

type paneGutterDrag struct {
	paneID      string
	dragOffsetY int
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
	return gx >= 0 && localX == gx
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
	ui.PaintScrollbarCell(buf, x, y, st.Scroll, st.TrackH, trackRow, s.tokens, bg)
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
	top, th := st.Scroll.ThumbBounds(trackH)
	if trackLocalY >= top && trackLocalY < top+th {
		s.paneDrag = &paneGutterDrag{paneID: info.PaneID, dragOffsetY: trackLocalY - top}
	} else {
		s.scrollPaneGutterToLocalY(info.PaneID, trackLocalY, trackH)
		top, _ = st.Scroll.ThumbBounds(trackH)
		s.paneDrag = &paneGutterDrag{paneID: info.PaneID, dragOffsetY: trackLocalY - top}
		if s.paneDrag.dragOffsetY < 0 {
			s.paneDrag.dragOffsetY = 0
		}
	}
	s.syncPaneScrollAfterUser(info.PaneID)
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
}

func (s *Screen) handlePaneGutterMotion(ctx ui.EventContext, msg tea.MouseMotionMsg) ui.EventResult {
	if s.paneDrag == nil {
		return ui.EventResult{}
	}
	_, ok := s.paneGutterState(s.paneDrag.paneID)
	if !ok {
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	trackLocalY, trackH, ok := s.paneTrackLocalY(s.paneDrag.paneID, ctx.LocalY)
	if !ok {
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	s.dragPaneGutterToLocalY(s.paneDrag.paneID, trackLocalY, trackH, s.paneDrag.dragOffsetY)
	s.syncPaneScrollAfterUser(s.paneDrag.paneID)
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
}

func (s *Screen) handlePaneGutterRelease(_ ui.EventContext, _ tea.MouseReleaseMsg) ui.EventResult {
	if s.paneDrag == nil {
		return ui.EventResult{}
	}
	s.paneDrag = nil
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
}

func (s *Screen) scrollPaneGutterToLocalY(paneID string, localY, trackH int) {
	st, ok := s.paneGutterState(paneID)
	if !ok {
		return
	}
	scroll := st.Scroll
	if trackH <= 1 {
		scroll.ScrollTo(0)
		return
	}
	maxOff := scroll.MaxOffset()
	if maxOff <= 0 {
		return
	}
	if localY < 0 {
		localY = 0
	}
	if localY >= trackH {
		localY = trackH - 1
	}
	off := localY * maxOff / (trackH - 1)
	scroll.ScrollTo(off)
}

func (s *Screen) dragPaneGutterToLocalY(paneID string, localY, trackH, dragOffsetY int) {
	st, ok := s.paneGutterState(paneID)
	if !ok {
		return
	}
	scroll := st.Scroll
	maxOff := scroll.MaxOffset()
	if maxOff <= 0 || trackH <= 0 {
		return
	}
	_, th := scroll.ThumbBounds(trackH)
	adjustedY := localY - dragOffsetY
	if adjustedY < 0 {
		adjustedY = 0
	}
	available := trackH - th
	if available <= 0 {
		scroll.ScrollTo(0)
		return
	}
	if adjustedY > available {
		adjustedY = available
	}
	off := adjustedY * maxOff / available
	scroll.ScrollTo(off)
}

func (s *Screen) handlePaneWheel(ctx ui.EventContext, msg tea.MouseWheelMsg) ui.EventResult {
	info, ok := s.viewportPaneInfo(ctx.LocalY)
	if !ok {
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
	s.paneDrag = nil
}
