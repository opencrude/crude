package shellchat

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/chatprojection"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
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
	res := s.Handle(ctx, ui.MouseClickEvent(tea.MouseClickMsg{X: ctx.LocalX, Y: ctx.LocalY, Button: tea.MouseLeft}))
	if !res.Handled {
		t.Fatal("click should be handled")
	}

	ctx.LocalX = th.Spacing.PadLR + 6
	ctx.LocalY = 0
	res = s.Handle(ctx, ui.MouseMotionEvent(tea.MouseMotionMsg{X: ctx.LocalX, Y: ctx.LocalY, Button: tea.MouseLeft}))
	if !res.Handled || !s.IsSelecting() {
		t.Fatal("motion during drag should be handled")
	}

	ctx.LocalX = th.Spacing.PadLR + 5
	res = s.Handle(ctx, ui.MouseReleaseEvent(tea.MouseReleaseMsg{X: ctx.LocalX, Y: ctx.LocalY, Button: tea.MouseLeft}))
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

func TestContextBoundariesUseSharedDragSelectionAndCopy(t *testing.T) {
	for _, test := range []struct {
		name  string
		lines []string
		want  string
	}{
		{name: "compaction", lines: []string{"", "", "Context compacted", "Source 420K", "Continuation installed", ""}, want: "Source 420K"},
		{name: "restore", lines: []string{"", "", "Workspace restored", "2 files restored", "Conversation retained", ""}, want: "2 files restored"},
	} {
		t.Run(test.name, func(t *testing.T) {
			th := theme.DefaultDark()
			s := New(th)
			native := make([]chatprojection.PaintLine, len(test.lines))
			for i, line := range test.lines {
				native[i] = chatprojection.PaintLine{Spans: []chatprojection.RowSpan{{Text: line, Role: ui.ColorFg}}}
			}
			s.SetMessages([]chatprojection.MessageRow{{ID: test.name, Lines: test.lines, Native: native, ContextBoundary: true, ContextBoundaryStart: 1}}, PaintParams{
				Compose: ComposeParams{InnerW: 40, PadLR: th.Spacing.PadLR, Theme: th},
			})
			s.SyncScrollMetrics(len(test.lines), len(test.lines))
			s.Layout(ui.LayoutContext{}, ui.Rect{W: 40 + th.Spacing.PadLR*2, H: len(test.lines)})
			ctx := ui.EventContext{LocalX: th.Spacing.PadLR, LocalY: 2}
			_ = s.Handle(ctx, ui.MouseClickEvent(tea.MouseClickMsg{X: ctx.LocalX, Y: ctx.LocalY, Button: tea.MouseLeft}))
			ctx.LocalX = th.Spacing.PadLR + 40
			ctx.LocalY = 4
			_ = s.Handle(ctx, ui.MouseMotionEvent(tea.MouseMotionMsg{X: ctx.LocalX, Y: ctx.LocalY, Button: tea.MouseLeft}))
			if selected := s.SelectedText(); !strings.Contains(selected, test.want) {
				t.Fatalf("selected text=%q want %q", selected, test.want)
			}
			res := s.Handle(ctx, ui.MouseReleaseEvent(tea.MouseReleaseMsg{X: ctx.LocalX, Y: ctx.LocalY, Button: tea.MouseLeft}))
			if res.Cmd == nil {
				t.Fatal("boundary selection did not produce copy command")
			}
		})
	}
}

func TestShellChatDisclosureClickEmitsToggleAction(t *testing.T) {
	th := theme.DefaultDark()
	s := New(th)
	rows := []chatprojection.MessageRow{{
		ID: "thought-1", Lines: []string{"│ ● Thought for 2s ▸"}, Toggle: true, ToggleLine: 0,
	}}
	s.SetMessages(rows, PaintParams{Compose: ComposeParams{InnerW: 40, PadLR: th.Spacing.PadLR, Theme: th}})
	s.SyncScrollMetrics(1, 1)
	s.Layout(ui.LayoutContext{}, ui.Rect{W: 40 + th.Spacing.PadLR*2, H: 1})

	ctx := ui.EventContext{LocalX: th.Spacing.PadLR + 2, LocalY: 0}
	_ = s.Handle(ctx, ui.MouseClickEvent(tea.MouseClickMsg{X: ctx.LocalX, Y: 0, Button: tea.MouseLeft}))
	res := s.Handle(ctx, ui.MouseReleaseEvent(tea.MouseReleaseMsg{X: ctx.LocalX, Y: 0, Button: tea.MouseLeft}))
	if res.Action == nil || res.Action.ID != chatprojection.ChatToggleActionID {
		t.Fatalf("release action = %+v", res.Action)
	}
	payload, ok := res.Action.Payload.(chatprojection.ChatTogglePayload)
	if !ok || payload.MessageID != "thought-1" {
		t.Fatalf("toggle payload = %#v", res.Action.Payload)
	}
}

