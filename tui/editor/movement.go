package editor

import (
	"crude/tui/rendering"
	"unicode"
)

func (e *Editor) moveUp() {
	if e.usesRows() {
		e.moveVisualRows(-1)
		return
	}
	if e.cursor.Line <= 0 {
		return
	}
	if !e.goalSet {
		e.goalCol = e.displayColumn(e.cursor.Line, e.cursor.Col)
		e.goalSet = true
	}
	e.cursor.Line--
	e.applyGoalCol()
	e.ensureCursorVisible()
}

func (e *Editor) moveDown() {
	if e.usesRows() {
		e.moveVisualRows(1)
		return
	}
	maxLine := e.buf.LineCount() - 1
	if maxLine < 0 {
		maxLine = 0
	}
	if e.cursor.Line >= maxLine {
		return
	}
	if !e.goalSet {
		e.goalCol = e.displayColumn(e.cursor.Line, e.cursor.Col)
		e.goalSet = true
	}
	e.cursor.Line++
	e.applyGoalCol()
	e.ensureCursorVisible()
}

func (e *Editor) applyGoalCol() {
	e.cursor = e.buf.snapPosition(Position{Line: e.cursor.Line, Col: e.sourceColumn(e.cursor.Line, e.goalCol)})
}

func (e *Editor) clearGoal() {
	e.goalSet = false
	e.goalCol = 0
}

func (e *Editor) moveLeft() {
	e.clearGoal()
	if e.wrapsRows() && !e.wrapBefore {
		row, col := e.visualPosition(e.cursor, false)
		if col == 0 && e.rowAt(row).index > 0 {
			e.wrapBefore = true
			e.ensureCursorVisible()
			return
		}
	}
	e.wrapBefore = false
	e.cursor = e.buf.previousPosition(e.cursor)
	e.ensureCursorVisible()
}

func (e *Editor) moveRight() {
	e.clearGoal()
	if e.wrapsRows() && e.wrapBefore {
		e.wrapBefore = false
		e.ensureCursorVisible()
		return
	}
	e.cursor = e.buf.nextPosition(e.cursor)
	e.ensureCursorVisible()
}

func (e *Editor) moveWordLeft() {
	e.wrapBefore = false
	e.clearGoal()
	e.cursor = e.buf.snapPosition(e.cursor)
	if e.cursor.Col <= 0 && e.cursor.Line == 0 {
		return
	}
	runes, columns, ri := e.wordGeometry(e.cursor.Line, e.cursor.Col)
	for {
		if ri <= 0 {
			if e.cursor.Line > 0 {
				e.cursor.Line--
				runes, columns, _ = e.wordGeometry(e.cursor.Line, 0)
				ri = len(runes)
			} else {
				e.cursor.Col = 0
				return
			}
		}
		// Skip trailing whitespace first.
		for ri > 0 && unicode.IsSpace(runes[ri-1]) {
			ri--
		}
		if ri <= 0 {
			if e.cursor.Line == 0 {
				e.cursor.Col = 0
				e.ensureCursorVisible()
				return
			}
			// Whitespace-only (or fully consumed) line: keep climbing to the
			// previous line's last word. Regression (b323a3b): this returned,
			// parking the cursor on blank lines instead of the word before them.
			continue
		}
		r := runes[ri-1]
		if isWordChar(r) {
			for ri > 0 && isWordChar(runes[ri-1]) {
				ri--
			}
		} else {
			// Punctuation run: stop at its start (do not skip through to a word).
			for ri > 0 && !isWordChar(runes[ri-1]) && !unicode.IsSpace(runes[ri-1]) {
				ri--
			}
		}
		e.cursor.Col = columns[ri]
		e.ensureCursorVisible()
		return
	}
}

func (e *Editor) moveWordRight() {
	e.wrapBefore = false
	e.clearGoal()
	e.cursor = e.buf.snapPosition(e.cursor)
	runes, columns, ri := e.wordGeometry(e.cursor.Line, e.cursor.Col)
	maxLine := e.buf.LineCount() - 1
	for {
		if ri >= len(runes) {
			if e.cursor.Line < maxLine {
				e.cursor.Line++
				e.cursor.Col = 0
				e.ensureCursorVisible()
				return
			}
			return
		}
		r := runes[ri]
		if isWordChar(r) {
			for ri < len(runes) && isWordChar(runes[ri]) {
				ri++
			}
		} else if unicode.IsSpace(r) {
			for ri < len(runes) && unicode.IsSpace(runes[ri]) {
				ri++
			}
			if ri >= len(runes) {
				continue
			}
			if isWordChar(runes[ri]) {
				for ri < len(runes) && isWordChar(runes[ri]) {
					ri++
				}
			} else {
				for ri < len(runes) && !isWordChar(runes[ri]) && !unicode.IsSpace(runes[ri]) {
					ri++
				}
			}
		} else {
			// Punctuation run: stop at its end.
			for ri < len(runes) && !isWordChar(runes[ri]) && !unicode.IsSpace(runes[ri]) {
				ri++
			}
		}
		e.cursor.Col = columns[ri]
		e.ensureCursorVisible()
		return
	}
}

