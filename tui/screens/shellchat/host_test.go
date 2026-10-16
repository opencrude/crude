package shellchat

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/chatprojection"
	"crude/tui/theme"
	"crude/tui/ui"
)

func newShellChatHost(t *testing.T, rows []chatprojection.MessageRow, compose ComposeParams, viewportH int) (*Screen, *ui.Host) {
	t.Helper()
	th := theme.DefaultDark()
	if compose.Theme == nil {
		compose.Theme = th
	}
	if compose.PadLR == 0 {
		compose.PadLR = th.Spacing.PadLR
	}
	s := New(th)
	s.SetMessages(rows, PaintParams{Compose: compose, PaneLineMap: compose.PaneLineMap})
	contentH := 0
	for _, row := range rows {
		h := len(row.Lines)
		if h < 1 {
			h = 1
		}
		contentH += h
	}
	s.SyncScrollMetrics(viewportH, contentH)
	w := ContentSurfaceWidth(compose.InnerW, compose.PadLR, compose.MainSBVisible)
	h := viewportH
	host := ui.NewHost(th, s)
	host.Resize(w, h)
	host.Layout()
	return s, host
}

func TestShellChatHostTextSelectionCopy(t *testing.T) {
	th := theme.DefaultDark()
	padLR := th.Spacing.PadLR
	innerW := 40
	rows := []chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"hello world", "second line"}},
	}
	s, host := newShellChatHost(t, rows, ComposeParams{
		InnerW: innerW,
		PadLR:  padLR,
		Theme:  th,
	}, 2)

	clickX := padLR + 1
	host.Handle(tea.MouseClickMsg{X: clickX, Y: 0, Button: tea.MouseLeft})
	if !s.IsSelecting() {
		t.Fatal("host click should start selection via screen")
	}

	host.Handle(tea.MouseMotionMsg{X: padLR + 6, Y: 0, Button: tea.MouseLeft})
	if !s.IsSelecting() {
		t.Fatal("host motion should extend selection")
	}

	res := host.Handle(tea.MouseReleaseMsg{X: padLR + 5, Y: 0, Button: tea.MouseLeft})
	if !res.Handled {
		t.Fatal("host release should be handled")
	}
	if res.Cmd == nil {
		t.Fatal("expected clipboard copy cmd on non-empty selection")
	}
	if s.IsSelecting() {
		t.Fatal("selection should end after release")
	}
}

func TestShellChatHostWheelDisarmsAutoScroll(t *testing.T) {
	th := theme.DefaultDark()
	lines := make([]string, 12)
	for i := range lines {
		lines[i] = "scrollable"
	}
	rows := []chatprojection.MessageRow{{ID: "m1", Lines: lines}}
	s, host := newShellChatHost(t, rows, ComposeParams{
		InnerW:        30,
		PadLR:         th.Spacing.PadLR,
		Theme:         th,
		MainSBVisible: true,
	}, 5)

	scroll := s.ScrollModel()
	scroll.StickToBottom()
	if !scroll.AutoScroll() {
		t.Fatal("expected auto-scroll armed")
	}

	host.Handle(tea.MouseWheelMsg{X: th.Spacing.PadLR + 2, Y: 2, Button: tea.MouseWheelUp})
	if scroll.AutoScroll() {
		t.Fatal("wheel up over message area should disarm auto-scroll")
	}
	if scroll.Offset() >= scroll.MaxOffset() {
		t.Fatal("wheel up should scroll away from bottom")
	}
}

func TestShellChatHostWheelDownStickToBottom(t *testing.T) {
	th := theme.DefaultDark()
	lines := make([]string, 12)
	for i := range lines {
		lines[i] = "scrollable"
	}
	s, host := newShellChatHost(t, []chatprojection.MessageRow{{ID: "m1", Lines: lines}}, ComposeParams{
		InnerW:        30,
		PadLR:         th.Spacing.PadLR,
		Theme:         th,
		MainSBVisible: true,
	}, 5)

	scroll := s.ScrollModel()
	scroll.SetAutoScroll(false)
	scroll.ScrollTo(0)

	host.Handle(tea.MouseWheelMsg{X: th.Spacing.PadLR + 2, Y: 2, Button: tea.MouseWheelDown})
	for i := 0; i < 20 && scroll.Offset() < scroll.MaxOffset(); i++ {
		host.Handle(tea.MouseWheelMsg{X: th.Spacing.PadLR + 2, Y: 2, Button: tea.MouseWheelDown})
	}
	if scroll.Offset() != scroll.MaxOffset() {
		t.Fatalf("wheel down to bottom offset=%d max=%d", scroll.Offset(), scroll.MaxOffset())
	}
	if !scroll.AutoScroll() {
		t.Fatal("wheel down at bottom should re-arm auto-scroll")
	}
}

