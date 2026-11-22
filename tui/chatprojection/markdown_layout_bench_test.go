package chatprojection

import (
	"strings"
	"testing"

	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/conversation"
)

const mixedBenchmarkBody = "## نتیجه\n\n> نسخه Go 1.24 را بررسی کنید.\n\n" +
	"12. پیکربندی `./run.sh --check` را بررسی کنید.\n13. Test the result.\n\n" +
	"| داده | API |\n| --- | --- |\n| آزمایش | v1.2 |\n\n"

func BenchmarkMixedMarkdownLayout(b *testing.B) {
	th := theme.DefaultDark()
	md, tokens := rendering.MakeMdRenderer(80, th), ui.NewTokenSet(th)
	source := strings.Repeat(mixedBenchmarkBody, 4)
	b.Run("Reflow", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			rows, _ := renderMarkdownRows(source, md, tokens, "m", false)
			for _, row := range rows {
				ArrangedLine(row, 96)
			}
		}
	})
	b.Run("Retained", func(b *testing.B) {
		rows, _ := renderMarkdownRows(source, md, tokens, "m", false)
		for _, row := range rows {
			ArrangedLine(row, 96)
		}
		b.ReportAllocs()
		for b.Loop() {
			for _, row := range rows {
				ArrangedLine(row, 96)
			}
		}
	})
}

func BenchmarkMixedMarkdownStreamingBlink(b *testing.B) {
	r := NewRenderer(theme.DefaultDark())
	r.SetSize(84, 40) // 80 document columns, just like Reflow.
	r.SetChatAreaBounds(0, 40)
	r.AppendMessage(conversation.ChatMessage{InternalID: "m", Role: conversation.RoleAssistant, Content: mixedBenchmarkBody + strings.Repeat("نتیجه API 1.24 ", 18)})
	r.SetStreaming(true)
	msg := r.sync.Messages[0]
	r.renderSingleMessage(msg)
	b.ReportAllocs()
	for b.Loop() {
		r.spinFrame++
		_, rows := r.renderSingleMessage(msg)
		for _, row := range rows {
			ArrangedLine(row, 96)
		}
	}
}
