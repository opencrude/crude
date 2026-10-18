package editor

import (
	"sort"
	"unicode/utf8"

	"crude/tui/rendering"
)

// firstSourceUnit measures one source editing unit. A tab is one byte and one
// movement unit even when its display projection contains several space cells.
func firstSourceUnit(text string, col int) (size, width int) {
	if text == "" {
		return 0, 0
	}
	if text[0] == '\t' {
		return 1, TabStopAdvance(col, tabWidth)
	}
	cluster, width := rendering.FirstGrapheme(text)
	return len(cluster), width
}

// positionAtOffset maps a source byte offset to a legal caret boundary using
// the complete line, never a truncated prefix that could split a grapheme.
// Offsets inside a grapheme or CRLF terminator map to its leading boundary.
func (pt *PieceTable) positionAtOffset(offset int) Position {
	pt.ensureCache()
	offset = min(len(pt.cachedBytes), max(0, offset))
	line := sort.Search(len(pt.lineOffsets), func(i int) bool {
		return pt.lineOffsets[i] > offset
	}) - 1
	content, _ := pt.lineView(line)
	return Position{Line: line, Col: ByteOffsetToVisualCol(string(content), offset-pt.lineOffsets[line], tabWidth)}
}

// snapPosition clamps to document content and floors columns inside a tab or
// wide grapheme to that source unit's leading boundary. Hit testing, vertical
// motion and editing must share this rule.
func (pt *PieceTable) snapPosition(pos Position) Position {
	return pt.positionAtOffset(pt.positionToOffset(pos))
}

func (pt *PieceTable) previousPosition(pos Position) Position {
	snapped := pt.snapPosition(pos)
	if pos.Line == snapped.Line && pos.Col > snapped.Col {
		return snapped
	}
	if snapped.Col == 0 {
		if snapped.Line == 0 {
			return snapped
		}
		content, _ := pt.lineView(snapped.Line - 1)
		return Position{Line: snapped.Line - 1, Col: LineVisualWidth(string(content), tabWidth)}
	}
	content, _ := pt.lineView(snapped.Line)
	text := string(content)
	col, previous := 0, 0
	for len(text) > 0 && col < snapped.Col {
		size, width := firstSourceUnit(text, col)
		if size == 0 {
			break
		}
		previous = col
		col += width
		text = text[size:]
	}
	return Position{Line: snapped.Line, Col: previous}
}

func (pt *PieceTable) nextPosition(pos Position) Position {
	pos = pt.snapPosition(pos)
	content, _ := pt.lineView(pos.Line)
	text := string(content)
	offset := VisualColToByteOffset(text, pos.Col, tabWidth)
	if offset < len(text) {
		_, width := firstSourceUnit(text[offset:], pos.Col)
		return Position{Line: pos.Line, Col: pos.Col + width}
	}
	if pos.Line < pt.LineCount()-1 {
		return Position{Line: pos.Line + 1}
	}
	return pos
}

// sourceWordGeometry classifies whole graphemes by their first rune but retains
// their complete measured widths. Unlike lineRunes, tabs are not expanded and
// flag/ZWJ/modifier clusters do not lose their geometry.
func sourceWordGeometry(text string) (runes []rune, columns []int) {
	col := 0
	for len(text) > 0 {
		size, width := firstSourceUnit(text, col)
		if size == 0 {
			break
		}
		r, _ := utf8.DecodeRuneInString(text)
		runes = append(runes, r)
		columns = append(columns, col)
		col += width
		text = text[size:]
	}
	return runes, append(columns, col)
}

func (e *Editor) wordGeometry(line int, col int) (runes []rune, columns []int, index int) {
	content, _ := e.buf.lineView(line)
	runes, columns = sourceWordGeometry(string(content))
	index = sort.Search(len(columns), func(i int) bool { return columns[i] > col }) - 1
	return runes, columns, max(0, index)
}
