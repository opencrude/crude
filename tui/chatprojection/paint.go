package chatprojection

import (
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"

	"github.com/charmbracelet/x/ansi"
)

// TruncatePaintLine truncates visible content to maxW grapheme columns while
// preserving span boundaries where possible.
func TruncatePaintLine(pl PaintLine, maxW int) PaintLine {
	if maxW < 0 {
		maxW = 0
	}
	if pl.VisibleWidth() <= maxW {
		return pl
	}
	var out []RowSpan
	remain := maxW
	for _, span := range pl.Spans {
		if remain <= 0 {
			break
		}
		w := rendering.VisibleWidth(span.Text)
		if w <= remain {
			out = append(out, span)
			remain -= w
			continue
		}
		out = append(out, RowSpan{
			Text: rendering.TruncateVisual(span.Text, remain, ""),
			Role: span.Role,
			Attr: span.Attr,
		})
		break
	}
	return PaintLine{Spans: out}
}

// PaintLineAt writes a PaintLine into buf at (x,y), optionally applying text
// selection in plain-text column coordinates and subpane background fill.
func PaintLineAt(buf *surface.CellBuffer, x, y, maxW int, pl PaintLine, tokens *ui.TokenSet, selStart, selEnd int, subPaneBg bool) {
	if buf == nil || y < 0 || y >= buf.Height() {
		return
	}
	if maxW < 0 {
		maxW = 0
	}
	pl = TruncatePaintLine(pl, maxW)

	rowBg := cell.NoColor
	if subPaneBg && tokens != nil {
		rowBg = tokens.Color(ui.ColorSubPaneBg)
	}

	col := 0
	curX := x
	for _, span := range pl.Spans {
		fg := cell.NoColor
		if tokens != nil {
			fg = tokens.Color(span.Role)
		}
		spanBG := rowBg
		if span.BgSet && tokens != nil {
			spanBG = tokens.Color(span.BgRole)
		}
		rest := span.Text
		for len(rest) > 0 {
			cluster, w := ansi.FirstGraphemeCluster(rest, ansi.GraphemeWidth)
			if len(cluster) == 0 {
				break
			}
			rest = rest[len(cluster):]
			if w == 0 {
				continue
			}
			if col >= maxW {
				break
			}
			nextCol := col + w
			if nextCol > maxW {
				break
			}

			cellFG := fg
			cellBG := spanBG
			cellAttr := span.Attr
			if selStart < selEnd && nextCol > selStart && col < selEnd && tokens != nil {
				cellFG, cellBG = tokens.ListRowColors(ui.StateFocused)
			}

			curX = buf.WriteString(curX, y, cluster, cellFG, cellBG, cellAttr)
			col = nextCol
		}
	}

	for col < maxW && curX < buf.Width() {
		cellFG := rowBg
		cellBG := rowBg
		if subPaneBg && tokens != nil {
			cellFG = tokens.Color(ui.ColorFg)
			cellBG = tokens.Color(ui.ColorSubPaneBg)
		}
		if selStart < selEnd && col >= selStart && col < selEnd && tokens != nil {
			cellFG, cellBG = tokens.ListRowColors(ui.StateFocused)
		}
		curX = buf.WriteString(curX, y, " ", cellFG, cellBG, 0)
		col++
	}
}
