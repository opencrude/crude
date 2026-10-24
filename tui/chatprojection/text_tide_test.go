package chatprojection

import (
	"strings"
	"testing"

	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"crude/types/reasoning"
)

func TestSummaryMarkdownFollowsThemeAndInvalidatesCachedPalette(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetSize(100, 30)
	r.SetChatAreaBounds(0, 20)
	r.SyncMessages([]types.ChatMessage{{InternalID: "summary", Role: types.RoleThinking, Content: "**Sample** has Markdown emphasis", ThinkingDisplay: reasoning.Display{Kind: reasoning.Summary}}}, 1, 0)
	for _, light := range []bool{false, true, false} {
		th := theme.DefaultDark()
		if light {
			th = theme.DefaultLight()
		}
		r.SetTheme(th)
		row := r.ExportMessageRowsSnapshot(100).Rows[0].Native[1]
		if strings.Contains(row.PlainText(), "…") {
			t.Fatalf("Markdown padding created false overflow: %q", row.PlainText())
		}
		x := rendering.VisibleWidth(row.PlainText()[:strings.Index(row.PlainText(), "Sample")])
		buf := surface.NewCellBuffer(100, 1)
		PaintLineAt(buf, 0, 0, 100, row, r.Tokens(), 0, 0, false)
		ink := buf.Get(x, 0).FG
		rgb := ink.Blend(ink, 0.5)
		brightness := (int(rgb.R) + int(rgb.G) + int(rgb.B)) / 3
		if light && brightness > 120 || !light && brightness < 160 {
			t.Fatalf("unreadable summary ink light=%v brightness=%d", light, brightness)
		}
	}
}

func TestTextTidePreservesGraphemesGeometryAndSelection(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		tokens := ui.NewTokenSet(th)
		pl := PaintLine{TextTide: true, Spans: []RowSpan{{Text: "Inspecting e\u0301 🧑‍💻 界 files", Role: ui.ColorActivityText}}}
		var motion ui.TextTideMotion
		var previous *surface.CellBuffer
		changed := false
		for frame := 0; frame < 10000; frame += 16 {
			buf := surface.NewCellBuffer(50, 1)
			pl.TideCenter = motion.Advance(frame, pl.VisibleWidth(), true)
			PaintLineAt(buf, 0, 0, 50, pl, tokens, 1, 3, false)
			if previous != nil {
				for x := 0; x < 50; x++ {
					a, b := previous.Get(x, 0), buf.Get(x, 0)
					if a.Char != b.Char || a.Content != b.Content || a.Width != b.Width || a.BG != b.BG {
						t.Fatalf("frame %d moved a grapheme at %d", frame, x)
					}
					changed = changed || a.FG != b.FG
				}
			}
			if buf.Get(1, 0).BG != tokens.Color(ui.ColorTextSelection) {
				t.Fatal("tide overwrote selection")
			}
			previous = buf
		}
		if !changed {
			t.Fatal("tide did not animate")
		}
	}
}

func BenchmarkTextTidePaint(b *testing.B) {
	tokens := ui.NewTokenSet(theme.DefaultDark())
	buf := surface.NewCellBuffer(120, 1)
	pl := PaintLine{TextTide: true, Spans: []RowSpan{{Text: "Thinking ▸ Inspecting the provider lifecycle and preserving every summary block", Role: ui.ColorMuted}}}
	var motion ui.TextTideMotion
	width := pl.VisibleWidth()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		pl.TideCenter = motion.Advance(i*16, width, true)
		PaintLineAt(buf, 0, 0, 120, pl, tokens, 0, 0, false)
	}
}
