package editor

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/document"
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

// insertNewline uses the shared catalog-driven Enter recipe. New text and
// indentation are one scalar or multiple-caret transaction and Undo step.
func (e *Editor) insertNewline() {
	off := e.buf.positionToOffset(e.cursor)
	e.applySingleCaretEdit(e.newlineCaretEdit(off, off))
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
	e.insertText(style.padding(e.cursor.Col, e.cursor.Col+style.size-e.cursor.Col%style.size))
}

func (e *Editor) deleteBackward() {
	e.applySingleCaretEdit(e.backwardCaretEdit(e.buf.snapPosition(e.cursor), false))
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
	return editorViewState{Secondary: e.secondary, Cursor: e.buf.positionToOffset(e.cursor), Anchor: e.buf.positionToOffset(e.selAnchor), End: e.buf.positionToOffset(e.selEnd), Selecting: e.selecting, ScrollX: e.scrollX, ScrollY: e.scrollY, WrapOffset: e.wrapOffset, WrapBefore: e.wrapBefore}
}

func (e *Editor) restoreViewState(s editorViewState) {
	e.retainSecondary(s.Secondary)
	e.multiDirty = false
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
	last := e.buf.changeIndex
	first := last - 1
	group := e.buf.changes[first].Group
	for group != 0 && first > 0 && e.buf.changes[first-1].Group == group {
		first--
	}
	e.buf.ensureCache()
	size := len(e.buf.cachedBytes)
	var sequential []document.Edit
	for i := last - 1; i >= first; i-- {
		c := e.buf.changes[i]
		edit := document.Edit{Start: c.Offset, End: c.Offset}
		if c.Type == ActInsert {
			edit.End += len(c.Text)
		} else {
			edit.Text = string(c.Text)
		}
		sequential = append(sequential, edit)
	}
	if err := e.applySourceOffsets(composeDocumentEdits(size, sequential)); err != nil {
		return
	}
	e.buf.changeIndex = first
	if c := e.buf.changes[first]; c.viewID == 0 || c.viewID == e.historyID {
		e.restoreViewState(c.Before)
	}
	e.invalidateEditorCaches()
	e.ensureCursorVisible()
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
	first := e.buf.changeIndex
	last := first + 1
	group := e.buf.changes[first].Group
	for group != 0 && last < len(e.buf.changes) && e.buf.changes[last].Group == group {
		last++
	}
	e.buf.ensureCache()
	size := len(e.buf.cachedBytes)
	var sequential []document.Edit
	for i := first; i < last; i++ {
		c := e.buf.changes[i]
		edit := document.Edit{Start: c.Offset, End: c.Offset}
		if c.Type == ActDelete {
			edit.End += len(c.Text)
		} else {
			edit.Text = string(c.Text)
		}
		sequential = append(sequential, edit)
	}
	if err := e.applySourceOffsets(composeDocumentEdits(size, sequential)); err != nil {
		return
	}
	e.buf.changeIndex = last
	if c := e.buf.changes[last-1]; c.viewID == 0 || c.viewID == e.historyID {
		e.restoreViewState(c.After)
	}
	e.clampCursorToLine()
	e.invalidateEditorCaches()
	e.ensureCursorVisible()
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
