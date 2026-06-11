package editor

import "crude/tui/rendering"

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
		ri--
		if ri < 0 {
			continue
		}
		r := runes[ri]
		if isWordChar(r) {
			for ri > 0 && isWordChar(runes[ri-1]) {
				ri--
			}
			e.cursor.Col = runesToVisualCol(runes, ri)
			e.ensureCursorVisible()
			return
		}
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
			e.cursor.Col = runesToVisualCol(runes, ri)
			e.ensureCursorVisible()
			return
		}
		ri++
	}
}

func isWordChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
}

func runeVisualWidth(r rune) int {
	if r == '\t' {
		return tabWidth
	}
	return rendering.StringWidth(string(r))
}

func lineVisualWidthFromRunes(runes []rune) int {
	w := 0
	for _, r := range runes {
		w += runeVisualWidth(r)
	}
	return w
}

func runesToVisualCol(runes []rune, ri int) int {
	col := 0
	for i := 0; i < ri && i < len(runes); i++ {
		col += runeVisualWidth(runes[i])
	}
	return col
}

func colToRuneIdx(runes []rune, col int) int {
	visCol := 0
	for i, r := range runes {
		cw := runeVisualWidth(r)
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

func (e *Editor) moveToLineStart() {
	e.clearGoal()
	b, err := e.buf.Line(e.cursor.Line)
	if err != nil {
		e.cursor.Col = 0
		e.ensureCursorVisible()
		return
	}
	expLine := stringsReplaceTab(string(b))
	if e.cursor.Col > 0 {
		firstNonWS := 0
		for _, r := range expLine {
			if r != ' ' {
				break
			}
			firstNonWS++
		}
		if e.cursor.Col != firstNonWS {
			e.cursor.Col = firstNonWS
		} else {
			e.cursor.Col = 0
		}
	}
	e.ensureCursorVisible()
}

func (e *Editor) moveToLineEnd() {
	e.clearGoal()
	e.cursor.Col = e.lineVisualWidth(e.cursor.Line)
	e.ensureCursorVisible()
}

func (e *Editor) pageUp() {
	e.clearGoal()
	pageSize := e.viewH - 2
	if pageSize < 1 {
		pageSize = 1
	}
	e.cursor.Line -= pageSize
	if e.cursor.Line < 0 {
		e.cursor.Line = 0
	}
	e.clampCol(e.cursor.Line)
	e.ensureCursorVisible()
}

func (e *Editor) pageDown() {
	e.clearGoal()
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
	e.clampCol(e.cursor.Line)
	e.ensureCursorVisible()
}

func (e *Editor) clampCol(lineIdx int) {
	if lineIdx < 0 {
		return
	}
	lineVisW := e.lineVisualWidth(lineIdx)
	if e.cursor.Col > lineVisW {
		e.cursor.Col = lineVisW
	}
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
