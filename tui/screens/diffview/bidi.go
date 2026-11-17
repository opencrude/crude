package diffview

import (
	"strconv"

	"crude/tui/bidi"
)

// Window arranges the visible columns [from, from+width) of source line
// number for bidirectional text (docs/reference/bidi.md). A source line is a
// left-to-right paragraph, as in the editor's default; right-to-left runs
// read right to left in place. Comparison painters write the window's units
// in logical order with WriteLogical and then apply this line, so selection
// and match fills travel with their characters; pointer hits map a screen
// column back through Logical. It is nil when the window paints as written.
func (source SourceLines) Window(number, from, width int) *bidi.Line {
	if source.owner == nil || number <= 0 || number > len(source.owner.lines) || width <= 0 {
		return nil
	}
	text := source.owner.lines[number-1]
	if !bidi.NeedsLayout(text) {
		return nil
	}
	key := strconv.Itoa(from) + "\x00" + text
	return bidi.CachedLine(key, bidi.LeftToRight, width, width, func(dst []bidi.Unit) []bidi.Unit {
		at := 0
		cursor := source.cursor(number, from, from+width)
		for unit, ok := cursor.next(); ok; unit, ok = cursor.next() {
			left := unit.column - from
			if left < 0 {
				continue
			}
			if left+unit.width > width {
				break
			}
			// A wide grapheme cut by the window leaves blank cells.
			for ; at < left; at++ {
				dst = append(dst, bidi.Unit{Text: " ", Width: 1})
			}
			// The painted glyph is unit.display (a tab shows as spaces);
			// the algorithm reads the source grapheme.
			dst = append(dst, bidi.Unit{Text: unit.raw, Width: unit.width})
			at += unit.width
		}
		return dst
	})
}
