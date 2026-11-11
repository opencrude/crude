package editor

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/ui"
)

func (e *Editor) insertRune(r rune) {
	e.insertText(string(r))
}

// InsertRune inserts a single rune at the cursor and advances the cursor.
// Public surface used by the LSP completion popup to apply the selected
// completion's text.
func (e *Editor) InsertRune(r rune) {
	if e.readOnly {
		return
	}
	key := tea.KeyPressMsg{Code: r, Text: string(r)}
	if e.DeferDocumentEvent(key) {
		p := e.documentProjection
		p.historyQueue[len(p.historyQueue)-1].edit = func() { e.handleNonInteractiveInput(key) }
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
	byteOff := VisualColToByteOffset(line, e.cursor.Col, e.TabWidth())
	before := line[:byteOff]
	indent := leadingWhitespace(before)
	baseIndent := indent
	trimmed := strings.TrimRight(before, " \t")
	if r, ok := lastRune(trimmed); ok && (r == '(' || r == '[' || r == '{') {
		indent += e.levelUnit(indent)
		if closer := autoClosePairs[r]; strings.TrimSpace(line[byteOff:]) == string(closer) {
			// Whitespace between the caret and the existing closer belongs to
			// this split. Do not carry it onto the closing line after baseIndent.
			padding := len(leadingWhitespace(line[byteOff:]))
			if padding > 0 {
				e.deleteRecorded(e.cursor, Position{Line: e.cursor.Line, Col: ByteOffsetToVisualCol(line, byteOff+padding, e.TabWidth())}, e.cursor)
			}
			e.insertText(e.lineEnding() + indent + e.lineEnding() + baseIndent)
			e.cursor.Line--
			e.cursor.Col = LineVisualWidth(indent, e.TabWidth())
			e.ensureCursorVisible()
			return
		}
	}
	e.insertText(e.lineEnding() + indent)
}

// pastedText gives every line break of pasted text the document's terminator.
// Inserted as they arrive, a terminal's CR or the Windows clipboard's CRLF
// puts a bare CR inside one line or mixes terminators in the file.
func (e *Editor) pastedText(text string) string {
	text = ui.PastedLineBreaks(text)
	if terminator := e.lineEnding(); terminator != "\n" {
		text = strings.ReplaceAll(text, "\n", terminator)
	}
	return text
}

// Newly inserted line breaks, typed or pasted, follow the source document's
// first terminator. No save or background normalization is involved.
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

// insertTab inserts one level of the document's indentation: a tab, or the
// spaces that reach the next indentation stop.
func (e *Editor) insertTab() {
	style := e.indentStyle()
	if style.tab {
		e.insertText("\t")
		return
	}
	e.insertText(strings.Repeat(" ", style.size-e.cursor.Col%style.size))
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
		off := VisualColToByteOffset(raw, end.Col, e.TabWidth())
		if strings.Trim(raw[:off], " \t") == "" {
			stop := e.indentStyle().width()
			start = e.buf.snapPosition(Position{Line: end.Line, Col: ((end.Col - 1) / stop) * stop})
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
	c.viewID = e.historyID
	if e.buf.changeIndex < len(e.buf.changes) {
		e.buf.changes = e.buf.changes[:e.buf.changeIndex]
	}
	e.buf.changes = append(e.buf.changes, c)
	e.buf.changeIndex = len(e.buf.changes)
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
		e.buf.undoGroupSeq++
		e.undoGroupID = e.buf.undoGroupSeq
		e.startUndoGroup()
	}
}

func (e *Editor) startUndoGroup() {
	e.beginGhostEdit()
	e.undoGroupBefore = e.captureViewState()
	e.undoGroupStart = e.buf.changeIndex
	e.beginDocumentEditGroup(fmt.Sprintf("editor-%x-%d", e.historyID, e.undoGroupID))
}

// BreakUndoGroup separates typing across explicit navigation or focus changes
// without changing text, selection or retained undo/redo transactions.
func (e *Editor) BreakUndoGroup() { e.typingGroup = 0 }

func (e *Editor) endUndoGroup() {
	if e.undoGroupDepth == 0 {
		return
	}
	if e.undoGroupDepth == 1 {
		e.finishSnippetMirrors()
	}
	e.undoGroupDepth--
	if e.undoGroupDepth == 0 {
		e.finishGhostEdit()
		after := e.captureViewState()
		for i := e.undoGroupStart; i < e.buf.changeIndex; i++ {
			e.buf.changes[i].Before = e.undoGroupBefore
			e.buf.changes[i].After = after
		}
		e.endDocumentEditGroup()
		e.undoGroupID = 0
		if e.contentChangePending {
			e.contentChangePending = false
			e.emitContentChanged()
		}
		// Prune complete transactions only; a large replacement must never be
		// cut in half just because it crosses the retained-history bound.
		if len(e.buf.changes) > e.maxHistory {
			cut := len(e.buf.changes) - e.maxHistory
			for cut < len(e.buf.changes) && e.buf.changes[cut].Group == e.buf.changes[cut-1].Group {
				cut++
			}
			if cut < len(e.buf.changes) {
				e.buf.changes = append([]Change(nil), e.buf.changes[cut:]...)
				e.buf.changeIndex = len(e.buf.changes)
			}
		}
	}
}

func (e *Editor) captureViewState() editorViewState {
	return editorViewState{Cursor: e.buf.positionToOffset(e.cursor), Anchor: e.buf.positionToOffset(e.selAnchor), End: e.buf.positionToOffset(e.selEnd), Selecting: e.selecting, ScrollX: e.scrollX, ScrollY: e.scrollY, WrapOffset: e.wrapOffset, WrapBefore: e.wrapBefore}
}

func (e *Editor) restoreViewState(s editorViewState) {
	e.cursor = e.buf.positionAtOffset(s.Cursor)
	e.selAnchor, e.selEnd = e.buf.positionAtOffset(s.Anchor), e.buf.positionAtOffset(s.End)
	e.stopPointerSelection()
	e.selecting = s.Selecting
	e.scrollX, e.scrollY = s.ScrollX, s.ScrollY
	e.wrapOffset, e.wrapBefore = s.WrapOffset, s.WrapBefore
	e.clearGoal()
}

func (e *Editor) undo() {
	e.DismissGhostText()
	e.CancelSnippet()
	e.typingGroup = 0
	if e.documentProjection != nil {
		e.requestDocumentUndo(false)
		return
	}
	if e.buf.changeIndex <= 0 {
		return
	}
	group := e.buf.changes[e.buf.changeIndex-1].Group
	for e.buf.changeIndex > 0 {
		c := e.buf.changes[e.buf.changeIndex-1]
		if c.Group != group {
			break
		}
		e.buf.changeIndex--

		switch c.Type {
		case ActInsert:
			_, _ = e.buf.deleteOffsets(c.Offset, c.Offset+len(c.Text))
		case ActDelete:
			_, _ = e.buf.insertAtOffset(c.Offset, c.Text)
		}
		if c.viewID == 0 || c.viewID == e.historyID {
			e.restoreViewState(c.Before)
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

func (e *Editor) Undo() {
	if !e.readOnly {
		e.undo()
	}
}

func (e *Editor) redo() {
	e.DismissGhostText()
	e.CancelSnippet()
	e.typingGroup = 0
	if e.documentProjection != nil {
		e.requestDocumentUndo(true)
		return
	}
	if e.buf.changeIndex >= len(e.buf.changes) {
		return
	}
	group := e.buf.changes[e.buf.changeIndex].Group
	for e.buf.changeIndex < len(e.buf.changes) {
		c := e.buf.changes[e.buf.changeIndex]
		if c.Group != group {
			break
		}
		e.buf.changeIndex++

		switch c.Type {
		case ActInsert:
			_, _ = e.buf.insertAtOffset(c.Offset, c.Text)
		case ActDelete:
			_, _ = e.buf.deleteOffsets(c.Offset, c.Offset+len(c.Text))
		}
		if c.viewID == 0 || c.viewID == e.historyID {
			e.restoreViewState(c.After)
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

func (e *Editor) Redo() {
	if !e.readOnly {
		e.redo()
	}
}
