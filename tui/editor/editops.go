package editor

import (
	"strings"

	"crude/document"
	"crude/insight/catalog"
)

// This file implements modern-IDE editing operations: auto-closing pairs,
// line indent/outdent, move/duplicate/delete lines, and comment toggling.
// Every operation records exactly one undo step (single change or one
// undo group).

// autoClosePairs maps opening characters to their closing partner.
var autoClosePairs = map[rune]rune{
	'(':  ')',
	'[':  ']',
	'{':  '}',
	'"':  '"',
	'\'': '\'',
}

func isPairCloser(r rune) bool {
	switch r {
	case ')', ']', '}', '"', '\'':
		return true
	}
	return false
}

// ── shared edit plumbing ─────────────────────────────────────────────

// insertRecorded inserts text at pos and records the change with an exact
// end position derived from byte offsets (safe for tab-containing text).
func (e *Editor) insertRecorded(pos Position, text string, cursorBefore Position) Position {
	pos = e.buf.snapPosition(pos)
	if text == "" {
		return pos
	}
	startOff := e.buf.positionToOffset(pos)
	e.spliceRecorded(startOff, startOff, text, cursorBefore)
	end := e.buf.positionAtOffset(startOff + len(text))
	if e.buf.positionToOffset(end) < startOff+len(text) {
		end = e.buf.nextPosition(end) // insertion caret has trailing affinity
	}
	return end
}

// deleteRecorded deletes [start, end) and records the change. Returns the
// deleted text ("" when the range was empty and nothing was recorded).
func (e *Editor) deleteRecorded(start, end Position, cursorBefore Position) string {
	return e.spliceRecorded(e.buf.positionToOffset(start), e.buf.positionToOffset(end), "", cursorBefore)
}

// spliceRecorded is the one exact-byte recorder for local editor mutations,
// including completion imports, search replacements and snippet mirrors.
// Callers validate the complete command before its first splice. No observer
// writes here: inverses are recorded before the outer transaction finalizes.
func (e *Editor) spliceRecorded(start, end int, text string, cursorBefore Position) string {
	e.buf.ensureCache()
	if start < 0 || end < start || end > len(e.buf.cachedBytes) {
		return ""
	}
	deleted := string(e.buf.cachedBytes[start:end])
	if deleted == text {
		return deleted
	}
	if err := e.spliceRecordedBatch([]document.Edit{{Start: start, End: end, Text: text}}, cursorBefore); err != nil {
		return ""
	}
	return deleted
}

// spliceRecordedBatch applies one checked simultaneous vector, retaining sparse
// inverses in the same editor/core group. PieceTable owns the complete mutation;
// neither mirrors nor history maintain a second byte writer or derived cache.
func (e *Editor) spliceRecordedBatch(edits []document.Edit, cursorBefore Position) error {
	if err := e.buf.checkOffsets(edits); err != nil {
		return err
	}
	// A transaction owns one before/after view, not one per sparse edit.
	// Its boundary snapshots are filled by startUndoGroup/endUndoGroup.
	standalone := e.documentProjection == nil && e.undoGroupDepth == 0
	var before, after editorViewState
	if standalone {
		before = e.captureViewState()
		before.Cursor = e.buf.positionToOffset(cursorBefore)
	}
	deleted := make([]string, len(edits))
	for i, edit := range edits {
		deleted[i] = string(e.buf.cachedBytes[edit.Start:edit.End])
	}
	previousOwner, previousActor := e.buf.editOwner, e.buf.linkActor
	e.buf.editOwner, e.buf.linkActor = e, e
	defer func() { e.buf.editOwner, e.buf.linkActor = previousOwner, previousActor }()
	if err := e.buf.applyOffsets(edits); err != nil {
		return err
	}
	if standalone {
		after = e.captureViewState()
	}
	for i := len(edits) - 1; i >= 0; i-- {
		edit := edits[i]
		if deleted[i] == edit.Text {
			continue
		}
		if deleted[i] != "" {
			e.recordChange(Change{Type: ActDelete, Text: []byte(deleted[i]), Before: before, After: after, Offset: edit.Start})
		}
		if edit.Text != "" {
			e.recordChange(Change{Type: ActInsert, Text: []byte(edit.Text), Before: before, After: after, Offset: edit.Start})
		}
	}
	return nil
}

