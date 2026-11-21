package chatprojection

import (
	"strconv"
	"strings"

	"crude/tui/bidi"
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
// Right-to-left text in the row is arranged after it is painted.
func PaintLineAtBackground(buf *surface.CellBuffer, x, y, maxW int, pl PaintLine, tokens *ui.TokenSet, selStart, selEnd int, rowBg cell.Color) {
	PaintLineLogical(buf, x, y, maxW, pl, tokens, selStart, selEnd, rowBg)
	if buf != nil {
		buf.ApplyLine(x, y, ArrangedLine(pl, maxW))
	}
}

// PaintLineLogical paints a row in logical order without arranging it.
// A painter that adds decorations at logical columns (find matches) paints
// with it, adds them, and then arranges the row with ArrangedLine.
func PaintLineLogical(buf *surface.CellBuffer, x, y, maxW int, pl PaintLine, tokens *ui.TokenSet, selStart, selEnd int, rowBg cell.Color) {
	selection := [...]bidi.ColumnRange{{Start: selStart, End: selEnd}}
	PaintLineLogicalRanges(buf, x, y, maxW, pl, tokens, selection[:], rowBg)
}

// PaintLineLogicalRanges allows source selection to skip gaps between cells.
func PaintLineLogicalRanges(buf *surface.CellBuffer, x, y, maxW int, pl PaintLine, tokens *ui.TokenSet, selection []bidi.ColumnRange, rowBg cell.Color) {
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
			for len(selection) > 0 && selection[0].End <= col {
				selection = selection[1:]
			}
			if len(selection) > 0 && nextCol > selection[0].Start && col < selection[0].End && tokens != nil {
				// Selection preserves readable semantic/syntax ink.
				cellFG, cellBG = tokens.TextSelectionColors(fg)
				cellAttr &^= cell.AttrDim | cell.AttrReverse
			}

			curX = buf.WriteLogical(curX, y, cluster, cellFG, cellBG, cellAttr, span.Link)
			col = nextCol
		}
	}

	for col < maxW && curX < buf.Width() {
		cellFG := cell.NoColor
		cellBG := rowBg
		for len(selection) > 0 && selection[0].End <= col {
			selection = selection[1:]
		}
		if len(selection) > 0 && col >= selection[0].Start && col < selection[0].End && tokens != nil {
			cellFG, cellBG = tokens.TextSelectionColors(cellFG)
		}
		curX = buf.WriteLogical(curX, y, " ", cellFG, cellBG, 0)
		col++
	}
}

// ArrangedLine returns the arrangement of a row at most maxW columns wide,
// or nil when it paints as written. Mapped Markdown derives each display row
// from its retained paragraph or table cells. Fenced code and unmapped rows
// keep their own line boundaries.
func ArrangedLine(pl PaintLine, maxW int) *bidi.Line {
	if maxW <= 0 || !bidi.Current().Arrange {
		return nil
	}
	if pl.prose != nil {
		if pl.prose.blockWidth > 0 {
			maxW = min(maxW, pl.prose.blockWidth+pl.prose.shift)
		}
		return pl.prose.arrange(maxW)
	}
	if pl.mappedMarkdown {
		if pl.code {
			return arrangeSpans(pl, bidi.LeftToRight, maxW)
		}
		return nil
	}
	dir := bidi.LeftToRight
	for _, span := range pl.Spans {
		if span.Copy.Valid() {
			if span.Copy.Text.RTLAt(span.Copy.Start) {
				dir = bidi.RightToLeft
			}
			break
		}
	}
	return arrangeSpans(pl, dir, maxW)
}

func arrangeSpans(pl PaintLine, dir bidi.Direction, width int) *bidi.Line {
	if dir != bidi.RightToLeft {
		needed := false
		for _, span := range pl.Spans {
			if bidi.NeedsLayout(span.Text) {
				needed = true
				break
			}
		}
		if !needed {
			return nil
		}
	}
	var text strings.Builder
	// Inline code is isolated left to right (UTS #55 section 4.1.4), so
	// `./run.sh --all` keeps its order inside a Persian sentence. code
	// holds the byte range of each code span in the row text.
	var code [][2]int
	var codeText *rendering.CopyText
	codeIndex := -1
	for _, span := range TruncatePaintLine(pl, width).Spans {
		start := text.Len()
		text.WriteString(span.Text)
		index := -1
		if span.Copy.Valid() && span.Text != "" {
			index = span.Copy.Text.InlineCode(span.Copy.Start)
		}
		switch {
		case index < 0:
		case len(code) > 0 && codeText == span.Copy.Text && codeIndex == index && code[len(code)-1][1] == start:
			code[len(code)-1][1] = text.Len()
		default:
			code = append(code, [2]int{start, text.Len()})
		}
		codeText, codeIndex = span.Copy.Text, index
	}
	if len(code) == 0 {
		return bidi.TextLine(text.String(), dir, width, width)
	}
	row := text.String()
	var key strings.Builder
	key.WriteString(row)
	for _, r := range code {
		key.WriteString("\x00")
		key.WriteString(strconv.Itoa(r[0]))
		key.WriteByte(':')
		key.WriteString(strconv.Itoa(r[1]))
	}
	return bidi.CachedLine(key.String(), dir, width, width, func(dst []bidi.Unit) []bidi.Unit {
		units := bidi.Clusters(row, dst)
		offset, next := 0, 0
		for i := range units {
			end := offset + len(units[i].Text)
			for next < len(code) && code[next][1] <= offset {
				next++
			}
			if next < len(code) && offset >= code[next][0] && end <= code[next][1] {
				if offset == code[next][0] {
					units[i].Before = bidi.LRI
				}
				if end == code[next][1] {
					units[i].After = bidi.PDI
				}
			}
			offset = end
		}
		return units
	})
}