func TestShellChatHostScrollbarDragUpdatesScrollModel(t *testing.T) {
	th := theme.DefaultDark()
	padLR := th.Spacing.PadLR
	innerW := 20
	lines := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	s, host := newShellChatHost(t, []chatprojection.MessageRow{{ID: "m1", Lines: lines}}, ComposeParams{
		InnerW:        innerW,
		PadLR:         padLR,
		Theme:         th,
		MainSBVisible: true,
	}, 3)

	scroll := s.ScrollModel()
	if scroll.MaxOffset() <= 0 {
		t.Fatal("expected scrollable content")
	}
	sb := s.scrollbar.Bounds()
	sbScreenX := sb.X
	thumbTop, _ := scroll.ThumbBounds(sb.H)
	thumbY := sb.Y + thumbTop

	host.Handle(tea.MouseClickMsg{X: sbScreenX, Y: thumbY, Button: tea.MouseLeft})
	if !s.IsScrollbarDragging() {
		t.Fatal("host click on gutter should start scrollbar drag")
	}
	before := scroll.Offset()
	host.Handle(tea.MouseMotionMsg{X: sbScreenX, Y: 0, Button: tea.MouseLeft})
	if !s.IsScrollbarDragging() {
		t.Fatal("scrollbar drag should continue on motion")
	}
	if scroll.Offset() == before && scroll.MaxOffset() > 1 {
		t.Fatalf("drag should change offset from %d", before)
	}
	host.Handle(tea.MouseReleaseMsg{X: sbScreenX, Y: 2, Button: tea.MouseLeft})
	if s.IsScrollbarDragging() {
		t.Fatal("drag should end on release")
	}
}

func TestShellChatHostVirtualListDoesNotStealPointer(t *testing.T) {
	th := theme.DefaultDark()
	s, host := newShellChatHost(t, []chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"pick me"}},
	}, ComposeParams{InnerW: 30, PadLR: th.Spacing.PadLR, Theme: th}, 1)

	host.Handle(tea.MouseClickMsg{X: th.Spacing.PadLR + 1, Y: 0, Button: tea.MouseLeft})
	if !s.IsSelecting() {
		t.Fatal("screen should own host-routed click for text selection")
	}
	if host.Focused() != ScreenID {
		t.Fatalf("focus=%q want screen owner %q", host.Focused(), ScreenID)
	}
}

func TestShellChatHostSelectionCrossesScrollbar(t *testing.T) {
	th := theme.DefaultDark()
	padLR := th.Spacing.PadLR
	innerW := 30
	rows := []chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"hello scrollbar cross"}},
	}
	s, host := newShellChatHost(t, rows, ComposeParams{
		InnerW:        innerW,
		PadLR:         padLR,
		Theme:         th,
		MainSBVisible: true,
	}, 1)

	clickX := padLR + 1
	host.Handle(tea.MouseClickMsg{X: clickX, Y: 0, Button: tea.MouseLeft})
	if !host.HasPointerCapture() {
		t.Fatal("selection click should capture pointer on host")
	}
	if !s.IsSelecting() {
		t.Fatal("click should start selection")
	}

	sbX := s.scrollbar.Bounds().X
	host.Handle(tea.MouseMotionMsg{X: sbX, Y: 0, Button: tea.MouseLeft})
	if !s.IsSelecting() {
		t.Fatal("selection must continue when motion crosses into scrollbar column")
	}
	if s.IsScrollbarDragging() {
		t.Fatal("scrollbar drag must not preempt captured selection")
	}

	res := host.Handle(tea.MouseReleaseMsg{X: sbX, Y: 0, Button: tea.MouseLeft})
	if !res.Handled || host.HasPointerCapture() {
		t.Fatal("release should complete selection and clear capture")
	}
	if s.IsSelecting() {
		t.Fatal("selection should end after release")
	}
}

