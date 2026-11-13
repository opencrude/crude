package editor

import (
	"bytes"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// Undo stops are a port of VS Code's cursor undo-stop rules
// (src/vs/editor/common/cursorCommon.ts, cursor/cursorTypeEditOperations.ts,
// cursor/cursorDeleteOperations.ts and cursor/cursor.ts). An undo step stays
// open while the user keeps doing the same kind of edit, independent of
// typing speed: a word, a run of spaces, a run of Backspace or Delete. A
// different kind of edit, a caret move, a command, a paste or a change from
// another view or shell closes it.

// editOperation is VS Code's EditOperationType.
type editOperation uint8

const (
	editOther editOperation = iota
	editDeletingLeft
	editDeletingRight
	editTypingOther
	editTypingFirstSpace
	editTypingConsecutiveSpace
)

// undoStop is the undo half of VS Code's EditOperationResult.
type undoStop struct {
	op            editOperation
	before, after bool
}

// atomicUndoStop is a command: it never joins or extends another step.
var atomicUndoStop = undoStop{op: editOther, before: true, after: true}

// openUndoStep is the step later edits may extend (VS Code's open
// EditStackElement). canAppend also requires the source and caret it left.
type openUndoStep struct {
	group    int
	revision uint64
	cursor   Position
}

func typingOperation(text string, previous editOperation) editOperation {
	if text == " " {
		if previous == editTypingFirstSpace || previous == editTypingConsecutiveSpace {
			return editTypingConsecutiveSpace
		}
		return editTypingFirstSpace
	}
	return editTypingOther
}

func shouldPushStackElementBetween(previous, next editOperation) bool {
	if isTypingOperation(previous) && !isTypingOperation(next) {
		return true // always set an undo stop before non-type operations
	}
	if previous == editTypingFirstSpace {
		return false // `abc |d`: no undo stop; `abc  |d`: undo stop
	}
	return normalizeOperation(previous) != normalizeOperation(next)
}

func normalizeOperation(op editOperation) editOperation {
	if op == editTypingConsecutiveSpace {
		return editTypingFirstSpace
	}
	return op
}

func isTypingOperation(op editOperation) bool {
	return op == editTypingOther || op == editTypingFirstSpace || op == editTypingConsecutiveSpace
}

// simpleTypingStop is SimpleCharacterTypeOperation's result.
func (e *Editor) simpleTypingStop(text string) undoStop {
	op := typingOperation(text, e.prevEditOp)
	return undoStop{op: op, before: shouldPushStackElementBetween(e.prevEditOp, op)}
}

// keyUndoStop computes the undo stop of an editing key before it runs, from
// the same edit planners that execute it.
func (e *Editor) keyUndoStop(key tea.KeyPressMsg) undoStop {
	if key.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModSuper) != 0 {
		return atomicUndoStop // word deletion and commands push stops on both sides
	}
	spans := e.selectionSpans()
	selected := false
	for _, s := range spans {
		selected = selected || s.Anchor != s.Cursor
	}
	merge := func(plan func(selectionSpan) undoStop) undoStop {
		result := plan(spans[0])
		for _, s := range spans[1:] {
			next := plan(s)
			if next.op != result.op {
				return atomicUndoStop
			}
			result.before = result.before || next.before
			result.after = result.after || next.after
		}
		// Crude can select source without a caret command (snippet fields,
		// search, pointer drags). Replacing a selection always starts a step,
		// as it does after every VS Code selection command.
		result.before = result.before || selected
		return result
	}
	e.buf.ensureCache()
	switch {
	case key.Code == tea.KeyEnter:
		return undoStop{op: editTypingOther, before: true} // EnterOperation
	case key.Code == tea.KeyTab:
		return atomicUndoStop // the Tab command
	case key.Code == tea.KeyBackspace:
		return merge(func(s selectionSpan) undoStop {
			a, b := s.bounds()
			if a != b {
				return e.deleteUndoStop(editDeletingLeft, a, b)
			}
			return e.backwardCaretEdit(e.buf.positionAtOffset(a), true).stop
		})
	case key.Code == tea.KeyDelete:
		return merge(func(s selectionSpan) undoStop {
			a, b := s.bounds()
			if a == b {
				b = e.buf.positionToOffset(e.buf.nextPosition(e.buf.positionAtOffset(a)))
			}
			return e.deleteUndoStop(editDeletingRight, a, b)
		})
	case key.Text != "":
		return merge(func(s selectionSpan) undoStop {
			if utf8.RuneCountInString(key.Text) != 1 {
				return e.simpleTypingStop(key.Text)
			}
			return e.typedCaretEdit(s.SourceSelection, key.Text).stop
		})
	}
	return atomicUndoStop
}

// deleteUndoStop ports DeleteOperations.deleteLeft/deleteRight: a run of the
// same deletion is one step, and joining lines always starts one.
func (e *Editor) deleteUndoStop(op editOperation, start, end int) undoStop {
	before := e.prevEditOp != op
	if start < end && end <= len(e.buf.cachedBytes) && bytes.ContainsAny(e.buf.cachedBytes[start:end], "\r\n") {
		before = true
	}
	return undoStop{op: op, before: before}
}

// canAppendUndo is EditStackElement.canAppend: the open step is still the
// latest source state and the caret has not moved since.
func (e *Editor) canAppendUndo() bool {
	o := e.openUndo
	return o.group != 0 && o.revision == e.Revision() && o.cursor == e.cursor
}

// beginUndoStep opens the outer transaction for one edit, extending the open
// step when the stop rules allow.
func (e *Editor) beginUndoStep(stop undoStop) {
	if e.undoGroupDepth == 0 && !stop.before && e.canAppendUndo() {
		e.undoGroupDepth = 1
		e.undoGroupID = e.openUndo.group
		e.startUndoGroup()
		return
	}
	e.openUndo = openUndoStep{}
	e.beginUndoGroup()
}

// endUndoStep closes the transaction and records what the next edit may join.
func (e *Editor) endUndoStep(stop undoStop, before uint64) {
	group := e.undoGroupID
	e.endUndoGroup()
	if e.undoGroupDepth != 0 {
		return
	}
	if e.Revision() == before {
		return // nothing changed; a stop before was already pushed
	}
	e.prevEditOp = stop.op
	if stop.after || group == 0 {
		e.openUndo = openUndoStep{}
		return
	}
	e.openUndo = openUndoStep{group: group, revision: e.Revision(), cursor: e.cursor}
}

// BreakUndoGroup is VS Code's pushUndoStop: navigation, focus changes,
// commands and changes from elsewhere end the open step. Text, selection and
// retained undo/redo transactions are unchanged.
func (e *Editor) BreakUndoGroup() {
	e.openUndo = openUndoStep{}
	e.prevEditOp = editOther
}