// finishEdit applies the shared post-mutation bookkeeping for operations
// that call the buffer directly instead of going through insertText.
func (e *Editor) finishEdit() {
	e.clearGoal()
	e.invalidateEditorCaches()
	e.markDocumentWidthAll()
	e.ensureCursorVisible()
	e.notifyContentChanged()
}

func (e *Editor) clampCursorToLine() {
	lc := e.LineCount()
	if e.cursor.Line > lc-1 {
		e.cursor.Line = lc - 1
	}
	if e.cursor.Line < 0 {
		e.cursor.Line = 0
	}
	if w := e.lineVisualWidth(e.cursor.Line); e.cursor.Col > w {
		e.cursor.Col = w
	}
	if e.cursor.Col < 0 {
		e.cursor.Col = 0
	}
	e.cursor = e.buf.snapPosition(e.cursor)
}

func (e *Editor) runeAtCursor() rune {
	line := e.LineText(e.cursor.Line)
	off := VisualColToByteOffset(line, e.cursor.Col, e.TabWidth())
	if off >= len(line) {
		return 0
	}
	return []rune(line[off:])[0]
}

func (e *Editor) runeBeforeCursor() rune {
	if e.cursor.Col <= 0 {
		return 0
	}
	start := e.buf.previousPosition(e.cursor)
	if start.Line != e.cursor.Line {
		return 0
	}
	text := e.buf.GetText(start, e.cursor)
	if text == "" {
		return 0
	}
	return []rune(text)[0]
}

// ── auto-indent helpers ──────────────────────────────────────────────

func leadingWhitespace(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\t' {
			return s[:i]
		}
	}
	return s
}

func lastRune(s string) (rune, bool) {
	if s == "" {
		return 0, false
	}
	runes := []rune(s)
	return runes[len(runes)-1], true
}

// ── auto-closing pairs ───────────────────────────────────────────────

// typeRune handles a printable typed character: wraps the selection when
// an opener is typed over one, types over an already-present closer,
// auto-inserts closing pairs, and otherwise inserts the rune as-is.
func (e *Editor) typeRune(r rune) {
	e.beginUndoGroup()
	defer e.endUndoGroup()
	closer, isOpener := autoClosePairs[r]
	if isOpener && e.HasSelection() {
		e.wrapSelection(r, closer)
		return
	}
	if isPairCloser(r) && e.runeAtCursor() == r {
		e.moveRight()
		return
	}
	if isOpener && e.shouldAutoClose(r) {
		e.insertText(string(r) + string(closer))
		e.cursor.Col -= runeVisualWidth(e.TabWidth(), closer)
		e.ensureCursorVisible()
		return
	}
	e.insertRune(r)
}

// shouldAutoClose reports whether typing opener r should insert its
// closing pair. Pairs open only before EOL, whitespace, or punctuation
// that commonly follows an insertion point; quotes additionally refuse
// to pair right after a word character or the same quote.
func (e *Editor) shouldAutoClose(r rune) bool {
	switch next := e.runeAtCursor(); next {
	case 0, ' ', '\t', ')', ']', '}', ';', ':', ',', '.', '=', '>':
	default:
		return false
	}
	if r == '"' || r == '\'' {
		prev := e.runeBeforeCursor()
		if prev == r || isWordChar(prev) {
			return false
		}
	}
	return true
}

