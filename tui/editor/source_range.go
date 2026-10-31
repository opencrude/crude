package editor

import "unicode/utf8"

// SourceByteRange returns the ordered half-open UTF-8 source range of the
// current selection, or an empty range at the caret when there is no selection.
// Display endpoints must denote exact, unique grapheme/tab boundaries. Invalid,
// split or ambiguous endpoints and invalid source encoding are refused, never
// clamped or widened. This query does not change text, view state or history.
func (e *Editor) SourceByteRange() (start, end int, ok bool) {
	if e == nil || e.buf == nil {
		return 0, 0, false
	}
	// Existing line access lazily fills caches. Keep even that derived state
	// local to the query; all shared piece storage is read-only here.
	source := *e.buf
	source.ensureCache()
	if !utf8.Valid(source.cachedBytes) {
		return 0, 0, false
	}
	first, last := e.cursor, e.cursor
	if e.HasSelection() {
		sl, sc, el, ec := e.SelectionRange()
		first, last = Position{Line: sl, Col: sc}, Position{Line: el, Col: ec}
	}
	start, ok = source.exactSourceOffset(first)
	if !ok {
		return 0, 0, false
	}
	if first == last {
		return start, start, true
	}
	end, ok = source.exactSourceOffset(last)
	if !ok {
		return 0, 0, false
	}
	return start, end, true
}

// exactSourceOffset differs from positionToOffset: only exact, uniquely
// representable display boundaries are admitted. In particular, a zero-cell
// source unit can make one display column name multiple source byte offsets.
func (pt *PieceTable) exactSourceOffset(pos Position) (int, bool) {
	if pos.Line < 0 || pos.Col < 0 {
		return 0, false
	}
	content, err := pt.lineView(pos.Line)
	if err != nil {
		return 0, false
	}
	text := string(content)
	offset, col, found := 0, 0, -1
	for {
		if col == pos.Col {
			if found >= 0 {
				return 0, false
			}
			found = offset
		}
		if offset == len(text) || col > pos.Col {
			break
		}
		size, width := firstSourceUnit(text[offset:], col)
		if size <= 0 || width < 0 {
			return 0, false
		}
		offset += size
		col += width
	}
	if found < 0 {
		return 0, false
	}
	return pt.lineOffsets[pos.Line] + found, true
}
