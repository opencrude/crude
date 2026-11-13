package editor

import (
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
	if len(e.folds.collapsed) > 0 {
		for _, edit := range edits {
			e.unfoldSource(e.buf.positionAtOffset(edit.Start).Line, true)
			e.unfoldSource(e.buf.positionAtOffset(edit.End).Line, true)
		}
	}
	deleted := make([]string, len(edits))
	for i, edit := range edits {
		deleted[i] = string(e.buf.cachedBytes[edit.Start:edit.End])
	}
	previousOwner, previousActor := e.buf.editOwner, e.buf.linkActor
	e.buf.editOwner, e.buf.linkActor = e, e
	defer func() { e.buf.editOwner, e.buf.linkActor = previousOwner, previousActor }()
	if err := e.applySourceOffsets(edits); err != nil {
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

// ── auto-closing pairs ───────────────────────────────────────────────

// typeRune handles a printable typed character: wraps the selection when
// an opener is typed over one, types over an already-present closer,
// auto-inserts closing pairs, and otherwise inserts the rune as-is.
func (e *Editor) typeRune(r rune) {
	e.buf.ensureCache()
	p := e.typedCaretEdit(e.primaryRange(), string(r))
	p.primary = true
	_ = e.applyCaretEdits([]caretEdit{p})
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

// deleteBackwardSmart deletes the selection if one is active, removes
// both characters of an empty auto-close pair when the cursor sits
// between them, and otherwise falls back to a plain backward delete.
func (e *Editor) deleteBackwardSmart() {
	if e.HasSelection() {
		e.DeleteSelection()
		return
	}
	e.applySingleCaretEdit(e.backwardCaretEdit(e.buf.snapPosition(e.cursor), true))
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
	e.rewriteIndentation(first, last, style.size, style)
}

// outdentLines removes one indentation level, including a partial display tab.
func (e *Editor) outdentLines() {
	first, last := e.selectedLineRange()
	style := e.indentStyle()
	e.rewriteIndentation(first, last, -style.size, style)
}

func (e *Editor) moveLinesUp()              { _ = e.applyNativeLineAction("edit.moveLineUp") }
func (e *Editor) moveLinesDown()            { _ = e.applyNativeLineAction("edit.moveLineDown") }
func (e *Editor) duplicateLineOrSelection() { _ = e.applyNativeCaretAction("edit.duplicateLine") }
func (e *Editor) deleteLines()              { _ = e.applyNativeLineAction("edit.deleteLine") }

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

// toggleLineComment uses one source-qualified vector for every selected line.
func (e *Editor) toggleLineComment() { _ = e.applyNativeLineAction("edit.toggleComment") }
