package editor

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"crude/tui/ui"
)

// ActionMsg is an exact native operation, not synthetic keyboard input. It is
// retained with queued history so menus/control and keys share recovery.
type ActionMsg struct{ ID ui.ActionID }

func (e *Editor) handleAction(msg ActionMsg) tea.Cmd {
	action, ok := ui.FindEditorAction(msg.ID)
	if !ok || e.readOnly && action.MutatesText {
		return nil
	}
	if e.DeferDocumentEvent(msg) {
		return nil
	}
	if msg.ID != "select.expand" && msg.ID != "select.shrink" {
		e.expansion = nil
	}
	e.stopPointerSelection()
	e.DismissGhostText()
	e.landing = nil
	e.typingGroup = 0
	e.cursorBlink.MarkActivity()
	if action.MutatesText {
		e.CancelSnippet()
		e.beginUndoGroup()
		defer e.endUndoGroup()
	}
	switch msg.ID {
	case "edit.duplicateLine":
		e.duplicateLineOrSelection()
	case "edit.moveLineUp":
		e.moveLinesUp()
	case "edit.moveLineDown":
		e.moveLinesDown()
	case "edit.deleteLine":
		e.deleteLines()
	case "edit.joinLines":
		e.joinLines()
	case "edit.lineBelow":
		e.startLine(false)
	case "edit.lineAbove":
		e.startLine(true)
	case "edit.indent":
		e.indentLines()
	case "edit.outdent":
		e.outdentLines()
	case "edit.toggleComment":
		e.toggleLineComment()
	case "edit.upperCase", "edit.lowerCase", "edit.toggleCase":
		e.changeCase(msg.ID)
	case "edit.sortLines":
		e.orderLines(false)
	case "edit.reverseLines":
		e.orderLines(true)
	case "edit.transpose":
		e.transposeCharacters()
	case "move.matchingBracket", "select.matchingBracket":
		e.moveToMatchingBracket(msg.ID == "select.matchingBracket")
	case "select.line":
		e.selectCurrentLine()
	case "select.expand":
		e.expandSelection()
	case "select.shrink":
		e.shrinkSelection()
	case "move.paragraphPrevious", "move.paragraphNext", "select.paragraphPrevious", "select.paragraphNext":
		e.actionMotion(strings.HasPrefix(string(msg.ID), "select."), func() { e.moveParagraph(strings.HasSuffix(string(msg.ID), "Next")) })
	case "move.subwordPrevious", "move.subwordNext", "select.subwordPrevious", "select.subwordNext":
		e.actionMotion(strings.HasPrefix(string(msg.ID), "select."), func() { e.moveSubword(strings.HasSuffix(string(msg.ID), "Next")) })
	}
	return nil
}

func (e *Editor) actionMotion(extend bool, move func()) {
	e.clearSelectionIfNotExtending(extend)
	if extend {
		e.startSelection()
	}
	move()
	if extend {
		e.selEnd = e.cursor
	}
	e.clearGoal()
	e.ensureCursorVisible()
}

func (e *Editor) startLine(above bool) {
	line := e.cursor.Line
	raw := e.LineText(line)
	indent := raw[:len(raw)-len(strings.TrimLeft(raw, " \t"))]
	e.ClearSelection()
	if !above {
		e.cursor = Position{Line: line, Col: e.lineVisualWidth(line)}
		e.insertNewline()
		return
	}
	e.insertRecorded(Position{Line: line}, indent+e.lineEnding(), e.cursor)
	e.cursor = Position{Line: line, Col: LineVisualWidth(indent, e.TabWidth())}
	e.finishEdit()
}

// Joining is deliberately lexical: combine lines with one space, not a guessed
// language-specific rewrite of strings, comments, operators or punctuation.
func (e *Editor) joinLines() {
	first, last := e.selectedLineRange()
	selected := e.HasSelection()
	if first == last {
		last = min(last+1, e.LineCount()-1)
	}
	if first == last {
		return
	}
	firstText := strings.TrimRight(e.LineText(first), " \t")
	seam := LineVisualWidth(firstText, e.TabWidth())
	var joined strings.Builder
	joined.WriteString(firstText)
	for line := first + 1; line <= last; line++ {
		next := strings.Trim(e.LineText(line), " \t")
		if joined.Len() > 0 && next != "" {
			joined.WriteByte(' ')
		}
		joined.WriteString(next)
	}
	text := joined.String()
	start, end := Position{Line: first}, Position{Line: last, Col: e.lineVisualWidth(last)}
	e.spliceRecorded(e.buf.positionToOffset(start), e.buf.positionToOffset(end), text, e.cursor)
	e.ClearSelection()
	e.cursor = Position{Line: first, Col: seam}
	if selected {
		e.selAnchor = start
		e.selEnd = Position{Line: first, Col: LineVisualWidth(text, e.TabWidth())}
		e.selecting = true
		e.cursor = e.selEnd
	}
	e.finishEdit()
}