// wrapSelection surrounds the current selection with opener/closer and
// keeps the wrapped text selected. One undo step.
func (e *Editor) wrapSelection(opener, closer rune) {
	sLine, sCol, eLine, eCol := e.SelectionRange()
	start := Position{Line: sLine, Col: sCol}
	end := Position{Line: eLine, Col: eCol}
	cursorBefore := e.cursor
	ow := runeVisualWidth(e.TabWidth(), opener)

	e.beginUndoGroup()
	defer e.endUndoGroup()
	e.insertRecorded(end, string(closer), cursorBefore)
	e.insertRecorded(start, string(opener), cursorBefore)

	newStart := Position{Line: sLine, Col: sCol + ow}
	newEnd := Position{Line: eLine, Col: eCol}
	if eLine == sLine {
		newEnd.Col += ow
	}
	e.selecting = true
	e.selAnchor = newStart
	e.selEnd = newEnd
	e.cursor = newEnd
	e.finishEdit()
}

// deleteBackwardSmart deletes the selection if one is active, removes
// both characters of an empty auto-close pair when the cursor sits
// between them, and otherwise falls back to a plain backward delete.
func (e *Editor) deleteBackwardSmart() {
	e.beginUndoGroup()
	defer e.endUndoGroup()
	if e.HasSelection() {
		e.DeleteSelection()
		return
	}
	prev := e.runeBeforeCursor()
	if closer, ok := autoClosePairs[prev]; ok && e.runeAtCursor() == closer {
		cursorBefore := e.cursor
		start := Position{Line: e.cursor.Line, Col: e.cursor.Col - runeVisualWidth(e.TabWidth(), prev)}
		end := Position{Line: e.cursor.Line, Col: e.cursor.Col + runeVisualWidth(e.TabWidth(), closer)}
		e.deleteRecorded(start, end, cursorBefore)
		e.cursor = start
		e.finishEdit()
		return
	}
	e.deleteBackward()
}

// ── line-range helpers ───────────────────────────────────────────────

// selectedLineRange returns the inclusive line range covered by the
// selection (or just the cursor line). A multi-line selection ending at
// column 0 excludes that final line, matching editor conventions.
func (e *Editor) selectedLineRange() (int, int) {
	if !e.HasSelection() {
		return e.cursor.Line, e.cursor.Line
	}
	sLine, _, eLine, eCol := e.SelectionRange()
	if eLine > sLine && eCol == 0 {
		eLine--
	}
	return sLine, eLine
}

// ── indent / outdent ─────────────────────────────────────────────────

// indentLines inserts one indent level at the start of every non-empty
// selected line, preserving the selection. One undo step.
func (e *Editor) indentLines() {
	first, last := e.selectedLineRange()
	style := e.indentStyle()
	indent, width := style.unit(), style.width()
	cursorBefore := e.cursor
	indented := make(map[int]bool, last-first+1)

	e.beginUndoGroup()
	defer e.endUndoGroup()
	for line := first; line <= last; line++ {
		if e.LineText(line) == "" {
			continue
		}
		e.insertRecorded(Position{Line: line}, indent, cursorBefore)
		indented[line] = true
	}
	if len(indented) == 0 {
		return
	}

	adjust := func(p Position) Position {
		if indented[p.Line] && p.Col > 0 {
			p.Col += width
		}
		return p
	}
	e.cursor = adjust(e.cursor)
	if e.HasSelection() {
		e.selAnchor = adjust(e.selAnchor)
		e.selEnd = adjust(e.selEnd)
	}
	e.finishEdit()
}

