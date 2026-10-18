package editor

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (e *Editor) insertRune(r rune) {
	e.insertText(string(r))
}

// InsertRune inserts a single rune at the cursor and advances the cursor.
// Public surface used by the LSP completion popup to apply the selected
// completion's text.
func (e *Editor) InsertRune(r rune) {
	if e.readOnly || e.DeferDocumentEvent(tea.KeyPressMsg{Code: r, Text: string(r)}) {
		return
	}
	e.insertRune(r)
}

func (e *Editor) insertText(s string) {
	if s == "" {
		return
	}

	e.beginUndoGroup()
	defer e.endUndoGroup()
	e.clearGoal()
	e.cursor = e.insertRecorded(e.buf.snapPosition(e.cursor), s, e.cursor)
	e.invalidateEditorCaches()
	e.ensureCursorVisible()
	e.notifyContentChanged()
}

// insertNewline inserts a line break with auto-indent: the new line
// inherits the current line's leading whitespace (up to the cursor), plus
// one extra indent level when the text before the cursor ends with an
// opening brace/bracket/paren. Recorded as a single undo step.
func (e *Editor) insertNewline() {
	e.beginUndoGroup()
	defer e.endUndoGroup()
	line := e.LineText(e.cursor.Line)
	byteOff := VisualColToByteOffset(line, e.cursor.Col, tabWidth)
	before := line[:byteOff]
	indent := leadingWhitespace(before)
	baseIndent := indent
	trimmed := strings.TrimRight(before, " \t")
	if r, ok := lastRune(trimmed); ok && (r == '(' || r == '[' || r == '{') {
		indent += indentUnit(indent)
		if closer := autoClosePairs[r]; strings.TrimSpace(line[byteOff:]) == string(closer) {
			e.insertText(e.lineEnding() + indent + e.lineEnding() + baseIndent)
			e.cursor.Line--
			e.cursor.Col = LineVisualWidth(indent, tabWidth)
			e.ensureCursorVisible()
			return
		}
	}
	e.insertText(e.lineEnding() + indent)
}

// Newly inserted line breaks follow the source document's first terminator;
// paste remains byte-exact. No save or background normalization is involved.
func (e *Editor) lineEnding() string {
	e.buf.ensureCache()
	for i, b := range e.buf.cachedBytes {
		if b == '\n' {
			if i > 0 && e.buf.cachedBytes[i-1] == '\r' {
				return "\r\n"
			}
			break
		}
	}
	return "\n"
}

func (e *Editor) insertTab() {
	e.insertText(strings.Repeat(" ", tabWidth))
}

func (e *Editor) deleteBackward() {
	e.beginUndoGroup()
	defer e.endUndoGroup()
	end := e.buf.snapPosition(e.cursor)
	start := e.buf.previousPosition(end)
	// Within leading whitespace, Backspace removes one indentation stop. It
	// never extends into words, even when the indent contains tabs.
	if end.Col > 0 {
		raw := e.LineText(end.Line)
		off := VisualColToByteOffset(raw, end.Col, tabWidth)
		if strings.Trim(raw[:off], " \t") == "" {
			start = e.buf.snapPosition(Position{Line: end.Line, Col: ((end.Col - 1) / tabWidth) * tabWidth})
		}
	}
	if e.deleteRecorded(start, end, e.cursor) != "" {
		e.cursor = start
		e.finishSimpleEdit()
	}
}

func (e *Editor) deleteForward() {
	e.beginUndoGroup()
	defer e.endUndoGroup()
	start := e.buf.snapPosition(e.cursor)
	if e.deleteRecorded(start, e.buf.nextPosition(start), e.cursor) != "" {
		e.cursor = start
		e.finishSimpleEdit()
	}
}

func (e *Editor) finishSimpleEdit() {
	e.clearGoal()
	e.invalidateEditorCaches()
	e.ensureCursorVisible()
	e.notifyContentChanged()
}

func (e *Editor) recordChange(c Change) {
	if e.documentProjection != nil {
		return // the core owns connected history; do not retain a competing log
	}
	c.Group = e.undoGroupID
	c.Before = e.captureViewState()
	c.Before.Cursor = c.CursorBefore
	c.After = e.captureViewState()
	if e.changeIndex < len(e.changes) {
		e.changes = e.changes[:e.changeIndex]
	}
	e.changes = append(e.changes, c)
	e.changeIndex = len(e.changes)
}