func (e *Editor) changeCase(action ui.ActionID) {
	var start, end Position
	if e.HasSelection() {
		sl, sc, el, ec := e.SelectionRange()
		start, end = Position{Line: sl, Col: sc}, Position{Line: el, Col: ec}
	} else {
		start, end = e.pointerUnitRange(pointerWord, e.cursor)
	}
	text := e.buf.GetText(start, end)
	if text == "" {
		return
	}
	changed := strings.ToUpper(text)
	if action == "edit.lowerCase" || action == "edit.toggleCase" && changed == text {
		changed = strings.ToLower(text)
	}
	if text == changed {
		return
	}
	off := e.buf.positionToOffset(start)
	e.spliceRecorded(off, e.buf.positionToOffset(end), changed, e.cursor)
	e.selectOffsets(off, off+len(changed))
	e.finishEdit()
}

func (e *Editor) orderLines(reverse bool) {
	first, last := e.selectedLineRange()
	if !e.HasSelection() {
		first, last = 0, e.LineCount()-1
		if last > 0 && e.LineText(last) == "" {
			last--
		}
	}
	if first >= last {
		return
	}
	lines := make([]string, 0, last-first+1)
	for i := first; i <= last; i++ {
		lines = append(lines, e.LineText(i))
	}
	if reverse {
		slices.Reverse(lines)
	} else {
		slices.Sort(lines)
	}
	start, end := Position{Line: first}, Position{Line: last, Col: e.lineVisualWidth(last)}
	replacement := strings.Join(lines, e.lineEnding())
	if replacement == e.buf.GetText(start, end) {
		return
	}
	off := e.buf.positionToOffset(start)
	e.spliceRecorded(off, e.buf.positionToOffset(end), replacement, e.cursor)
	e.selectOffsets(off, off+len(replacement))
	e.finishEdit()
}

func (e *Editor) transposeCharacters() {
	if e.HasSelection() {
		return
	}
	right := e.cursor
	if right.Col == e.lineVisualWidth(right.Line) {
		right = e.buf.previousPosition(right)
	}
	if right.Line != e.cursor.Line || right.Col == 0 {
		return
	}
	left, end := e.buf.previousPosition(right), e.buf.nextPosition(right)
	if left.Line != right.Line || end.Line != right.Line {
		return
	}
	text := e.buf.GetText(right, end) + e.buf.GetText(left, right)
	off := e.buf.positionToOffset(left)
	e.spliceRecorded(off, e.buf.positionToOffset(end), text, e.cursor)
	e.cursor = e.buf.positionAtOffset(off + len(text))
	e.finishEdit()
}

func (e *Editor) selectOffsets(start, end int) {
	e.selAnchor, e.selEnd = e.buf.positionAtOffset(start), e.buf.positionAtOffset(end)
	e.selecting = start != end
	e.cursor = e.selEnd
	e.clearGoal()
	e.ensureCursorVisible()
}

func (e *Editor) selectCurrentLine() {
	line := e.cursor.Line
	start := e.buf.positionToOffset(Position{Line: line})
	end := e.buf.positionToOffset(Position{Line: line, Col: e.lineVisualWidth(line)})
	if line < e.LineCount()-1 {
		end = e.buf.positionToOffset(Position{Line: line + 1})
	}
	// Repeated Select Line extends down rather than reselecting the same line.
	if e.HasSelection() {
		sl, sc, _, _ := e.SelectionRange()
		start = e.buf.positionToOffset(Position{Line: sl, Col: sc})
	}
	e.selectOffsets(start, end)
}

func (e *Editor) moveParagraph(next bool) {
	line := e.cursor.Line
	blank := func(i int) bool { return strings.TrimSpace(e.LineText(i)) == "" }
	if next {
		if line == e.LineCount()-1 {
			e.cursor.Col = e.lineVisualWidth(line)
			return
		}
		line++
		if blank(e.cursor.Line) {
			for line < e.LineCount()-1 && blank(line) {
				line++
			}
		} else {
			for line < e.LineCount()-1 && !blank(line) {
				line++
			}
		}
	} else {
		if line == 0 {
			e.cursor.Col = 0
			return
		}
		line--
		if blank(e.cursor.Line) {
			for line > 0 && blank(line) {
				line--
			}
		} else {
			for line > 0 && !blank(line) {
				line--
			}
		}
	}
	e.cursor = Position{Line: line}
}

func subwordBoundary(rs []rune, i int) bool {
	if i == 0 || i == len(rs) {
		return true
	}
	a, b := rs[i-1], rs[i]
	if unicode.IsSpace(a) != unicode.IsSpace(b) || isWordChar(a) != isWordChar(b) {
		return true
	}
	if a == '_' || b == '_' {
		return a != b
	}
	if unicode.IsDigit(a) != unicode.IsDigit(b) {
		return true
	}
	if unicode.IsLower(a) && unicode.IsUpper(b) {
		return true
	}
	return unicode.IsUpper(a) && unicode.IsUpper(b) && i+1 < len(rs) && unicode.IsLower(rs[i+1])
}