// outdentLines removes up to one indent level (a leading tab, or up to one
// level of the document's spaces) from every selected line, preserving the
// selection. One undo step.
func (e *Editor) outdentLines() {
	first, last := e.selectedLineRange()
	level := e.indentStyle().width()
	cursorBefore := e.cursor
	removed := make(map[int]int, last-first+1)

	e.beginUndoGroup()
	defer e.endUndoGroup()
	for line := first; line <= last; line++ {
		raw := e.LineText(line)
		if raw == "" {
			continue
		}
		var nBytes, cols int
		if raw[0] == '\t' {
			nBytes = 1
			cols = TabStopAdvance(0, e.TabWidth())
		} else {
			for nBytes < len(raw) && nBytes < level && raw[nBytes] == ' ' {
				nBytes++
			}
			cols = nBytes
		}
		if nBytes == 0 {
			continue
		}
		e.deleteRecorded(Position{Line: line}, Position{Line: line, Col: cols}, cursorBefore)
		removed[line] = cols
	}
	if len(removed) == 0 {
		return
	}

	adjust := func(p Position) Position {
		if d := removed[p.Line]; d > 0 {
			p.Col -= d
			if p.Col < 0 {
				p.Col = 0
			}
		}
		return p
	}
	e.cursor = adjust(e.cursor)
	if e.HasSelection() {
		e.selAnchor = adjust(e.selAnchor)
		e.selEnd = adjust(e.selEnd)
	}
	e.finishEdit()
}

// ── move lines ───────────────────────────────────────────────────────

func (e *Editor) moveLinesUp() {
	first, last := e.selectedLineRange()
	if first <= 0 {
		return
	}
	e.swapLineBlock(first-1, first, last, -1)
}

func (e *Editor) moveLinesDown() {
	first, last := e.selectedLineRange()
	if last >= e.LineCount()-1 {
		return
	}
	e.swapLineBlock(last+1, first, last, 1)
}

// swapLineBlock moves the block [first, last] one line in direction dir
// by swapping it with the adjacent line at swapIdx. Cursor and selection
// travel with the block. One undo step.
func (e *Editor) swapLineBlock(swapIdx, first, last, dir int) {
	block := make([]string, 0, last-first+2)
	for i := first; i <= last; i++ {
		block = append(block, e.LineText(i))
	}
	swapLine := e.LineText(swapIdx)

	var newLines []string
	var regionFirst, regionLast int
	if dir < 0 {
		regionFirst, regionLast = swapIdx, last
		newLines = append(block, swapLine)
	} else {
		regionFirst, regionLast = first, swapIdx
		newLines = append([]string{swapLine}, block...)
	}
	start := Position{Line: regionFirst}
	end := Position{Line: regionLast, Col: e.lineVisualWidth(regionLast)}
	replacement := strings.Join(newLines, e.lineEnding())
	cursorBefore := e.cursor

	e.beginUndoGroup()
	defer e.endUndoGroup()
	if e.buf.GetText(start, end) != replacement {
		e.deleteRecorded(start, end, cursorBefore)
		e.insertRecorded(start, replacement, cursorBefore)
	}

	e.cursor.Line += dir
	if e.HasSelection() {
		e.selAnchor.Line += dir
		e.selEnd.Line += dir
	}
	e.clampCursorToLine()
	e.finishEdit()
}

// ── duplicate ────────────────────────────────────────────────────────

// duplicateLineOrSelection copies the selected text right after the
// selection (selecting the copy), or duplicates the current line below
// when nothing is selected. One undo step.
func (e *Editor) duplicateLineOrSelection() {
	e.beginUndoGroup()
	defer e.endUndoGroup()
	cursorBefore := e.cursor
	if e.HasSelection() {
		_, _, eLine, eCol := e.SelectionRange()
		text := e.SelectedText()
		if text == "" {
			return
		}
		insertPos := Position{Line: eLine, Col: eCol}
		end := e.insertRecorded(insertPos, text, cursorBefore)
		e.selecting = true
		e.selAnchor = insertPos
		e.selEnd = end
		e.cursor = end
		e.finishEdit()
		return
	}
	line := e.cursor.Line
	text := e.LineText(line)
	e.insertRecorded(Position{Line: line, Col: e.lineVisualWidth(line)}, e.lineEnding()+text, cursorBefore)
	e.cursor.Line = line + 1
	e.clampCursorToLine()
	e.finishEdit()
}

// ── delete lines ─────────────────────────────────────────────────────

