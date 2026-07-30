package editor

func (e *Editor) ClearSelection() {
	e.selecting = false
	e.mouseDragging = false
	e.selAnchor = Position{Line: 0, Col: 0}
	e.selEnd = Position{Line: 0, Col: 0}
}

func (e *Editor) HasSelection() bool {
	if !e.selecting && e.selAnchor.Line == e.selEnd.Line && e.selAnchor.Col == e.selEnd.Col {
		return false
	}
	return e.selAnchor.Line != e.selEnd.Line || e.selAnchor.Col != e.selEnd.Col
}

func (e *Editor) IsSelecting() bool { return e.selecting }

// IsMouseDragging reports whether a mouse-button selection drag is in progress.
// Keyboard Shift+Arrow selections set selecting without mouseDragging.
func (e *Editor) IsMouseDragging() bool { return e.mouseDragging }

// CancelPointerCapture ends an in-progress mouse drag (and scrollbar thumb
// drag) without copying text. Keyboard-created selections are left intact
// because they never set mouseDragging.
func (e *Editor) CancelPointerCapture() {
	if e.mouseDragging {
		e.mouseDragging = false
		e.selecting = false
		e.selAnchor = Position{Line: 0, Col: 0}
		e.selEnd = Position{Line: 0, Col: 0}
	}
	if e.sbCtrl != nil {
		e.sbCtrl.CancelPointerCapture()
	}
}

func (e *Editor) SelectionRange() (startLine, startCol, endLine, endCol int) {
	if e.selAnchor.Line < e.selEnd.Line || (e.selAnchor.Line == e.selEnd.Line && e.selAnchor.Col <= e.selEnd.Col) {
		return e.selAnchor.Line, e.selAnchor.Col, e.selEnd.Line, e.selEnd.Col
	}
	return e.selEnd.Line, e.selEnd.Col, e.selAnchor.Line, e.selAnchor.Col
}

func (e *Editor) SelectedText() string {
	sLine, sCol, eLine, eCol := e.SelectionRange()
	if sLine == eLine && sCol == eCol {
		return ""
	}
	return e.buf.GetText(Position{Line: sLine, Col: sCol}, Position{Line: eLine, Col: eCol})
}

func (e *Editor) isSelected(line, col int) bool {
	if !e.HasSelection() {
		return false
	}
	sLine, sCol, eLine, eCol := e.SelectionRange()
	if line < sLine || line > eLine {
		return false
	}
	if line == sLine && col < sCol {
		return false
	}
	if line == eLine && col >= eCol {
		return false
	}
	return true
}

func (e *Editor) startSelection() {
	if !e.selecting {
		e.selecting = true
		e.selAnchor = e.cursor
		e.selEnd = e.cursor
	}
}

func (e *Editor) selectAll() {
	e.selecting = true
	e.selAnchor = Position{Line: 0, Col: 0}
	e.selEnd = Position{Line: e.buf.LineCount() - 1, Col: e.lineVisualWidth(e.buf.LineCount() - 1)}
}

func (e *Editor) clearSelectionIfNotExtending(extend bool) {
	if !extend {
		e.ClearSelection()
	}
}
