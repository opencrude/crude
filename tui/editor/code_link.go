package editor

import tea "charm.land/bubbletea/v2"

// Code links: as in established IDEs, holding Ctrl over a symbol underlines
// it, and Ctrl+click goes to its definition. Ctrl+Alt+click goes to its
// implementations. The editor only places the caret at the clicked symbol and
// reports the request; the shell owns the language-server query and opens the
// result in the pane that was clicked.

// NavigateAtMsg asks the shell to run a code navigation from Editor's caret,
// which the click has just placed on the symbol.
type NavigateAtMsg struct {
	Editor         *Editor
	Implementation bool
}

// codeLink is the source range underlined while Ctrl is held over it.
type codeLink struct {
	line, start, end int // source visual columns [start, end)
}

// codeLinkModifier reports whether a pointer event asks for a code link.
func codeLinkModifier(mod tea.KeyMod) bool {
	return mod.Contains(tea.ModCtrl) && !mod.Contains(tea.ModShift)
}

// wordAt returns the identifier-like source range under pos, if any.
func (e *Editor) wordAt(pos Position) (codeLink, bool) {
	if pos.Line < 0 || pos.Line >= e.LineCount() || pos.Col >= e.lineVisualWidth(pos.Line) {
		return codeLink{}, false
	}
	start, end := e.pointerUnitRange(pointerWord, pos)
	if start.Line != pos.Line || end.Line != pos.Line || end.Col <= start.Col {
		return codeLink{}, false
	}
	text := e.buf.GetText(start, end)
	for _, r := range text {
		if !isWordChar(r) {
			return codeLink{}, false
		}
	}
	return codeLink{line: pos.Line, start: start.Col, end: end.Col}, true
}

// updateCodeLink tracks the Ctrl+hover underline. It reports whether the
// underline changed, so the pane repaints only then.
func (e *Editor) updateCodeLink(x, y int, mod tea.KeyMod) bool {
	var next *codeLink
	if codeLinkModifier(mod) && !e.IsMouseDragging() && !e.IsScrollbarDragging() && y >= 0 && y < e.bodyHeight() &&
		x >= e.gutterWidth() && !e.inlineInsertionAt(x, y) {
		if pos, ok := e.mouseToPosition(x, y); ok {
			if link, ok := e.wordAt(pos); ok {
				next = &link
			}
		}
	}
	if next == nil && e.link == nil || next != nil && e.link != nil && *next == *e.link {
		return false
	}
	e.link = next
	return true
}

// ClearCodeLink removes the Ctrl+hover underline (pointer left, key released).
func (e *Editor) ClearCodeLink() { e.link = nil }

// codeLinkAt reports whether a source column is inside the underlined link.
func (e *Editor) codeLinkAt(line, col int) bool {
	return e.link != nil && e.link.line == line && col >= e.link.start && col < e.link.end
}

// handleCodeLinkClick places the caret on the symbol under a Ctrl+click and
// returns the navigation request; ok is false when the click is not on a
// symbol, so it falls through to ordinary caret placement.
func (e *Editor) handleCodeLinkClick(msg tea.MouseClickMsg) (tea.Cmd, bool) {
	if msg.Button != tea.MouseLeft || !codeLinkModifier(msg.Mod) || msg.Y < 0 || msg.Y >= e.bodyHeight() ||
		msg.X < e.gutterWidth() || e.inlineInsertionAt(msg.X, msg.Y) {
		return nil, false
	}
	pos, ok := e.mouseToPosition(msg.X, msg.Y)
	if !ok {
		return nil, false
	}
	link, ok := e.wordAt(pos)
	if !ok {
		return nil, false
	}
	e.clearGoal()
	e.ClearSelection()
	e.cursor = pos
	e.link = &link
	e.clickCount = 0
	implementation := msg.Mod.Contains(tea.ModAlt)
	return func() tea.Msg { return NavigateAtMsg{Editor: e, Implementation: implementation} }, true
}