func (e *Editor) moveSubword(next bool) {
	rs, cols, index := e.wordGeometry(e.cursor.Line, e.cursor.Col)
	if next {
		if index == len(rs) {
			e.moveRight()
			return
		}
		index++
		for index < len(rs) && !subwordBoundary(rs, index) {
			index++
		}
	} else {
		if index == 0 {
			e.moveLeft()
			return
		}
		index--
		for index > 0 && !subwordBoundary(rs, index) {
			index--
		}
	}
	e.cursor.Col = cols[index]
}

// Only an uninterrupted selection-growth chain owns these prior ranges. Source,
// buffer or independent cursor/selection changes invalidate it before reuse.
type selectionExpansion struct {
	buffer   *PieceTable
	revision uint64
	expected editorViewState
	previous []editorViewState
}

func (e *Editor) selectionExpansionCurrent() bool {
	s := e.expansion
	return s != nil && s.buffer == e.buf && s.revision == e.Revision() && s.expected.Cursor == e.buf.positionToOffset(e.cursor) && s.expected.Anchor == e.buf.positionToOffset(e.selAnchor) && s.expected.End == e.buf.positionToOffset(e.selEnd) && s.expected.Selecting == e.selecting
}
func (e *Editor) expandSelection() {
	if !e.selectionExpansionCurrent() {
		e.expansion = &selectionExpansion{buffer: e.buf, revision: e.Revision()}
	}
	start, end, ok := e.SourceByteRange()
	if !ok {
		return
	}
	var candidate [2]int
	found := false
	offer := func(a, b int) {
		if a <= start && b >= end && (a < start || b > end) && (!found || b-a < candidate[1]-candidate[0]) {
			candidate = [2]int{a, b}
			found = true
		}
	}
	ranges, brackets := e.structuralRanges()
	for _, r := range ranges {
		offer(int(r.Start), int(r.End))
	}
	for _, r := range brackets {
		if r.Offset < r.Mate {
			offer(int(r.Offset)+1, int(r.Mate))
			offer(int(r.Offset), int(r.Mate)+1)
		}
	}
	wordStart, wordEnd := e.pointerUnitRange(pointerWord, e.cursor)
	offer(e.buf.positionToOffset(wordStart), e.buf.positionToOffset(wordEnd))
	sl, el := e.buf.positionAtOffset(start).Line, e.buf.positionAtOffset(end).Line
	raw := e.LineText(sl)
	if sl == el {
		off := e.buf.positionToOffset(Position{Line: sl})
		offer(off+len(raw)-len(strings.TrimLeft(raw, " \t")), off+len(strings.TrimRight(raw, " \t")))
	}
	lineStart := e.buf.positionToOffset(Position{Line: sl})
	lineEnd := e.buf.positionToOffset(Position{Line: el, Col: e.lineVisualWidth(el)})
	offer(lineStart, lineEnd)
	if el < e.LineCount()-1 {
		offer(lineStart, e.buf.positionToOffset(Position{Line: el + 1}))
	}
	first, last := sl, el
	for first > 0 && strings.TrimSpace(e.LineText(first-1)) != "" {
		first--
	}
	for last < e.LineCount()-1 && strings.TrimSpace(e.LineText(last+1)) != "" {
		last++
	}
	offer(e.buf.positionToOffset(Position{Line: first}), e.buf.positionToOffset(Position{Line: last, Col: e.lineVisualWidth(last)}))
	e.buf.ensureCache()
	offer(0, len(e.buf.cachedBytes))
	if !found {
		return
	}
	previous := e.captureViewState()
	e.selectOffsets(candidate[0], candidate[1])
	e.expansion.previous = append(e.expansion.previous, previous)
	e.expansion.expected = e.captureViewState()
}
func (e *Editor) shrinkSelection() {
	if !e.selectionExpansionCurrent() {
		e.expansion = nil
		return
	}
	s := e.expansion
	if len(s.previous) == 0 {
		return
	}
	previous := s.previous[len(s.previous)-1]
	s.previous = s.previous[:len(s.previous)-1]
	e.restoreViewState(previous)
	e.clearGoal()
	e.ensureCursorVisible()
	s.expected = e.captureViewState()
}

// ActionToken binds an explicit command to this view, its source and selection.
// It is computed only for menus/control, never as a source export during paint.
func (e *Editor) ActionToken() string {
	queued := 0
	if p := e.documentProjection; p != nil {
		queued = len(p.historyQueue)
	}
	start, end, valid := e.SourceByteRange()
	state := e.DocumentState()
	value := fmt.Sprintf("%p:%p:%d:%s:%d:%d:%d:%t:%d:%d:%t:%t:%t:%d:%d:%t:%t:%t", e, e.buf, e.Revision(), state.DocumentID, e.cursor.Line, e.cursor.Col, start, valid, end, queued, e.selecting, e.readOnly, e.open, state.Revision, state.PendingEdits, state.InFlight, state.UndoPending, state.HistoryQueued)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
}
