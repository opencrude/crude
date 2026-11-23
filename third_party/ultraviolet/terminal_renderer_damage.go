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

// HoldArea keeps what the terminal shows in area through the next Render by
// copying the physical cells into newbuf, so the diff leaves them alone. An
// external graphics owner uses it to keep the previous picture until its
// replacement is ready instead of presenting an intermediate blank state.
// Unknown, invalidated and wide cells are not held, and nothing is held while
// a full erase is pending or the geometry differs.
func (s *TerminalRenderer) HoldArea(newbuf *RenderBuffer, area Rectangle) {
	if s.clear || s.curbuf == nil || newbuf == nil || s.curbuf.Bounds() != newbuf.Bounds() {
		return
	}
	area = area.Intersect(newbuf.Bounds())
	for y := area.Min.Y; y < area.Max.Y; y++ {
		held, line := s.curbuf.Lines[y], newbuf.Lines[y]
		for x := area.Min.X; x < area.Max.X; x++ {
			if old := held[x]; old.Width == 1 && old.Content != "\x00" && line[x].Width == 1 {
				line[x] = old
			}
		}
	}
}
