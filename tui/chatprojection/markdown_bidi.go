package chatprojection

import (
	"crude/tui/bidi"
	"crude/tui/rendering"
)

// A table line retains fixed cell boxes, plus the slices of the resolved
// paragraphs that Lip Gloss put on this display row. The gaps never take part
// in the text algorithm, even if every other cell on the row is empty.
func buildTableLine(geometry *rendering.MarkdownGeometry, spans []RowSpan, paragraphs map[*rendering.MarkdownCell]*proseParagraph) *proseLine {
	row := &proseLine{boxes: geometry.Cells}
	row.setBox(spans, geometry)
	col := 0
	for _, box := range row.boxes {
		start, end := box.ContentStart, box.ContentEnd
		p, known := paragraphs[box.Cell]
		if !known {
			if box.Cell.RTL || bidi.NeedsLayout(box.Cell.Source.String()) {
				p = &proseParagraph{source: box.Cell.Source.Text, last: box.Cell.Source.Start, code: -1}
			}
			paragraphs[box.Cell] = p
		}
		if p == nil || start >= end {
			continue
		}
		for len(spans) > 0 {
			next := col + rendering.VisibleWidth(spans[0].Text)
			if next > start {
				break
			}
			col, spans = next, spans[1:]
		}
		firstColumn := p.cols
		for i, x := 0, col; i < len(spans) && x < end; i++ {
			span := spans[i]
			w := rendering.VisibleWidth(span.Text)
			if x < start || x+w > end {
				from, to := max(0, start-x), min(end-x, w)
				prefix := len(rendering.ExtractVisibleRange(span.Text, 0, from))
				part := rendering.ExtractVisibleRange(span.Text, from, to)
				span = span.Slice(prefix, prefix+len(part))
			}
			if span.Text != "" {
				p.append(span)
			}
			x += w
		}
		if p.cols-firstColumn == end-start {
			row.pieces = append(row.pieces, prosePiece{paragraph: p, column: start, start: firstColumn, end: p.cols})
		}
	}
	return row
}
