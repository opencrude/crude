package shellchat

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/chatprojection"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/conversation"
)

func TestFileReferenceClickAndSelectionUsePaintedCells(t *testing.T) {
	for _, width := range []int{40, 80, 160} {
		for _, prefix := range []string{"See ", "این پرونده را ببینید: "} {
			t.Run(fmt.Sprintf("%d/%s", width, prefix), func(t *testing.T) {
				th := theme.DefaultDark()
				renderer := chatprojection.NewRenderer(th)
				renderer.SetSize(width, 20)
				renderer.SetChatAreaBounds(0, 20)
				renderer.AppendMessage(conversation.ChatMessage{InternalID: "reply", Role: conversation.RoleAssistant,
					Content: prefix + "`src/main.go:12:3`"})
				rows, _, height := renderer.ExportMessageRows(width)
				screen := New(th)
				screen.SetMessages(rows, PaintParams{SourceID: 1, Compose: ComposeParams{
					InnerW: width - 2*th.Spacing.PadLR, PadLR: th.Spacing.PadLR, Theme: th}})
				screen.SyncScrollMetrics(height, height)
				buf := screen.PaintTestBuffer(width, height)
				x, y := -1, -1
				for row := 0; row < height; row++ {
					for col := 0; col < width; col++ {
						if buf.Get(col, row).Link.File() {
							x, y = col, row
							break
						}
					}
					if x >= 0 {
						break
					}
				}
				if x < 0 {
					t.Fatalf("no native file link was painted: %+v", rows)
				}
				ctx := ui.EventContext{LocalX: x, LocalY: y}
				click := func() ui.EventResult {
					screen.Handle(ctx, ui.MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
					return screen.Handle(ctx, ui.MouseReleaseEvent(tea.MouseReleaseMsg{Button: tea.MouseLeft}))
				}
				result := click()
				if result.Action == nil || result.Action.ID != chatprojection.ChatFileOpenActionID || result.Action.Payload != "src/main.go:12:3" {
					t.Fatalf("painted file cell did not open its exact target: %+v", result)
				}
				screen.Handle(ctx, ui.MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
				screen.Handle(ui.EventContext{LocalX: x + 2, LocalY: y}, ui.MouseMotionEvent(tea.MouseMotionMsg{Button: tea.MouseLeft}))
				screen.Handle(ctx, ui.MouseMotionEvent(tea.MouseMotionMsg{Button: tea.MouseLeft}))
				if result := screen.Handle(ctx, ui.MouseReleaseEvent(tea.MouseReleaseMsg{Button: tea.MouseLeft})); result.Action != nil {
					t.Fatal("dragging away and back opened a file")
				}
				screen.Handle(ctx, ui.MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
				screen.SetMessages(rows, PaintParams{SourceID: 2, Compose: ComposeParams{InnerW: width, Theme: th}})
				if result := screen.Handle(ctx, ui.MouseReleaseEvent(tea.MouseReleaseMsg{Button: tea.MouseLeft})); result.Action != nil {
					t.Fatal("a replacement source retained a pressed file link")
				}
			})
		}
	}
}

func BenchmarkDenseFileLinkHit(b *testing.B) {
	for _, count := range []int{100, 20000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			rows := make([]chatprojection.MessageRow, count)
			action := &ui.Action{ID: chatprojection.ChatFileOpenActionID, Payload: "src/main.go"}
			for i := range rows {
				copyText := &rendering.CopyText{Value: "src/main.go"}
				rows[i] = chatprojection.MessageRow{ID: fmt.Sprint(i), Lines: []string{"src/main.go"},
					Native: []chatprojection.PaintLine{{SourceAware: true, Spans: []chatprojection.RowSpan{{Text: copyText.Value,
						Copy: rendering.CopyRange{Text: copyText, Start: 0, End: len(copyText.Value)}, Action: action}}}}}
			}
			screen := New(theme.DefaultDark())
			screen.width, screen.height = 80, 1
			screen.SetMessages(rows, PaintParams{SourceID: 1, Compose: ComposeParams{InnerW: 80}})
			screen.SyncScrollMetrics(1, count)
			screen.ScrollModel().ScrollTo(count - 1)
			ctx := ui.EventContext{LocalX: 2}
			b.ReportAllocs()
			for b.Loop() {
				screen.handleSelectionClick(ctx, tea.MouseClickMsg{Button: tea.MouseLeft})
				if result := screen.handleSelectionRelease(ctx, tea.MouseReleaseMsg{Button: tea.MouseLeft}); result.Action != action {
					b.Fatal("wrong target")
				}
			}
		})
	}
}

func TestWideLinkContinuationClickIsNotASelection(t *testing.T) {
	action := &ui.Action{ID: chatprojection.ChatWebOpenActionID, Payload: "https://example.test"}
	s, h := newShellChatHost(t, []chatprojection.MessageRow{{ID: "wide", Lines: []string{"界"}, Native: []chatprojection.PaintLine{{Spans: []chatprojection.RowSpan{{Text: "界", Action: action}}}}}}, ComposeParams{InnerW: 40, Theme: theme.DefaultDark()}, 1)
	x := s.layout.padLR + 1
	h.Handle(tea.MouseClickMsg{X: x, Button: tea.MouseLeft})
	r := h.Handle(tea.MouseReleaseMsg{X: x, Button: tea.MouseLeft})
	if r.Action != action || s.IsSelecting() || s.SelectedText() != "" {
		t.Fatal("clicking the second cell of a link must activate it without selecting")
	}
}
