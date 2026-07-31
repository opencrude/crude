package shellchat_test

import (
	"strings"
	"testing"

	"crude/tui/chatprojection"
	"crude/tui/screens/shellchat"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

// benchRows builds messageCount messages with mixed plain/markdown-style
// native PaintLine rows (multi-span, like bridged glamour output).
func benchRows(messageCount, linesPerMsg int) []chatprojection.MessageRow {
	rows := make([]chatprojection.MessageRow, messageCount)
	for i := range rows {
		lines := make([]string, linesPerMsg)
		native := make([]chatprojection.PaintLine, linesPerMsg)
		for j := range lines {
			lines[j] = "agent message line with text and `code`"
			native[j] = chatprojection.PaintLine{Spans: []chatprojection.RowSpan{
				{Text: "agent message line with ", Role: ui.ColorFg},
				{Text: "text", Role: ui.ColorMuted},
				{Text: " and ", Role: ui.ColorFg},
				{Text: "`code`", Role: ui.ColorAccent},
			}}
		}
		rows[i] = chatprojection.MessageRow{ID: "m" + strings.Repeat("x", 1+i%4), Lines: lines, Native: native}
	}
	return rows
}

// benchPaintStream paints one streaming frame: reinstall rows (same IDs, one
// appended line) and repaint the Host into a retained buffer, matching the
// per-frame renderChatToSurface path.
func benchPaintStream(b *testing.B, w, h, messageCount, linesPerMsg int) {
	th := theme.DefaultDark()
	s := shellchat.New(th)
	rows := benchRows(messageCount, linesPerMsg)
	compose := shellchat.ComposeParams{
		InnerW:        w - 2*th.Spacing.PadLR - 1,
		PadLR:         th.Spacing.PadLR,
		Theme:         th,
		MainSBVisible: true,
	}
	paint := shellchat.PaintParams{Compose: compose}
	s.SetMessages(rows, paint)

	host := ui.NewHost(th, s)
	contentW := shellchat.ContentSurfaceWidth(compose.InnerW, th.Spacing.PadLR, true)
	host.Resize(contentW, h)
	host.Layout()

	buf := surface.NewCellBuffer(contentW, h)
	host.Paint(buf)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Streaming frame: last message grows by one line, identical IDs.
		last := rows[len(rows)-1]
		last.Lines = append(last.Lines, "streaming continuation line")
		last.Native = append(last.Native, chatprojection.PaintLine{
			Spans: []chatprojection.RowSpan{{Text: "streaming continuation line", Role: ui.ColorFg}},
		})
		rows[len(rows)-1] = last
		s.SetMessages(rows, paint)
		host.Layout()
		host.Paint(buf)
	}
}

// BenchmarkChatStreamPaint_80x24 measures one streaming frame paint with a
// short transcript at the minimum supported chat size.
func BenchmarkChatStreamPaint_80x24(b *testing.B) {
	benchPaintStream(b, 80, 24, 20, 3)
}

// BenchmarkChatStreamPaint_120x40 measures one streaming frame paint with a
// populated transcript at the standard product size.
func BenchmarkChatStreamPaint_120x40(b *testing.B) {
	benchPaintStream(b, 120, 40, 60, 4)
}
