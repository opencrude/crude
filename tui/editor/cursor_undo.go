package editor

// Cursor Undo and Cursor Redo are a port of VS Code's CursorUndoRedoController
// (src/vs/editor/contrib/cursorUndo/browser/cursorUndo.ts). Caret moves and
// selection changes are not edits, so they never enter the document's undo
// history; Ctrl+U steps back through them instead. Any source change clears
// both stacks, and the stacks hold at most 50 states.

const cursorUndoLimit = 50

type cursorUndoStack struct {
	buffer     *PieceTable
	revision   uint64
	undo, redo []editorViewState
	// restored is the state the last Cursor Undo/Redo applied. The event that
	// applied it must not record it again (VS Code's _isCursorUndoRedo).
	restored    editorViewState
	hasRestored bool
}

// sameCursorState is CursorState.equals: the same selections in the same order.
func sameCursorState(a, b editorViewState) bool {
	selection := func(s editorViewState) (int, int) {
		if s.Selecting && s.Anchor != s.End {
			return s.Anchor, s.End
		}
		return s.Cursor, s.Cursor
	}
	aStart, aEnd := selection(a)
	bStart, bEnd := selection(b)
	if aStart != bStart || aEnd != bEnd || a.Cursor != b.Cursor {
		return false
	}
	if (a.Secondary == nil) != (b.Secondary == nil) {
		return false
	}
	if a.Secondary == nil {
		return true
	}
	if len(a.Secondary.ranges) != len(b.Secondary.ranges) {
		return false
	}
	for i := range a.Secondary.ranges {
		if a.Secondary.ranges[i] != b.Secondary.ranges[i] {
			return false
		}
	}
	return true
}

// recordCursorChange is the controller's onDidChangeCursorSelection listener:
// a selection change at an unchanged source revision pushes the previous state.
func (e *Editor) recordCursorChange(before editorViewState, revision uint64) {
	s := &e.cursorHistory
	if e.buf == nil {
		return
	}
	if s.buffer != e.buf || s.revision != e.Revision() || revision != e.Revision() {
		// onDidChangeModel / onDidChangeModelContent clear both stacks.
		*s = cursorUndoStack{buffer: e.buf, revision: e.Revision()}
		return
	}
	after := e.captureViewState()
	if sameCursorState(before, after) || s.hasRestored && sameCursorState(after, s.restored) {
		return
	}
	s.hasRestored = false
	if n := len(s.undo); n > 0 && sameCursorState(s.undo[n-1], before) {
		return
	}
	s.undo = append(s.undo, before)
	s.redo = nil
	if len(s.undo) > cursorUndoLimit {
		s.undo = s.undo[1:]
	}
}

func (e *Editor) cursorUndo() { e.stepCursorHistory(true) }
func (e *Editor) cursorRedo() { e.stepCursorHistory(false) }

func (e *Editor) stepCursorHistory(back bool) {
	s := &e.cursorHistory
	if s.buffer != e.buf || s.revision != e.Revision() {
		*s = cursorUndoStack{buffer: e.buf, revision: e.Revision()}
		return
	}
	from, to := &s.undo, &s.redo
	if !back {
		from, to = to, from
	}
	if len(*from) == 0 {
		return
	}
	*to = append(*to, e.captureViewState())
	state := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	// _applyState sets the selections and the saved scroll position.
	e.restoreViewState(state)
	s.restored, s.hasRestored = state, true
	e.BreakUndoGroup()
}