// deleteLines removes every line touched by the selection (or the cursor
// line), including the trailing newline so the line count shrinks. The
// cursor keeps its column on the line that takes the deleted lines'
// place. One undo step.
func (e *Editor) deleteLines() {
	e.beginUndoGroup()
	defer e.endUndoGroup()
	first, last := e.selectedLineRange()
	lc := e.LineCount()
	cursorBefore := e.cursor

	var start, end Position
	switch {
	case last < lc-1:
		start = Position{Line: first}
		end = Position{Line: last + 1}
	case first > 0:
		start = Position{Line: first - 1, Col: e.lineVisualWidth(first - 1)}
		end = Position{Line: last, Col: e.lineVisualWidth(last)}
	default:
		start = Position{}
		end = Position{Line: last, Col: e.lineVisualWidth(last)}
	}
	if e.deleteRecorded(start, end, cursorBefore) == "" {
		return
	}
	e.ClearSelection()
	e.cursor = Position{Line: first, Col: cursorBefore.Col}
	e.clampCursorToLine()
	e.finishEdit()
}

// ── toggle line comment ──────────────────────────────────────────────

// commentTokens returns the current file's line-comment prefix and block
// comment delimiters from the language catalog. Either may be empty.
func (e *Editor) commentTokens() (line, blockStart, blockEnd string) {
	path := e.filePath
	if path == "" {
		path = e.title
	}
	c := catalog.Current()
	language, ok := c.LanguageForPath(path, nil)
	if !ok {
		return "", "", ""
	}
	if len(language.CommentTokens) > 0 {
		line = language.CommentTokens[0]
	}
	if language.BlockComment != nil {
		blockStart, blockEnd = language.BlockComment.Start, language.BlockComment.End
	}
	return line, blockStart, blockEnd
}

// toggleLineComment comments or uncomments every selected line using the
// file's line-comment prefix, or its block delimiters when it has no prefix. When all non-blank selected lines are
// already commented they are uncommented; otherwise every non-blank line
// gains the prefix after its leading whitespace. One undo step.
func (e *Editor) toggleLineComment() {
	prefix, blockStart, blockEnd := e.commentTokens()
	if prefix == "" {
		// A language with block comments only (markup, style sheets) comments
		// each selected line with its own pair of delimiters.
		if blockStart != "" && blockEnd != "" {
			e.toggleBlockCommentLines(blockStart, blockEnd)
		}
		return
	}
	first, last := e.selectedLineRange()

	allCommented := true
	anyNonBlank := false
	for line := first; line <= last; line++ {
		trimmed := strings.TrimLeft(e.LineText(line), " \t")
		if trimmed == "" {
			continue
		}
		anyNonBlank = true
		if !strings.HasPrefix(trimmed, prefix) {
			allCommented = false
			break
		}
	}
	if !anyNonBlank {
		return
	}

	cursorBefore := e.cursor
	type lineShift struct {
		at    int
		delta int
	}
	shifts := make(map[int]lineShift, last-first+1)

	e.beginUndoGroup()
	defer e.endUndoGroup()
	for line := first; line <= last; line++ {
		raw := e.LineText(line)
		wsLen := len(raw) - len(strings.TrimLeft(raw, " \t"))
		trimmed := raw[wsLen:]
		if trimmed == "" {
			continue
		}
		insCol := ByteOffsetToVisualCol(raw, wsLen, e.TabWidth())
		if allCommented {
			remove := prefix
			if strings.HasPrefix(trimmed, prefix+" ") {
				remove = prefix + " "
			}
			start := Position{Line: line, Col: insCol}
			end := Position{Line: line, Col: insCol + len(remove)}
			e.deleteRecorded(start, end, cursorBefore)
			shifts[line] = lineShift{at: insCol, delta: -len(remove)}
		} else {
			ins := prefix + " "
			e.insertRecorded(Position{Line: line, Col: insCol}, ins, cursorBefore)
			shifts[line] = lineShift{at: insCol, delta: len(ins)}
		}
	}
	if len(shifts) == 0 {
		return
	}

	adjust := func(p Position) Position {
		s, ok := shifts[p.Line]
		if !ok || p.Col < s.at {
			return p
		}
		p.Col += s.delta
		if p.Col < s.at {
			p.Col = s.at
		}
		return p
	}
	e.cursor = adjust(e.cursor)
	if e.HasSelection() {
		e.selAnchor = adjust(e.selAnchor)
		e.selEnd = adjust(e.selEnd)
	}
	e.finishEdit()
}

