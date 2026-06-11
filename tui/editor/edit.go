package editor

import (
	"strings"
	"unicode/utf8"
)

func (e *Editor) insertRune(r rune) {
	e.clearGoal()
	cursorBefore := e.cursor
	runeBytes := make([]byte, utf8.RuneLen(r))
	utf8.EncodeRune(runeBytes, r)

	e.buf.Insert(e.cursor, runeBytes)
	e.invalidateEditorCaches()

	cursorAfter := e.cursor
	if r == '\n' {
		cursorAfter.Line++
		cursorAfter.Col = 0
	} else {
		cursorAfter.Col += runeVisualWidth(r)
	}
	e.cursor = cursorAfter
	e.modified = true

	e.recordChange(Change{
		Type:         ActInsert,
		Text:         runeBytes,
		StartPos:     cursorBefore,
		EndPos:       cursorAfter,
		CursorBefore: cursorBefore,
	})
	e.ensureCursorVisible()
	e.syncLSP()
}

func (e *Editor) insertText(s string) {
	if s == "" {
		return
	}

	cursorBefore := e.cursor
	cursorAfter := cursorBefore
	for _, r := range s {
		if r == '\n' {
			cursorAfter.Line++
			cursorAfter.Col = 0
		} else {
			cursorAfter.Col += runeVisualWidth(r)
		}
	}

	e.clearGoal()
	e.buf.Insert(cursorBefore, []byte(s))
	e.invalidateEditorCaches()
	e.cursor = cursorAfter
	e.modified = true

	e.recordChange(Change{
		Type:         ActInsert,
		Text:         []byte(s),
		StartPos:     cursorBefore,
		EndPos:       cursorAfter,
		CursorBefore: cursorBefore,
	})
	e.ensureCursorVisible()
	e.syncLSP()
}

func (e *Editor) insertNewline() {
	e.insertRune('\n')
}

func (e *Editor) insertTab() {
	e.insertText(strings.Repeat(" ", tabWidth))
}

func (e *Editor) deleteBackward() {
	e.clearGoal()
	if e.cursor.Col > 0 {
		cursorBefore := e.cursor
		runes := e.lineRunes(e.cursor.Line)
		ri := colToRuneIdx(runes, e.cursor.Col)
		if ri > 0 {
			ch := runes[ri-1]
			chVisW := runeVisualWidth(ch)
			start := Position{Line: e.cursor.Line, Col: e.cursor.Col - chVisW}
			end := e.cursor

			e.buf.Delete(start, end)
			e.invalidateEditorCaches()
			e.cursor.Col -= chVisW
			e.modified = true

			e.recordChange(Change{
				Type:         ActDelete,
				Text:         []byte(string(ch)),
				StartPos:     start,
				EndPos:       end,
				CursorBefore: cursorBefore,
			})
		}
	} else if e.cursor.Line > 0 {
		cursorBefore := e.cursor
		prevLine := e.cursor.Line - 1
		prevLineVisW := e.lineVisualWidth(prevLine)

		start := Position{Line: prevLine, Col: prevLineVisW}
		end := Position{Line: e.cursor.Line, Col: 0}

		e.buf.Delete(start, end)
		e.invalidateEditorCaches()
		e.cursor.Line = prevLine
		e.cursor.Col = prevLineVisW
		e.modified = true

		e.recordChange(Change{
			Type:         ActDelete,
			Text:         []byte("\n"),
			StartPos:     start,
			EndPos:       end,
			CursorBefore: cursorBefore,
		})
	}
	e.ensureCursorVisible()
	e.syncLSP()
}

func (e *Editor) deleteForward() {
	e.clearGoal()
	runes := e.lineRunes(e.cursor.Line)
	if len(runes) == 0 {
		return
	}
	lineVisW := lineVisualWidthFromRunes(runes)

	if e.cursor.Col < lineVisW {
		cursorBefore := e.cursor
		ri := colToRuneIdx(runes, e.cursor.Col)
		if ri < len(runes) {
			ch := runes[ri]
			chVisW := runeVisualWidth(ch)
			start := e.cursor
			end := Position{Line: e.cursor.Line, Col: e.cursor.Col + chVisW}

			e.buf.Delete(start, end)
			e.invalidateEditorCaches()
			e.modified = true

			e.recordChange(Change{
				Type:         ActDelete,
				Text:         []byte(string(ch)),
				StartPos:     start,
				EndPos:       end,
				CursorBefore: cursorBefore,
			})
		}
	} else if e.cursor.Line < e.buf.LineCount()-1 {
		cursorBefore := e.cursor
		start := e.cursor
		end := Position{Line: e.cursor.Line + 1, Col: 0}

		e.buf.Delete(start, end)
		e.invalidateEditorCaches()
		e.modified = true

		e.recordChange(Change{
			Type:         ActDelete,
			Text:         []byte("\n"),
			StartPos:     start,
			EndPos:       end,
			CursorBefore: cursorBefore,
		})
	}
	e.ensureCursorVisible()
	e.syncLSP()
}

func (e *Editor) recordChange(c Change) {
	if e.changeIndex < len(e.changes) {
		e.changes = e.changes[:e.changeIndex]
	}
	e.changes = append(e.changes, c)
	if len(e.changes) > e.maxHistory {
		e.changes = e.changes[len(e.changes)-e.maxHistory:]
	}
	e.changeIndex = len(e.changes)
}

func (e *Editor) undo() {
	if e.changeIndex <= 0 {
		return
	}
	e.changeIndex--
	c := e.changes[e.changeIndex]

	switch c.Type {
	case ActInsert:
		e.buf.Delete(c.StartPos, c.EndPos)
		e.cursor = c.CursorBefore
	case ActDelete:
		e.buf.Insert(c.StartPos, c.Text)
		e.cursor = c.CursorBefore
	}
	e.invalidateEditorCaches()
	e.modified = true
	e.ensureCursorVisible()
	e.syncLSP()
}

func (e *Editor) redo() {
	if e.changeIndex >= len(e.changes) {
		return
	}
	c := e.changes[e.changeIndex]
	e.changeIndex++

	switch c.Type {
	case ActInsert:
		e.buf.Insert(c.StartPos, c.Text)
		e.cursor = c.EndPos
	case ActDelete:
		e.buf.Delete(c.StartPos, c.EndPos)
		e.cursor = c.EndPos
	}
	e.invalidateEditorCaches()
	e.modified = true
	e.ensureCursorVisible()
	e.syncLSP()
}

func (e *Editor) save() {
	err := e.buf.Save(e.filePath)
	if err == nil {
		e.modified = false
	}
	e.syncLSP()
}
