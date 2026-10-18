package shellchat

import (
	tea "charm.land/bubbletea/v2"
	"time"

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

func (s *Screen) paintNestedGutter(buf *surface.CellBuffer, x, y int, paneInfo chatprojection.PaneLineInfo, bg cell.Color) {
	if buf == nil || paneInfo.PaneID == "" {
		return
	}
	st, ok := s.paneGutterState(paneInfo.PaneID)
	if !ok {
		return
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

// ResetWheelGesture ends retained scroll ownership at a mount/input boundary.
func (s *Screen) ResetWheelGesture() {
	s.wheelGesture.Reset()
	s.wheel = ui.WheelAccelState{}
	s.wheelDirection = 0
	s.wheelModifiers = 0
	s.wheelChain = nil
	s.wheelModels = nil
}

func (s *Screen) handleNestedWheel(ctx ui.EventContext, msg tea.MouseWheelMsg) ui.EventResult {
	if msg.Mod != s.wheelModifiers {
		s.ResetWheelGesture()
		s.wheelModifiers = msg.Mod
	}
	sign := 0
	switch msg.Button {
	case tea.MouseWheelUp:
		sign = -1
	case tea.MouseWheelDown:
		sign = 1
	default:
		return ui.EventResult{}
	}
	const main = "chat"
	candidate := main
	var chain []string
	explicit := false
	gx := s.nestedGutterX()
	if info, ok := s.viewportPaneInfo(ctx.LocalY); ok && (gx < 0 || ctx.LocalX < gx+ui.ScrollbarWidth) {
		for _, target := range info.Ancestors {
			if target.LineIdx >= 0 {
				chain = append(chain, target.PaneID)
			}
		}
		if info.LineIdx >= 0 {
			chain = append(chain, info.PaneID)
		}
		for i := len(chain) - 1; i >= 0; i-- {
			if _, visible := s.paneGutterState(chain[i]); visible {
				candidate = "pane/" + chain[i]
				explicit = s.isNestedGutterHit(ctx.LocalX)
				break
			}
		}
	} else if gx >= 0 && ctx.LocalX >= gx+ui.ScrollbarWidth {
		explicit = true
	}
	now := time.Now()
	if s.wheelNow != nil {
		now = s.wheelNow()
	}
	owner, fresh := s.wheelGesture.Resolve(now, ctx.ScreenX, ctx.ScreenY, candidate, explicit)
	if fresh {
		s.wheel = ui.WheelAccelState{}
		s.wheelChain = chain
		s.wheelModels = make(map[string]*ui.ScrollModel, len(chain))
		for _, id := range chain {
			s.wheelModels[id] = s.paintState.compose.PaneGutters[id].Scroll
		}
	}
	if sign != s.wheelDirection {
		s.wheel = ui.WheelAccelState{}
	}
	s.wheelDirection = sign
	delta := s.wheel.WheelLines(sign)
	if owner != main {
		paneID := owner[len("pane/"):]
		start := -1
		for i, id := range s.wheelChain {
			if id == paneID {
				start = i
				break
			}
		}
		for i := start; i >= 0 && delta != 0; i-- {
			id := s.wheelChain[i]
			if st, ok := s.paneGutterState(id); ok && s.wheelModels[id] == st.Scroll && s.paneScrollVisible(id) {
				before := st.Scroll.Offset()
				delta = ui.ScrollWheelDelta(st.Scroll, delta)
				if st.Scroll.Offset() != before {
					s.syncPaneScrollAfterUser(id)
				}
				if delta == 0 {
					s.wheelGesture.Transfer("pane/" + id)
				}
			}
		}
		if delta != 0 {
			s.wheelGesture.Transfer(main)
		}
	}
	if delta != 0 {
		ui.ScrollWheelDelta(s.list.Scroll(), delta)
		s.syncScrollAfterUserScroll()
	}
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
}

func (s *Screen) paneScrollVisible(id string) bool {
	for _, info := range s.ViewportPaneLineMap() {
		if info.PaneID == id {
			return true
		}
		for _, ancestor := range info.Ancestors {
			if ancestor.PaneID == id {
				return true
			}
		}
	}
	return false
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
	if s.onPaneScroll != nil {
		s.onPaneScroll(paneID)
	}
	if s.onPaneScrollChain != nil {
		// The same exact ancestry invalidation serves wheel, track clicks and
		// captured thumb drags. Non-scrollable ancestors still own rendered rows.
		for _, info := range s.ViewportPaneLineMap() {
			chain := make([]string, 0, len(info.Ancestors)+1)
			found := false
			for _, target := range info.Ancestors {
				chain = append(chain, target.PaneID)
				if target.PaneID == paneID {
					found = true
					break
				}
			}
			if !found && info.PaneID == paneID {
				chain = append(chain, paneID)
				found = true
			}
			if found {
				s.onPaneScrollChain(chain)
				break
			}
		}
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
