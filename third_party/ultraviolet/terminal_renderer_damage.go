package uv

// CellSpan is a half-open range of physical cells on one row. An empty span
// means no text write or erase. It can include gaps between writes.
type CellSpan struct{ Start, End int }

// RenderWithDamage queues the ordinary cell diff and records actual terminal
// writes, erases and shifts, not merely logical cell changes. Pass one span per
// row of the viewport. External graphics owners can then restore only images
// which the text diff could have damaged. Scrolling and cursor moves which
// rewrite unchanged cells are not used in this pass.
func (s *TerminalRenderer) RenderWithDamage(newbuf *RenderBuffer, rows []CellSpan) {
	clear(rows)
	s.damage = rows
	s.Render(newbuf)
	s.damage = nil
}

func (s *TerminalRenderer) recordDamage(y, start, end int) {
	if y < 0 || y >= len(s.damage) || start >= end {
		return
	}
	span := &s.damage[y]
	if span.Start == span.End {
		*span = CellSpan{Start: start, End: end}
		return
	}
	span.Start, span.End = min(span.Start, start), max(span.End, end)
}
