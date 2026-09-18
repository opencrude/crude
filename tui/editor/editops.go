package editor

import (
	"path/filepath"
	"strings"
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
	startOff := e.buf.positionToOffset(pos)
	_, _ = e.buf.Insert(pos, []byte(text))
	endLine, endCol := ByteOffsetToLineCol(string(e.buf.Bytes()), startOff+len(text), tabWidth)
	end := Position{Line: endLine, Col: endCol}
	e.recordChange(Change{
		Type:         ActInsert,
		Text:         []byte(text),
		StartPos:     pos,
		EndPos:       end,
		CursorBefore: cursorBefore,
	})
	return end
}

// deleteRecorded deletes [start, end) and records the change. Returns the
// deleted text ("" when the range was empty and nothing was recorded).
func (e *Editor) deleteRecorded(start, end Position, cursorBefore Position) string {
	text := e.buf.GetText(start, end)
	if text == "" {
		return ""
	}
	_, _ = e.buf.Delete(start, end)
	e.recordChange(Change{
		Type:         ActDelete,
		Text:         []byte(text),
		StartPos:     start,
		EndPos:       end,
		CursorBefore: cursorBefore,
	})
	return text
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
}

func (e *Editor) runeAtCursor() rune {
	runes := e.lineRunes(e.cursor.Line)
	ri := colToRuneIdx(runes, e.cursor.Col)
	if ri < 0 || ri >= len(runes) {
		return 0
	}
	return runes[ri]
}

