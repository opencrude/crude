package shellchat

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/chatprojection"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestShellChatTextSelectionCopy(t *testing.T) {
	th := theme.DefaultDark()
	s := New(th)
	rows := []chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"hello world", "second line"}},
	}
	s.SetMessages(rows, PaintParams{
		Compose: ComposeParams{
			InnerW: 40,
			PadLR:  th.Spacing.PadLR,
			Theme:  th,
		},
	})
	s.SyncScrollMetrics(2, 2)
	s.Layout(ui.LayoutContext{}, ui.Rect{W: 40 + th.Spacing.PadLR*2, H: 2})

	ctx := ui.EventContext{LocalX: th.Spacing.PadLR + 1, LocalY: 0}
	res := s.Handle(ctx, tea.MouseClickMsg{X: ctx.LocalX, Y: ctx.LocalY, Button: tea.MouseLeft})
	if !res.Handled {
		t.Fatal("click should be handled")
	}

	ctx.LocalX = th.Spacing.PadLR + 6
	ctx.LocalY = 0
	res = s.Handle(ctx, tea.MouseMotionMsg{X: ctx.LocalX, Y: ctx.LocalY, Button: tea.MouseLeft})
	if !res.Handled || !s.IsSelecting() {
		t.Fatal("motion during drag should be handled")
	}

	ctx.LocalX = th.Spacing.PadLR + 5
	res = s.Handle(ctx, tea.MouseReleaseMsg{X: ctx.LocalX, Y: ctx.LocalY, Button: tea.MouseLeft})
	if !res.Handled {
		t.Fatal("release should be handled")
	}
	if res.Cmd == nil {
		t.Fatal("expected clipboard copy cmd on non-empty selection")
	}
	if s.IsSelecting() {
		t.Fatal("selection should end after release")
	}
}

func TestShellChatScrollbarDragScrolls(t *testing.T) {
	th := theme.DefaultDark()
	s := New(th)
	rows := []chatprojection.MessageRow{
		{ID: "m1", Lines: []string{"a", "b", "c", "d", "e", "f", "g", "h"}},
	}
	s.SetMessages(rows, PaintParams{
		Compose: ComposeParams{
			InnerW:        20,
			PadLR:         th.Spacing.PadLR,
			Theme:         th,
			MainSBVisible: true,
		},
	})
	s.SyncScrollMetrics(3, 8)
	w := 20 + th.Spacing.PadLR*2 + 1
	s.Layout(ui.LayoutContext{}, ui.Rect{W: w, H: 3})
	scroll := s.ScrollModel()
	if scroll.MaxOffset() <= 0 {
		t.Fatal("expected scrollable content")
	}

	sbX := w - th.Spacing.PadLR - 1
	ctx := ui.EventContext{LocalX: sbX, LocalY: 1}
	res := s.Handle(ctx, tea.MouseClickMsg{X: sbX, Y: 1, Button: tea.MouseLeft})
	if !res.Handled {
		t.Fatal("scrollbar click should be handled")
	}
	before := scroll.Offset()
	res = s.Handle(ctx, tea.MouseMotionMsg{X: sbX, Y: 2, Button: tea.MouseLeft})
	if !res.Handled || !s.IsScrollbarDragging() {
		t.Fatal("scrollbar drag motion should be handled")
	}
	if scroll.Offset() == before && scroll.MaxOffset() > 1 {
		t.Fatalf("drag should change offset from %d", before)
	}
	_ = s.Handle(ctx, tea.MouseReleaseMsg{X: sbX, Y: 2, Button: tea.MouseLeft})
	if s.IsScrollbarDragging() {
		t.Fatal("drag should end on release")
	}
}
