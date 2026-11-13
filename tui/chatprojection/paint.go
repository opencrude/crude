package chatprojection

import (
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"

	"github.com/charmbracelet/x/ansi"
)

// wrapPaintLine uses the canonical word/grapheme wrapping boundaries while
// retaining each semantic span, including continuation rows. WordWrap only
// drops boundary spaces; it never rewrites the remaining source text.
func wrapPaintLine(line PaintLine, width int) []PaintLine {
	plain := line.PlainText()
	var rows []PaintLine
	for _, wrapped := range rendering.WordWrapRanges(plain, max(1, width)) {
		start, end := wrapped.Start, wrapped.End
		row := line
		row.Spans = nil
		offset := 0
		for _, span := range line.Spans {
			spanEnd := offset + len(span.Text)
			from, to := max(start, offset), min(end, spanEnd)
			if from < to {
				span = span.Slice(from-offset, to-offset)
				row.Spans = append(row.Spans, span)
			}
			offset = spanEnd
			if offset >= end {
				break
			}
		}
		rows = append(rows, row)
	}
	return rows
}

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
		span = span.Slice(0, len(rendering.TruncateVisual(span.Text, remain, "")))
		out = append(out, span)
		break
	}
	pl.Spans = out
	return pl
}

// PaintLineAt writes a PaintLine into buf at (x,y), optionally applying text
// selection in plain-text column coordinates and subpane background fill.
func PaintLineAt(buf *surface.CellBuffer, x, y, maxW int, pl PaintLine, tokens *ui.TokenSet, selStart, selEnd int, subPaneBg bool) {
	rowBg := cell.NoColor
	if subPaneBg && tokens != nil {
		rowBg = tokens.Color(ui.ColorSubPaneBg)
	}
	PaintLineAtBackground(buf, x, y, maxW, pl, tokens, selStart, selEnd, rowBg)
}

// PaintLineAtBackground paints with an explicit full-row background. Chat
// context boundaries use it so text and internal spacing share one surface.
func PaintLineAtBackground(buf *surface.CellBuffer, x, y, maxW int, pl PaintLine, tokens *ui.TokenSet, selStart, selEnd int, rowBg cell.Color) {
	if buf == nil || y < 0 || y >= buf.Height() {
		return
	}
	if maxW < 0 {
		maxW = 0
	}
	pl = TruncatePaintLine(pl, maxW)

	col := 0
	curX := x
	for _, span := range pl.Spans {
		fg := cell.NoColor
		if span.FGSet {
			fg = span.FGColor
		} else if tokens != nil {
			fg = tokens.Color(span.Role)
		}
		spanBG := rowBg
		if span.BGSet {
			spanBG = span.BGColor
		} else if span.BgSet && tokens != nil {
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
			if pl.TextTide {
				cellFG, cellAttr = ui.TextTide(cellFG, cellAttr, tokens, pl.TideCenter, col)
			}
			if selStart < selEnd && nextCol > selStart && col < selEnd && tokens != nil {
				// Selection preserves readable semantic/syntax ink.
				cellFG, cellBG = tokens.TextSelectionColors(fg)
				cellAttr &^= cell.AttrDim | cell.AttrReverse
			}

			curX = buf.WriteString(curX, y, cluster, cellFG, cellBG, cellAttr, span.Link)
			col = nextCol
		}
	}

	for col < maxW && curX < buf.Width() {
		cellFG := cell.NoColor
		cellBG := rowBg
		if selStart < selEnd && col >= selStart && col < selEnd && tokens != nil {
			cellFG, cellBG = tokens.TextSelectionColors(cellFG)
		}
		curX = buf.WriteString(curX, y, " ", cellFG, cellBG, 0)
		col++
	}
}