// toggleBlockCommentLines wraps every non-blank selected line in the block
// delimiters, after its indentation and before its trailing whitespace. When
// every non-blank line is already wrapped, it removes the delimiters and the
// one space inside each. One undo step.
func (e *Editor) toggleBlockCommentLines(start, end string) {
	first, last := e.selectedLineRange()
	type span struct {
		indent, content int
		wrapped         bool
	}
	spans := make(map[int]span, last-first+1)
	allCommented := true
	for line := first; line <= last; line++ {
		raw := e.LineText(line)
		indent := len(raw) - len(strings.TrimLeft(raw, " \t"))
		content := len(strings.TrimRight(raw, " \t"))
		if content <= indent {
			continue
		}
		body := raw[indent:content]
		wrapped := len(body) >= len(start)+len(end) && strings.HasPrefix(body, start) && strings.HasSuffix(body, end)
		if !wrapped && (strings.Contains(body, start) || strings.Contains(body, end)) {
			return // partial block delimiters cannot be safely nested
		}
		spans[line] = span{indent, content, wrapped}
		allCommented = allCommented && wrapped
	}
	if len(spans) == 0 {
		return
	}
	cursorBefore := e.cursor
	type shift struct{ at, head, tailAt, tail int } // visual columns and deltas
	shifts := make(map[int]shift, len(spans))

	e.beginUndoGroup()
	defer e.endUndoGroup()
	for line := first; line <= last; line++ {
		sp, ok := spans[line]
		if !ok || !allCommented && sp.wrapped {
			continue
		}
		raw := e.LineText(line)
		headCol := ByteOffsetToVisualCol(raw, sp.indent, e.TabWidth())
		tailCol := ByteOffsetToVisualCol(raw, sp.content, e.TabWidth())
		if allCommented {
			body := raw[sp.indent:sp.content]
			head, tail := start, end
			inner := body[len(start) : len(body)-len(end)]
			if strings.HasPrefix(inner, " ") {
				head += " "
			}
			if strings.HasSuffix(inner, " ") && len(inner) > 1 {
				tail = " " + tail
			}
			tailStart := ByteOffsetToVisualCol(raw, sp.content-len(tail), e.TabWidth())
			e.deleteRecorded(Position{Line: line, Col: tailStart}, Position{Line: line, Col: tailCol}, cursorBefore)
			e.deleteRecorded(Position{Line: line, Col: headCol}, Position{Line: line, Col: ByteOffsetToVisualCol(raw, sp.indent+len(head), e.TabWidth())}, cursorBefore)
			shifts[line] = shift{at: headCol, head: -len(head), tailAt: tailStart, tail: -len(tail)}
			continue
		}
		e.insertRecorded(Position{Line: line, Col: tailCol}, " "+end, cursorBefore)
		e.insertRecorded(Position{Line: line, Col: headCol}, start+" ", cursorBefore)
		shifts[line] = shift{at: headCol, head: len(start) + 1, tailAt: tailCol, tail: len(end) + 1}
	}

	adjust := func(p Position) Position {
		s, ok := shifts[p.Line]
		if !ok || p.Col < s.at {
			return p
		}
		col := p.Col
		if col > s.tailAt || col == s.tailAt && s.tail < 0 {
			col = max(s.tailAt, col+s.tail) // after the closing delimiter
		}
		return Position{Line: p.Line, Col: max(s.at, col+s.head)}
	}
	e.cursor = adjust(e.cursor)
	if e.HasSelection() {
		e.selAnchor = adjust(e.selAnchor)
		e.selEnd = adjust(e.selEnd)
	}
	e.finishEdit()
}
