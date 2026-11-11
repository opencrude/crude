package editor

// VisibleSourceLines is constant-size geometry, not an export or source scan.
// The range ends at the last painted source row, inclusive. Callers capture the
// final line's UTF-16 EOL only when admitting a new visible-range request.
func (e *Editor) VisibleSourceLines() (first, last int, ok bool) {
	if !e.IsOpen() || e.viewW < 10 || e.viewH < 4 || e.LineCount() == 0 {
		return 0, 0, false
	}
	first = min(max(0, e.scrollY), e.LineCount()-1)
	last = min(e.LineCount()-1, e.rowAt(e.scrollRow()+max(1, e.bodyHeight())-1).line)
	return first, last, true
}

// HasInlayProjection also covers qualified empty/null/unsupported results. It
// does not assert that any label exists or that project context is complete.
func (e *Editor) HasInlayProjection() bool { return e.currentInlays() != nil }