func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func runeVisualWidth(tabW int, r rune) int {
	if r == '\t' {
		// Lone-tab width at column 0; mid-line tabs go through ExpandTabs /
		// TabStopAdvance in the coords module.
		return TabStopAdvance(0, tabW)
	}
	return rendering.VisibleWidth(string(r))
}

func lineVisualWidthFromRunes(tabW int, runes []rune) int {
	return LineVisualWidth(string(runes), tabW)
}

func runesToVisualCol(tabW int, runes []rune, ri int) int {
	ri = min(len(runes), max(0, ri))
	return ByteOffsetToVisualCol(string(runes), len(string(runes[:ri])), tabW)
}

// RuneIndexToVisualColumn maps source rune offsets to editor columns, including
// tab stops and complete graphemes. Search/LSP adapters must use source geometry.
func RuneIndexToVisualColumn(tabW int, text string, index int) int {
	return runesToVisualCol(tabW, []rune(text), index)
}

func colToRuneIdx(tabW int, runes []rune, col int) int {
	text := string(runes)
	return len([]rune(text[:VisualColToByteOffset(text, col, tabW)]))
}

// moveToLineStart implements Smart Home: the cursor toggles between the
// first non-whitespace column and column 0, from either direction.
func (e *Editor) moveToLineStart() {
	if e.wrapsRows() {
		row, _ := e.visualPosition(e.cursor, e.wrapBefore)
		r := e.rowAt(row)
		start := e.sourceColumn(r.line, r.start)
		if r.index > 0 && (e.cursor.Col != start || e.wrapBefore) {
			e.cursor.Col = start
			e.wrapBefore = false
			e.clearGoal()
			e.ensureCursorVisible()
			return
		}
	}
	e.wrapBefore = false
	e.clearGoal()
	b, err := e.buf.Line(e.cursor.Line)
	if err != nil {
		e.cursor.Col = 0
		e.ensureCursorVisible()
		return
	}
	text := string(b)
	firstNonWS := 0
	for len(text) > 0 {
		if text[0] != ' ' && text[0] != '\t' {
			break
		}
		size, width := firstSourceUnit(e.TabWidth(), text, firstNonWS)
		firstNonWS += width
		text = text[size:]
	}
	if e.cursor.Col == firstNonWS {
		e.cursor.Col = 0
	} else {
		e.cursor.Col = firstNonWS
	}
	e.ensureCursorVisible()
}

func (e *Editor) moveToLineEnd() {
	if e.wrapsRows() && !e.wrapBefore {
		row, _ := e.visualPosition(e.cursor, false)
		r := e.rowAt(row)
		if r.continues {
			e.cursor.Col = e.sourceColumn(r.line, r.end)
			e.wrapBefore = true
			e.clearGoal()
			e.ensureCursorVisible()
			return
		}
	}
	e.wrapBefore = false
	e.clearGoal()
	e.cursor.Col = e.lineVisualWidth(e.cursor.Line)
	e.ensureCursorVisible()
}

func (e *Editor) pageUp() {
	if e.usesRows() {
		e.moveVisualRows(-max(1, e.bodyHeight()-1))
		return
	}
	if !e.goalSet {
		e.goalCol = e.displayColumn(e.cursor.Line, e.cursor.Col)
		e.goalSet = true
	}
	pageSize := e.viewH - 2
	if pageSize < 1 {
		pageSize = 1
	}
	e.cursor.Line -= pageSize
	if e.cursor.Line < 0 {
		e.cursor.Line = 0
	}
	e.applyGoalCol()
	e.ensureCursorVisible()
}

func (e *Editor) pageDown() {
	if e.usesRows() {
		e.moveVisualRows(max(1, e.bodyHeight()-1))
		return
	}
	if !e.goalSet {
		e.goalCol = e.displayColumn(e.cursor.Line, e.cursor.Col)
		e.goalSet = true
	}
	pageSize := e.viewH - 2
	if pageSize < 1 {
		pageSize = 1
	}
	e.cursor.Line += pageSize
	lastLine := e.buf.LineCount() - 1
	if lastLine < 0 {
		lastLine = 0
	}
	if e.cursor.Line >= lastLine {
		e.cursor.Line = lastLine
	}
	e.applyGoalCol()
	e.ensureCursorVisible()
}

func (e *Editor) goToDocStart() {
	e.wrapBefore = false
	e.clearGoal()
	e.cursor = Position{Line: 0, Col: 0}
	e.ensureCursorVisible()
}

func (e *Editor) goToDocEnd() {
	e.wrapBefore = false
	e.clearGoal()
	lastLine := e.buf.LineCount() - 1
	if lastLine < 0 {
		lastLine = 0
	}
	e.cursor = Position{Line: lastLine, Col: e.lineVisualWidth(lastLine)}
	e.ensureCursorVisible()
}
