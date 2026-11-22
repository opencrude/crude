package shellchat_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/chatprojection"
	"crude/tui/screens/shellchat"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/conversation"
)

// persianChat paints one assistant message through the real chat renderer
// and screen, and returns the screen, its painted buffer and the row that
// holds the message text.
func persianChat(t *testing.T, content string, width int) (*shellchat.Screen, *surface.CellBuffer, int) {
	t.Helper()
	th := theme.DefaultDark()
	renderer := chatprojection.NewRenderer(th)
	renderer.SetSize(width, 12)
	renderer.SetChatAreaBounds(0, 12)
	renderer.AppendMessage(conversation.ChatMessage{InternalID: "fa", Role: conversation.RoleAssistant, Content: content})
	rows, _, height := renderer.ExportMessageRows(width)
	screen := shellchat.New(th)
	screen.SetMessages(rows, shellchat.PaintParams{Compose: shellchat.ComposeParams{InnerW: width - 2*th.Spacing.PadLR, PadLR: th.Spacing.PadLR, Theme: th}})
	screen.SyncScrollMetrics(height, height)
	buf := screen.PaintTestBuffer(width, height)
	probe := []rune(content)[:3]
	for y := 0; y < height; y++ {
		if strings.Contains(surface.ReadLogical(buf.Row(y)), string(probe)) {
			return screen, buf, y
		}
	}
	t.Fatalf("message text is not painted in any row")
	return nil, nil, 0
}

// A short Persian message reads right to left and ends at the right edge of
// the content column. Padding painted after the row is arranged must not
// erase the text that arrangement moved there.
func TestChatPersianRowKeepsItsTextAtTheRightEdge(t *testing.T) {
	const width = 60
	_, buf, y := persianChat(t, "سلام دنیا", width)
	if got := strings.TrimSpace(surface.ReadLogical(buf.Row(y))); got != "سلام دنیا" {
		t.Fatalf("copied row = %q, want the message text", got)
	}
	padLR := theme.DefaultDark().Spacing.PadLR
	right := buf.Get(width-padLR-1, y)
	if right.Char == ' ' || right.Char == 0 || !right.Bidi.Arranged() {
		t.Fatalf("right edge of a right-to-left row is %q; the paragraph must end there", right.Char)
	}
	if left := buf.Get(padLR, y); left.Char != ' ' && left.Char != 0 {
		t.Fatalf("left edge of a short right-to-left row shows %q, want padding", left.Char)
	}
}

// Dragging over right-to-left text selects the characters under the
// pointer, exactly as dragging over left-to-right text does: the rightmost
// word on screen is the first word in the text, and the drag ends at the
// edge of the cell under the pointer that comes first in reading order.
func TestChatDragOverPersianSelectsTheCharactersShown(t *testing.T) {
	const width = 60
	padLR := theme.DefaultDark().Spacing.PadLR
	end := width - padLR // one past the last content column
	for _, tc := range []struct {
		text, want string
		from, to   int
	}{
		{"hello world", "hello", padLR, padLR + 5},
		// "سلام" fills the four cells left of the right edge; the drag ends
		// on the space before it on screen.
		{"سلام دنیا", "سلام", end - 1, end - 5},
	} {
		screen, buf, y := persianChat(t, tc.text, width)
		ctx := ui.EventContext{LocalX: tc.from, LocalY: y}
		screen.Handle(ctx, ui.MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
		ctx.LocalX = tc.to
		screen.Handle(ctx, ui.MouseMotionEvent(tea.MouseMotionMsg{Button: tea.MouseLeft}))
		if got := screen.SelectedText(); got != tc.want {
			t.Fatalf("%q: selected %q, want %q (row %q)", tc.text, got, tc.want, surface.ReadLogical(buf.Row(y)))
		}
		if result := screen.Handle(ctx, ui.MouseReleaseEvent(tea.MouseReleaseMsg{Button: tea.MouseLeft})); result.Cmd == nil {
			t.Fatalf("%q: selection has no copy command", tc.text)
		}
	}
}
