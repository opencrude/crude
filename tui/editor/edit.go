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

	_, _ = e.buf.Insert(e.cursor, runeBytes)
	e.invalidateEditorCaches()
	cursorAfter := e.cursor
	if r == '\n' {
		cursorAfter.Line++
		cursorAfter.Col = 0
	} else {
		cursorAfter.Col += runeVisualWidth(r)
	}
	e.cursor = cursorAfter

	e.recordChange(Change{
		Type:         ActInsert,
		Text:         runeBytes,
		StartPos:     cursorBefore,
		EndPos:       cursorAfter,
		CursorBefore: cursorBefore,
	})
	e.ensureCursorVisible()
	e.notifyContentChanged()
}

// InsertRune inserts a single rune at the cursor and advances the cursor.
// Public surface used by the LSP completion popup to apply the selected
// completion's text.
func (e *Editor) InsertRune(r rune) {
	e.insertRune(r)
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
	_, _ = e.buf.Insert(cursorBefore, []byte(s))
	e.invalidateEditorCaches()
	e.cursor = cursorAfter

	e.recordChange(Change{
		Type:         ActInsert,
		Text:         []byte(s),
		StartPos:     cursorBefore,
		EndPos:       cursorAfter,
		CursorBefore: cursorBefore,
	})
	e.ensureCursorVisible()
	e.notifyContentChanged()
}

// insertNewline inserts a line break with auto-indent: the new line
// inherits the current line's leading whitespace (up to the cursor), plus
// one extra indent level when the text before the cursor ends with an
// opening brace/bracket/paren. Recorded as a single undo step.
func (e *Editor) insertNewline() {
	line := e.LineText(e.cursor.Line)
	byteOff := VisualColToByteOffset(line, e.cursor.Col, tabWidth)
	before := line[:byteOff]
	indent := leadingWhitespace(before)
	trimmed := strings.TrimRight(before, " \t")
	if r, ok := lastRune(trimmed); ok && (r == '(' || r == '[' || r == '{') {
		indent += indentUnit(indent)
	}
	e.insertText("\n" + indent)
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

			_, _ = e.buf.Delete(start, end)
			e.invalidateEditorCaches()
			e.cursor.Col -= chVisW

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

		_, _ = e.buf.Delete(start, end)
		e.invalidateEditorCaches()
		e.cursor.Line = prevLine
		e.cursor.Col = prevLineVisW

		e.recordChange(Change{
			Type:         ActDelete,
			Text:         []byte("\n"),
			StartPos:     start,
			EndPos:       end,
			CursorBefore: cursorBefore,
		})
	}
	e.ensureCursorVisible()
	e.notifyContentChanged()
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

			_, _ = e.buf.Delete(start, end)
			e.invalidateEditorCaches()

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

		_, _ = e.buf.Delete(start, end)
		e.invalidateEditorCaches()

		e.recordChange(Change{
			Type:         ActDelete,
			Text:         []byte("\n"),
			StartPos:     start,
			EndPos:       end,
			CursorBefore: cursorBefore,
		})
	}
	e.ensureCursorVisible()
	e.notifyContentChanged()
}

func (e *Editor) recordChange(c Change) {
	c.Group = e.undoGroupID
	if e.changeIndex < len(e.changes) {
		e.changes = e.changes[:e.changeIndex]
	}
	e.changes = append(e.changes, c)
	if len(e.changes) > e.maxHistory {
		e.changes = e.changes[len(e.changes)-e.maxHistory:]
	}
	e.changeIndex = len(e.changes)
}

// beginUndoGroup starts an undo group: every change recorded before the
// matching endUndoGroup shares one group ID and undoes/redoes atomically.
// Nested calls join the outermost group.
func (e *Editor) beginUndoGroup() {
	e.undoGroupDepth++
	if e.undoGroupDepth == 1 {
		e.undoGroupSeq++
		e.undoGroupID = e.undoGroupSeq
	}
}

func (e *Editor) endUndoGroup() {
	if e.undoGroupDepth == 0 {
		return
	}
	e.undoGroupDepth--
	if e.undoGroupDepth == 0 {
		e.undoGroupID = 0
	}
}

func (e *Editor) undo() {
	if e.changeIndex <= 0 {
		return
	}
	group := e.changes[e.changeIndex-1].Group
	for e.changeIndex > 0 {
		c := e.changes[e.changeIndex-1]
		if c.Group != group {
			break
		}
		e.changeIndex--

		switch c.Type {
		case ActInsert:
			_, _ = e.buf.Delete(c.StartPos, c.EndPos)
			e.cursor = c.CursorBefore
		case ActDelete:
			_, _ = e.buf.Insert(c.StartPos, c.Text)
			e.cursor = c.CursorBefore
		}
		if group == 0 {
			break
		}
	}
	e.invalidateEditorCaches()
	e.ensureCursorVisible()
	e.markDocumentWidthAll()
	if e.buffer != nil {
		e.buffer.NoteEdit()
	}
	e.emitContentChanged()
}

func (e *Editor) Undo() { e.undo() }

func (e *Editor) redo() {
	if e.changeIndex >= len(e.changes) {
		return
	}
	group := e.changes[e.changeIndex].Group
	for e.changeIndex < len(e.changes) {
		c := e.changes[e.changeIndex]
		if c.Group != group {
			break
		}
		e.changeIndex++

		switch c.Type {
		case ActInsert:
			_, _ = e.buf.Insert(c.StartPos, c.Text)
			e.cursor = c.EndPos
		case ActDelete:
			_, _ = e.buf.Delete(c.StartPos, c.EndPos)
			e.cursor = c.EndPos
		}
		if group == 0 {
			break
		}
	}
	e.clampCursorToLine()
	e.invalidateEditorCaches()
	e.ensureCursorVisible()
	e.markDocumentWidthAll()
	if e.buffer != nil {
		e.buffer.NoteEdit()
	}
	e.emitContentChanged()
}

func (e *Editor) Redo() { e.redo() }