func TestShellChatHostScrollbarDragCrossesList(t *testing.T) {
	th := theme.DefaultDark()
	padLR := th.Spacing.PadLR
	innerW := 20
	lines := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	s, host := newShellChatHost(t, []chatprojection.MessageRow{{ID: "m1", Lines: lines}}, ComposeParams{
		InnerW:        innerW,
		PadLR:         padLR,
		Theme:         th,
		MainSBVisible: true,
	}, 3)

	scroll := s.ScrollModel()
	sbX := s.scrollbar.Bounds().X
	thumbTop, _ := scroll.ThumbBounds(s.scrollbar.Bounds().H)
	host.Handle(tea.MouseClickMsg{X: sbX, Y: s.scrollbar.Bounds().Y + thumbTop, Button: tea.MouseLeft})
	if !host.HasPointerCapture() {
		t.Fatal("scrollbar click should capture pointer on host")
	}
	if !s.IsScrollbarDragging() {
		t.Fatal("expected scrollbar drag")
	}
	before := scroll.Offset()

	listX := padLR + 1
	host.Handle(tea.MouseMotionMsg{X: listX, Y: 0, Button: tea.MouseLeft})
	if !s.IsScrollbarDragging() {
		t.Fatal("scrollbar drag must continue when pointer leaves gutter")
	}
	if scroll.Offset() == before && scroll.MaxOffset() > 1 {
		t.Fatalf("drag motion over list should still scroll, offset=%d", scroll.Offset())
	}

	host.Handle(tea.MouseReleaseMsg{X: listX, Y: 2, Button: tea.MouseLeft})
	if host.HasPointerCapture() || s.IsScrollbarDragging() {
		t.Fatal("release should end drag and clear capture")
	}
}

// TestShellChatHostScrollbarDragUsesExplicitCapture proves the migrated
// scrollbar thumb drag requests capture through the explicit ui.capture
// service (CapturedPointer reports the scrollbar as owner) and that the drag
// continues outside the gutter and ends reliably on release, meeting exit criterion
// #3 through the real Host dispatch path.
func TestShellChatHostScrollbarDragUsesExplicitCapture(t *testing.T) {
	th := theme.DefaultDark()
	lines := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	s, host := newShellChatHost(t, []chatprojection.MessageRow{{ID: "m1", Lines: lines}}, ComposeParams{
		InnerW:        20,
		PadLR:         th.Spacing.PadLR,
		Theme:         th,
		MainSBVisible: true,
	}, 3)

	scroll := s.ScrollModel()
	sbX := s.scrollbar.Bounds().X
	thumbTop, _ := scroll.ThumbBounds(s.scrollbar.Bounds().H)
	res := host.Handle(tea.MouseClickMsg{X: sbX, Y: s.scrollbar.Bounds().Y + thumbTop, Button: tea.MouseLeft})
	if !res.Handled {
		t.Fatal("scrollbar click should be handled")
	}
	owner, btn, ok := host.CapturedPointer()
	if !ok || owner != ScrollbarID || btn != tea.MouseLeft {
		t.Fatalf("explicit capture owner=%q btn=%v ok=%v want %q left", owner, btn, ok, ScrollbarID)
	}
	if !s.IsScrollbarDragging() {
		t.Fatal("click should start scrollbar drag")
	}

	// Drag continues outside the scrollbar column (over the message list).
	listX := th.Spacing.PadLR + 1
	before := scroll.Offset()
	host.Handle(tea.MouseMotionMsg{X: listX, Y: 0, Button: tea.MouseLeft})
	if !s.IsScrollbarDragging() {
		t.Fatal("scrollbar drag must continue when pointer leaves the gutter")
	}
	if scroll.Offset() == before && scroll.MaxOffset() > 1 {
		t.Fatalf("drag motion over the list should still scroll, offset=%d", scroll.Offset())
	}

	// Release reliably ends the drag and clears capture.
	host.Handle(tea.MouseReleaseMsg{X: listX, Y: 2, Button: tea.MouseLeft})
	if host.HasPointerCapture() {
		t.Fatal("release should clear host capture")
	}
	if s.IsScrollbarDragging() {
		t.Fatal("release should end scrollbar drag")
	}
}