func (e *Editor) runeBeforeCursor() rune {
	if e.cursor.Col <= 0 {
		return 0
	}
	runes := e.lineRunes(e.cursor.Line)
	ri := colToRuneIdx(runes, e.cursor.Col)
	if ri <= 0 || ri > len(runes) {
		return 0
	}
	return runes[ri-1]
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

// indentUnit returns one extra indent level matching the style of the
// existing indent: tabs beget a tab, otherwise spaces.
func indentUnit(indent string) string {
	if strings.Contains(indent, "\t") {
		return "\t"
	}
	return strings.Repeat(" ", tabWidth)
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
		e.cursor.Col -= runeVisualWidth(closer)
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
	ow := runeVisualWidth(opener)

	e.beginUndoGroup()
	e.insertRecorded(end, string(closer), cursorBefore)
	e.insertRecorded(start, string(opener), cursorBefore)
	e.endUndoGroup()

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
	if e.HasSelection() {
		e.DeleteSelection()
		return
	}
	prev := e.runeBeforeCursor()
	if closer, ok := autoClosePairs[prev]; ok && e.runeAtCursor() == closer {
		cursorBefore := e.cursor
		start := Position{Line: e.cursor.Line, Col: e.cursor.Col - runeVisualWidth(prev)}
		end := Position{Line: e.cursor.Line, Col: e.cursor.Col + runeVisualWidth(closer)}
		e.deleteRecorded(start, end, cursorBefore)
		e.cursor = start
		e.finishEdit()
		return
	}
	e.deleteBackward()
}

// ── line-range helpers ───────────────────────────────────────────────

func (e *Editor) selectionSpansLines() bool {
	if !e.HasSelection() {
		return false
	}
	sLine, _, eLine, _ := e.SelectionRange()
	return eLine > sLine
}

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
	indent := strings.Repeat(" ", tabWidth)
	cursorBefore := e.cursor
	indented := make(map[int]bool, last-first+1)

	e.beginUndoGroup()
	for line := first; line <= last; line++ {
		if e.LineText(line) == "" {
			continue
		}
		e.insertRecorded(Position{Line: line}, indent, cursorBefore)
		indented[line] = true
	}
	e.endUndoGroup()
	if len(indented) == 0 {
		return
	}

	adjust := func(p Position) Position {
		if indented[p.Line] && p.Col > 0 {
			p.Col += tabWidth
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

// outdentLines removes up to one indent level (a leading tab or up to
// tabWidth spaces) from every selected line, preserving the selection.
// One undo step.
func (e *Editor) outdentLines() {
	first, last := e.selectedLineRange()
	cursorBefore := e.cursor
	removed := make(map[int]int, last-first+1)

	e.beginUndoGroup()
	for line := first; line <= last; line++ {
		raw := e.LineText(line)
		if raw == "" {
			continue
		}
		var nBytes, cols int
		if raw[0] == '\t' {
			nBytes = 1
			cols = TabStopAdvance(0, tabWidth)
		} else {
			for nBytes < len(raw) && nBytes < tabWidth && raw[nBytes] == ' ' {
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
	e.endUndoGroup()
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
	replacement := strings.Join(newLines, "\n")
	cursorBefore := e.cursor

	e.beginUndoGroup()
	e.deleteRecorded(start, end, cursorBefore)
	e.insertRecorded(start, replacement, cursorBefore)
	e.endUndoGroup()

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
	e.insertRecorded(Position{Line: line, Col: e.lineVisualWidth(line)}, "\n"+text, cursorBefore)
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

var commentPrefixByExt = map[string]string{
	".go": "//", ".c": "//", ".h": "//", ".cpp": "//", ".hpp": "//", ".cc": "//",
	".hh": "//", ".cxx": "//", ".hxx": "//", ".c++": "//", ".h++": "//",
	".cs": "//", ".java": "//", ".js": "//", ".jsx": "//", ".ts": "//",
	".tsx": "//", ".mjs": "//", ".cjs": "//", ".rs": "//", ".zig": "//",
	".odin": "//", ".swift": "//", ".kt": "//", ".kts": "//", ".scala": "//",
	".dart": "//", ".d": "//", ".v": "//", ".m": "//", ".mm": "//",
	".php": "//", ".proto": "//", ".jsonc": "//", ".json5": "//",

	".py": "#", ".sh": "#", ".bash": "#", ".zsh": "#", ".fish": "#", ".ksh": "#",
	".rb": "#", ".pl": "#", ".pm": "#", ".r": "#", ".jl": "#", ".ex": "#",
	".exs": "#", ".nim": "#", ".cr": "#", ".yaml": "#", ".yml": "#",
	".toml": "#", ".mk": "#", ".cmake": "#", ".tcl": "#", ".ps1": "#",
	".psm1": "#", ".tf": "#", ".hcl": "#", ".conf": "#", ".properties": "#",
	".gitignore": "#", ".env": "#", ".dockerfile": "#", ".s": "#",

	".sql": "--", ".lua": "--", ".hs": "--", ".elm": "--",

	".lisp": ";", ".el": ";", ".clj": ";", ".cljs": ";", ".edn": ";",
	".scm": ";", ".ini": ";", ".asm": ";",

	".tex": "%", ".erl": "%", ".hrl": "%",

	".vim": "\"",
}

var commentPrefixByBase = map[string]string{
	"makefile":    "#",
	"gnumakefile": "#",
	"dockerfile":  "#",
}

var commentPrefixByLexer = map[string]string{
	"Go": "//", "C": "//", "C++": "//", "C#": "//", "Java": "//",
	"JavaScript": "//", "TypeScript": "//", "Rust": "//", "Zig": "//",
	"Swift": "//", "Kotlin": "//", "Scala": "//", "Dart": "//", "PHP": "//",
	"Python": "#", "Bash": "#", "Ruby": "#", "Perl": "#", "YAML": "#",
	"TOML": "#", "Makefile": "#", "Docker": "#", "CMake": "#", "Fish": "#",
	"R": "#", "Julia": "#", "Elixir": "#", "Nim": "#", "Crystal": "#",
	"SQL": "--", "Lua": "--", "Haskell": "--", "Elm": "--",
	"Common Lisp": ";", "Scheme": ";", "Clojure": ";", "EmacsLisp": ";",
	"TeX": "%", "Erlang": "%",
	"VimL": "\"",
}

// commentPrefix returns the line-comment prefix for the current file, or
// "" when the language has no known line comment.
func (e *Editor) commentPrefix() string {
	path := e.filePath
	if path == "" {
		path = e.title
	}
	if ext := strings.ToLower(filepath.Ext(path)); ext != "" {
		if p, ok := commentPrefixByExt[ext]; ok {
			return p
		}
	}
	if base := strings.ToLower(filepath.Base(path)); base != "" {
		if p, ok := commentPrefixByBase[base]; ok {
			return p
		}
	}
	if e.chromaLexer != nil {
		if p, ok := commentPrefixByLexer[e.chromaLexer.Config().Name]; ok {
			return p
		}
	}
	return ""
}

// toggleLineComment comments or uncomments every selected line using the
// file's line-comment prefix. When all non-blank selected lines are
// already commented they are uncommented; otherwise every non-blank line
// gains the prefix after its leading whitespace. One undo step.
func (e *Editor) toggleLineComment() {
	prefix := e.commentPrefix()
	if prefix == "" {
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
	for line := first; line <= last; line++ {
		raw := e.LineText(line)
		wsLen := len(raw) - len(strings.TrimLeft(raw, " \t"))
		trimmed := raw[wsLen:]
		if trimmed == "" {
			continue
		}
		insCol := ByteOffsetToVisualCol(raw, wsLen, tabWidth)
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
	e.endUndoGroup()
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
