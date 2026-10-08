package editor

import (
	"crude/tui/rendering"
	"unicode"
)

func (e *Editor) moveUp() {
	if e.cursor.Line <= 0 {
		return
	}
	if !e.goalSet {
		e.goalCol = e.cursor.Col
		e.goalSet = true
	}
	e.cursor.Line--
	e.applyGoalCol()
	e.ensureCursorVisible()
}

func (e *Editor) moveDown() {
	maxLine := e.buf.LineCount() - 1
	if maxLine < 0 {
		maxLine = 0
	}
	if e.cursor.Line >= maxLine {
		return
	}
	if !e.goalSet {
		e.goalCol = e.cursor.Col
		e.goalSet = true
	}
	e.cursor.Line++
	e.applyGoalCol()
	e.ensureCursorVisible()
}

func (e *Editor) applyGoalCol() {
	lineVisW := e.lineVisualWidth(e.cursor.Line)
	if e.goalCol > lineVisW {
		e.cursor.Col = lineVisW
	} else {
		e.cursor.Col = e.goalCol
	}
}

func (e *Editor) clearGoal() {
	e.goalSet = false
	e.goalCol = 0
}

func (e *Editor) moveLeft() {
	e.clearGoal()
	if e.cursor.Col > 0 {
		runes := e.lineRunes(e.cursor.Line)
		if len(runes) > 0 {
			ri := colToRuneIdx(runes, e.cursor.Col)
			if ri > 0 {
				prevChW := runeVisualWidth(runes[ri-1])
				e.cursor.Col -= prevChW
			} else {
				e.cursor.Col = 0
			}
		} else {
			e.cursor.Col--
		}
	} else if e.cursor.Line > 0 {
		e.cursor.Line--
		e.cursor.Col = e.lineVisualWidth(e.cursor.Line)
	}
	e.ensureCursorVisible()
}

func (e *Editor) moveRight() {
	e.clearGoal()
	lineVisW := e.lineVisualWidth(e.cursor.Line)
	if e.cursor.Col < lineVisW {
		runes := e.lineRunes(e.cursor.Line)
		if len(runes) > 0 {
			ri := colToRuneIdx(runes, e.cursor.Col)
			if ri < len(runes) {
				chW := runeVisualWidth(runes[ri])
				e.cursor.Col += chW
				if e.cursor.Col > lineVisW {
					e.cursor.Col = lineVisW
				}
			}
		} else {
			e.cursor.Col++
		}
	} else if e.cursor.Line < e.buf.LineCount()-1 {
		e.cursor.Line++
		e.cursor.Col = 0
	}
	e.ensureCursorVisible()
}

func (e *Editor) moveWordLeft() {
	e.clearGoal()
	if e.cursor.Col <= 0 && e.cursor.Line == 0 {
		return
	}
	runes := e.lineRunes(e.cursor.Line)
	ri := colToRuneIdx(runes, e.cursor.Col)
	for {
		if ri <= 0 {
			if e.cursor.Line > 0 {
				e.cursor.Line--
				runes = e.lineRunes(e.cursor.Line)
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
			// Punctuation run — stop at its start (do not skip through to a word).
			for ri > 0 && !isWordChar(runes[ri-1]) && !unicode.IsSpace(runes[ri-1]) {
				ri--
			}
		}
		e.cursor.Col = runesToVisualCol(runes, ri)
		e.ensureCursorVisible()
		return
	}
}

func (e *Editor) moveWordRight() {
	e.clearGoal()
	runes := e.lineRunes(e.cursor.Line)
	ri := colToRuneIdx(runes, e.cursor.Col)
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
			// Punctuation run — stop at its end.
			for ri < len(runes) && !isWordChar(runes[ri]) && !unicode.IsSpace(runes[ri]) {
				ri++
			}
		}
		e.cursor.Col = runesToVisualCol(runes, ri)
		e.ensureCursorVisible()
		return
	}
}

func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func runeVisualWidth(r rune) int {
	if r == '\t' {
		// Lone-tab width at column 0; mid-line tabs go through ExpandTabs /
		// TabStopAdvance in the coords module.
		return TabStopAdvance(0, tabWidth)
	}
	return rendering.VisibleWidth(string(r))
}

func lineVisualWidthFromRunes(runes []rune) int {
	col := 0
	for _, r := range runes {
		if r == '\t' {
			col += TabStopAdvance(col, tabWidth)
		} else {
			col += rendering.VisibleWidth(string(r))
		}
	}
	return col
}

func runesToVisualCol(runes []rune, ri int) int {
	col := 0
	for i := 0; i < ri && i < len(runes); i++ {
		if runes[i] == '\t' {
			col += TabStopAdvance(col, tabWidth)
		} else {
			col += rendering.VisibleWidth(string(runes[i]))
		}
	}
	return col
}

// RuneIndexToVisualColumn maps source rune offsets to editor columns, including
// tab stops and complete graphemes. Search/LSP adapters must use source geometry.
func RuneIndexToVisualColumn(text string, index int) int {
	runes := []rune(text)
	index = min(len(runes), max(0, index))
	return LineVisualWidth(string(runes[:index]), tabWidth)
}

func colToRuneIdx(runes []rune, col int) int {
	visCol := 0
	for i, r := range runes {
		var cw int
		if r == '\t' {
			cw = TabStopAdvance(visCol, tabWidth)
		} else {
			cw = rendering.VisibleWidth(string(r))
		}
		if visCol+cw > col {
			return i
		}
		if visCol+cw == col {
			return i + 1
		}
		visCol += cw
	}
	return len(runes)
}

// moveToLineStart implements Smart Home: the cursor toggles between the
// first non-whitespace column and column 0, from either direction.
func (e *Editor) moveToLineStart() {
	e.clearGoal()
	b, err := e.buf.Line(e.cursor.Line)
	if err != nil {
		e.cursor.Col = 0
		e.ensureCursorVisible()
		return
	}
	expLine := ExpandTabs(string(b), tabWidth)
	firstNonWS := 0
	for _, r := range expLine {
		if r != ' ' {
			break
		}
		firstNonWS++
	}
	if e.cursor.Col == firstNonWS {
		e.cursor.Col = 0
	} else {
		e.cursor.Col = firstNonWS
	}
	e.ensureCursorVisible()
}

func (e *Editor) moveToLineEnd() {
	e.clearGoal()
	e.cursor.Col = e.lineVisualWidth(e.cursor.Line)
	e.ensureCursorVisible()
}

func (e *Editor) pageUp() {
	if !e.goalSet {
		e.goalCol = e.cursor.Col
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
	if !e.goalSet {
		e.goalCol = e.cursor.Col
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
	e.clearGoal()
	e.cursor = Position{Line: 0, Col: 0}
	e.ensureCursorVisible()
}

func (e *Editor) goToDocEnd() {
	e.clearGoal()
	lastLine := e.buf.LineCount() - 1
	if lastLine < 0 {
		lastLine = 0
	}
	e.cursor = Position{Line: lastLine, Col: e.lineVisualWidth(lastLine)}
	e.ensureCursorVisible()
}
