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

			curX = buf.WriteLogical(curX, y, cluster, cellFG, cellBG, cellAttr, span.Link)
			col = nextCol
		}
	}

	for col < maxW && curX < buf.Width() {
		cellFG := cell.NoColor
		cellBG := rowBg
		if selStart < selEnd && col >= selStart && col < selEnd && tokens != nil {
			cellFG, cellBG = tokens.TextSelectionColors(cellFG)
		}
		curX = buf.WriteLogical(curX, y, " ", cellFG, cellBG, 0)
		col++
	}
}

// ArrangedLine returns the bidirectional arrangement of a row painted at
// most maxW columns wide, or nil when it paints as written. A row that
// shows source text takes the direction of that source paragraph, so every
// row of a Persian paragraph is right-aligned, including one that starts
// with an English word; code stays left-to-right. A row without source text
// is left-to-right with its right-to-left runs arranged in place.
func ArrangedLine(pl PaintLine, maxW int) *bidi.Line {
	if maxW <= 0 || !bidi.Current().Arrange {
		return nil
	}
	dir, needed := bidi.LeftToRight, false
	var source *rendering.CopyText
	structured := false
	for _, span := range pl.Spans {
		// The first source span decides; later spans share its paragraph.
		// A row that shows several source blocks (a table row) keeps its
		// columns left to right and arranges each run in place.
		if span.Copy.Valid() {
			switch {
			case source == nil:
				source = span.Copy.Text
				if source.RTLAt(span.Copy.Start) {
					dir = bidi.RightToLeft
				}
			case span.Copy.Text != source:
				structured = true
			}
		}
		if !needed && bidi.NeedsLayout(span.Text) {
			needed = true
		}
	}
	if structured {
		dir = bidi.LeftToRight
	}
	if !needed && dir == bidi.LeftToRight {
		return nil
	}
	var text strings.Builder
	// Inline code is isolated left to right (UTS #55 section 4.1.4), so
	// `./run.sh --all` keeps its order inside a Persian sentence. code
	// holds the byte range of each code span in the row text.
	var code [][2]int
	var codeText *rendering.CopyText
	codeIndex := -1
	for _, span := range TruncatePaintLine(pl, maxW).Spans {
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
		return bidi.TextLine(text.String(), dir, maxW, maxW)
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
	return bidi.CachedLine(key.String(), dir, maxW, maxW, func(dst []bidi.Unit) []bidi.Unit {
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