// beginUndoGroup starts an undo group: every change recorded before the
// matching endUndoGroup shares one group ID and undoes/redoes atomically.
// Nested calls join the outermost group.
// replacingSelection runs insert in place of the active selection: typing,
// pasting or Enter over a selection replaces it, and the delete plus insert
// undo as one step.
func (e *Editor) replacingSelection(insert func()) {
	if !e.HasSelection() {
		insert()
		return
	}
	e.beginUndoGroup()
	e.DeleteSelection()
	insert()
	e.endUndoGroup()
}

func (e *Editor) beginUndoGroup() {
	e.undoGroupDepth++
	if e.undoGroupDepth == 1 {
		e.undoGroupSeq++
		e.undoGroupID = e.undoGroupSeq
		e.startUndoGroup()
	}
}

func (e *Editor) startUndoGroup() {
	e.undoGroupBefore = e.captureViewState()
	e.undoGroupStart = e.changeIndex
	e.beginDocumentEditGroup(fmt.Sprintf("editor-%x-%d", e.historyID, e.undoGroupID))
}

// BreakUndoGroup separates typing across explicit navigation or focus changes
// without changing text, selection or retained undo/redo transactions.
func (e *Editor) BreakUndoGroup() { e.typingGroup = 0 }

func (e *Editor) endUndoGroup() {
	if e.undoGroupDepth == 0 {
		return
	}
	e.undoGroupDepth--
	if e.undoGroupDepth == 0 {
		after := e.captureViewState()
		for i := e.undoGroupStart; i < e.changeIndex; i++ {
			e.changes[i].Before = e.undoGroupBefore
			e.changes[i].After = after
		}
		e.endDocumentEditGroup()
		e.undoGroupID = 0
		// Prune complete transactions only; a large replacement must never be
		// cut in half just because it crosses the retained-history bound.
		if len(e.changes) > e.maxHistory {
			cut := len(e.changes) - e.maxHistory
			for cut < len(e.changes) && e.changes[cut].Group == e.changes[cut-1].Group {
				cut++
			}
			if cut < len(e.changes) {
				e.changes = append([]Change(nil), e.changes[cut:]...)
				e.changeIndex = len(e.changes)
			}
		}
	}
}

func (e *Editor) captureViewState() editorViewState {
	return editorViewState{Cursor: e.cursor, Anchor: e.selAnchor, End: e.selEnd, Selecting: e.selecting, ScrollX: e.scrollX, ScrollY: e.scrollY}
}

func (e *Editor) restoreViewState(s editorViewState) {
	e.cursor = e.buf.snapPosition(s.Cursor)
	e.selAnchor, e.selEnd = e.buf.snapPosition(s.Anchor), e.buf.snapPosition(s.End)
	e.stopPointerSelection()
	e.selecting = s.Selecting
	e.scrollX, e.scrollY = s.ScrollX, s.ScrollY
	e.clearGoal()
}

func (e *Editor) undo() {
	e.typingGroup = 0
	if e.documentProjection != nil {
		e.requestDocumentUndo(false)
		return
	}
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
			_, _ = e.buf.deleteOffsets(c.Offset, c.Offset+len(c.Text))
		case ActDelete:
			_, _ = e.buf.insertAtOffset(c.Offset, c.Text)
		}
		e.restoreViewState(c.Before)
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

func (e *Editor) Undo() {
	if !e.readOnly {
		e.undo()
	}
}

func (e *Editor) redo() {
	e.typingGroup = 0
	if e.documentProjection != nil {
		e.requestDocumentUndo(true)
		return
	}
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
			_, _ = e.buf.insertAtOffset(c.Offset, c.Text)
		case ActDelete:
			_, _ = e.buf.deleteOffsets(c.Offset, c.Offset+len(c.Text))
		}
		e.restoreViewState(c.After)
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

func (e *Editor) Redo() {
	if !e.readOnly {
		e.redo()
	}
}
