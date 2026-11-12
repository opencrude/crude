package editor

import (
	"crude/tui/ui"
	"strings"
)

// nativeCaretPlan is shared by one and several carets; it produces no effects.
// The batch owner checks all plans against the same source before writing.
func (e *Editor) nativeCaretPlan(id ui.ActionID, s SourceSelection) caretEdit {
	a, b := s.bounds()
	pos := e.buf.positionAtOffset(s.Cursor)
	p := replacement(a, a, "")
	switch id {
	case "edit.duplicateLine":
		if a != b {
			p = replacement(b, b, string(e.buf.cachedBytes[a:b]))
			p.anchor = 0
			return p
		}
		line := e.LineText(pos.Line)
		at := e.buf.positionToOffset(Position{Line: pos.Line, Col: e.lineVisualWidth(pos.Line)})
		p = replacement(at, at, e.lineEnding()+line)
		p.anchor = len(e.lineEnding()) + VisualColToByteOffset(line, pos.Col, e.TabWidth())
		p.cursor = p.anchor
	case "edit.upperCase", "edit.lowerCase", "edit.toggleCase":
		if a == b {
			left, right := e.pointerUnitRange(pointerWord, pos)
			a, b = e.buf.positionToOffset(left), e.buf.positionToOffset(right)
		}
		text := string(e.buf.cachedBytes[a:b])
		changed := strings.ToUpper(text)
		if id == "edit.lowerCase" || id == "edit.toggleCase" && changed == text {
			changed = strings.ToLower(text)
		}
		p = replacement(a, b, changed)
		p.anchor = 0
	case "edit.lineAbove":
		at := e.buf.positionToOffset(Position{Line: pos.Line})
		indent := leadingWhitespace(e.LineText(pos.Line))
		p = replacement(at, at, indent+e.lineEnding())
		p.anchor, p.cursor = len(indent), len(indent)
	case "edit.lineBelow":
		at := e.buf.positionToOffset(Position{Line: pos.Line, Col: e.lineVisualWidth(pos.Line)})
		p = e.newlineCaretEdit(at, at)
	case "edit.transpose":
		if a != b {
			return p
		}
		right := pos
		if right.Col == e.lineVisualWidth(right.Line) {
			right = e.buf.previousPosition(right)
		}
		if right.Line != pos.Line || right.Col == 0 {
			return p
		}
		left, end := e.buf.previousPosition(right), e.buf.nextPosition(right)
		if left.Line != right.Line || end.Line != right.Line {
			return p
		}
		p = replacement(e.buf.positionToOffset(left), e.buf.positionToOffset(end), e.buf.GetText(right, end)+e.buf.GetText(left, right))
	}
	return p
}
func caretPlannedAction(id ui.ActionID) bool {
	switch id {
	case "edit.duplicateLine", "edit.upperCase", "edit.lowerCase", "edit.toggleCase", "edit.lineAbove", "edit.lineBelow", "edit.transpose":
		return true
	}
	return false
}
func (e *Editor) applyNativeCaretAction(id ui.ActionID) error {
	e.buf.ensureCache()
	var plans []caretEdit
	for _, s := range e.selectionSpans() {
		p := e.nativeCaretPlan(id, s.SourceSelection)
		p.primary = s.primary
		plans = append(plans, p)
	}
	return e.applyCaretEdits(plans)
}
