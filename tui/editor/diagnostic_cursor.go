package editor

// DiagnosticCursor is a constant-space observation of the currently eligible
// publication. Presentation lifecycle checks must not clone the entire batch.
func (e *Editor) DiagnosticCursor() (string, bool) {
	e.diagMu.RLock()
	defer e.diagMu.RUnlock()
	return e.diagnosticFacts.ID, e.diagnosticsCurrent()
}

// PositionScreenPosition projects an actual source caret boundary into the
// mounted editor's screen cells. Off-viewport source is not clamped into a fake
// visible anchor. Col is a source visual column, never UTF-16 or an inlay cell.
func (e *Editor) PositionScreenPosition(pos Position, originX, originY int) (int, int, bool) {
	row := pos.Line - e.scrollY
	column := e.displayColumn(pos.Line, pos.Col) - e.scrollX
	x, y := originX+e.PadL+e.gutterWidth()+column, originY+row
	width := max(1, e.viewW-e.gutterWidth()-e.scrollbarColWidth())
	return x, y, e.open && row >= 0 && row < e.bodyHeight() && column >= 0 && column < width
}