func TestExpandedDisclosureClickAnywhereCollapses(t *testing.T) {
	th := theme.DefaultDark()
	s := New(th)
	rows := []chatprojection.MessageRow{{
		ID: "thought-1", Lines: []string{"│ ● Thought for 2s ▾", "│   body row"},
		Toggle: true, ToggleLine: 0, ToggleAny: true,
	}}
	s.SetMessages(rows, PaintParams{Compose: ComposeParams{InnerW: 40, PadLR: th.Spacing.PadLR, Theme: th}})
	s.SyncScrollMetrics(2, 2)
	s.Layout(ui.LayoutContext{}, ui.Rect{W: 40 + th.Spacing.PadLR*2, H: 2})

	ctx := ui.EventContext{LocalX: th.Spacing.PadLR + 5, LocalY: 1}
	_ = s.Handle(ctx, ui.MouseClickEvent(tea.MouseClickMsg{X: ctx.LocalX, Y: 1, Button: tea.MouseLeft}))
	res := s.Handle(ctx, ui.MouseReleaseEvent(tea.MouseReleaseMsg{X: ctx.LocalX, Y: 1, Button: tea.MouseLeft}))
	if res.Action == nil || res.Action.ID != chatprojection.ChatToggleActionID {
		t.Fatalf("expanded body click action = %+v", res.Action)
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
	host := ui.NewHost(th, s)
	host.Resize(w, 3)
	host.Layout()
	scroll := s.ScrollModel()
	if scroll.MaxOffset() <= 0 {
		t.Fatal("expected scrollable content")
	}

	sbX := w - th.Spacing.PadLR - 1
	thumbTop, _ := scroll.ThumbBounds(s.scrollbar.Bounds().H)
	res := host.Handle(tea.MouseClickMsg{X: sbX, Y: thumbTop, Button: tea.MouseLeft})
	if !res.Handled {
		t.Fatal("scrollbar click should be handled")
	}
	before := scroll.Offset()
	res = host.Handle(tea.MouseMotionMsg{X: sbX, Y: 0, Button: tea.MouseLeft})
	if !res.Handled || !s.IsScrollbarDragging() {
		t.Fatal("scrollbar drag motion should be handled")
	}
	if scroll.Offset() == before && scroll.MaxOffset() > 1 {
		t.Fatalf("drag should change offset from %d", before)
	}
	_ = host.Handle(tea.MouseReleaseMsg{X: sbX, Y: 2, Button: tea.MouseLeft})
	if s.IsScrollbarDragging() {
		t.Fatal("drag should end on release")
	}
}

func TestCollapsedMultilineDisclosureClickAndDrag(t *testing.T) {
	th := theme.DefaultDark()
	s := New(th)
	rows := []chatprojection.MessageRow{{ID: "tool", Lines: []string{"", "long compact title", "continued title", "last title"}, Toggle: true, ToggleLine: 1}}
	s.SetMessages(rows, PaintParams{Compose: ComposeParams{InnerW: 40, PadLR: th.Spacing.PadLR, Theme: th}})
	s.SyncScrollMetrics(4, 4)
	s.Layout(ui.LayoutContext{}, ui.Rect{W: 40 + th.Spacing.PadLR*2, H: 4})
	for _, row := range []int{1, 2, 3} {
		ctx := ui.EventContext{LocalX: th.Spacing.PadLR + 2, LocalY: row}
		s.Handle(ctx, ui.MouseClickEvent(tea.MouseClickMsg{X: ctx.LocalX, Y: row, Button: tea.MouseLeft}))
		got := s.Handle(ctx, ui.MouseReleaseEvent(tea.MouseReleaseMsg{X: ctx.LocalX, Y: row, Button: tea.MouseLeft}))
		if got.Action == nil || got.Action.ID != chatprojection.ChatToggleActionID || got.Action.Payload.(chatprojection.ChatTogglePayload).MessageID != "tool" {
			t.Fatalf("line %d: %#v", row, got.Action)
		}
	}
	ctx := ui.EventContext{LocalX: th.Spacing.PadLR + 2, LocalY: 0}
	s.Handle(ctx, ui.MouseClickEvent(tea.MouseClickMsg{X: ctx.LocalX, Y: 0, Button: tea.MouseLeft}))
	if got := s.Handle(ctx, ui.MouseReleaseEvent(tea.MouseReleaseMsg{X: ctx.LocalX, Y: 0, Button: tea.MouseLeft})); got.Action != nil {
		t.Fatal("spacer toggled")
	}
	ctx.LocalY = 2
	s.Handle(ctx, ui.MouseClickEvent(tea.MouseClickMsg{X: ctx.LocalX, Y: 2, Button: tea.MouseLeft}))
	ctx.LocalX += 6
	s.Handle(ctx, ui.MouseMotionEvent(tea.MouseMotionMsg{X: ctx.LocalX, Y: 2, Button: tea.MouseLeft}))
	got := s.Handle(ctx, ui.MouseReleaseEvent(tea.MouseReleaseMsg{X: ctx.LocalX, Y: 2, Button: tea.MouseLeft}))
	if got.Action != nil || got.Cmd == nil {
		t.Fatal("drag must copy rather than expand")
	}
}

// Exercise actual compact plan projection, not only hand-built rows. Both a
// live and a hydrated plan must expand from the continuation and step lines.
func TestRenderedCompactPlanEveryLineExpands(t *testing.T) {
	message := types.ChatMessage{InternalID: "plan", Role: types.RoleTool, ToolName: "update_plan", ToolStatus: "success", ToolPreviewPhase: "done", Content: `{"id":"plan-1","revision":1,"objective":"A deliberately long objective that wraps across several rows of a narrow transcript","status":"active","steps":[{"id":"one","description":"First item","status":"completed"},{"id":"two","description":"Current item with detailed evidence that should be displayed when expanded","status":"in_progress"}]}`}
	for _, hydrated := range []bool{false, true} {
		th := theme.DefaultDark()
		const width = 42
		r := chatprojection.NewRenderer(th)
		r.SetSize(width, 30)
		r.SetChatAreaBounds(0, 30)
		if hydrated {
			r.SyncMessages([]types.ChatMessage{message}, 1, 0)
		} else {
			r.AppendMessage(message)
		}
		rows, _, h := r.ExportMessageRows(width)
		if len(rows) != 1 || !rows[0].Toggle || len(rows[0].Lines) < 4 {
			t.Fatalf("compact plan lacks multiline disclosure: %+v", rows)
		}
		screen := New(th)
		screen.SetMessages(rows, PaintParams{Compose: ComposeParams{InnerW: width - th.Spacing.PadLR*2, PadLR: th.Spacing.PadLR, Theme: th}})
		screen.SyncScrollMetrics(h, h)
		screen.Layout(ui.LayoutContext{}, ui.Rect{W: width, H: h})
		for line := rows[0].ToggleLine; line < len(rows[0].Lines); line++ {
			ctx := ui.EventContext{LocalX: th.Spacing.PadLR + 4, LocalY: line}
			screen.Handle(ctx, ui.MouseClickEvent(tea.MouseClickMsg{X: ctx.LocalX, Y: line, Button: tea.MouseLeft}))
			result := screen.Handle(ctx, ui.MouseReleaseEvent(tea.MouseReleaseMsg{X: ctx.LocalX, Y: line, Button: tea.MouseLeft}))
			if result.Action == nil || result.Action.ID != chatprojection.ChatToggleActionID {
				t.Fatalf("hydrated=%v row %d did not expand: %q", hydrated, line, rows[0].Lines[line])
			}
			if result.Action.Payload.(chatprojection.ChatTogglePayload).MessageID != "plan" {
				t.Fatalf("wrong message: %#v", result.Action.Payload)
			}
		}
		r.ToggleDisclosure("plan")
		expanded, _, _ := r.ExportMessageRows(width)
		if !strings.Contains(strings.Join(expanded[0].Lines, "\n"), "evidence that") {
			t.Fatalf("expanded plan lost full text: %q", expanded[0].Lines)
		}
	}
}
