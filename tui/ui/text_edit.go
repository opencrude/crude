package ui

import (
	"unicode"
	"unicode/utf8"
)

const ActionTextPaste = "input.paste"

type ClipboardPasteTarget interface {
	PasteText(string)
	PasteActive() bool
}

func intAbs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// TextEdit is the shared grapheme/rune-safe editing model for all non-document
// text inputs (TextField, SearchField, FilterField, PromptField, form fields).
//
// Cursor and selection indices are rune offsets into Value runes — never bytes.
type TextEdit struct {
	runes     []rune
	cursor    int // rune index [0, len]
	selAnchor int // -1 = no selection; otherwise selection is [min,max) with cursor
	selActive bool
	undo      []textEditSnapshot
	redo      []textEditSnapshot
}

type textEditSnapshot struct {
	runes     []rune
	cursor    int
	selAnchor int
	selActive bool
}

// NewTextEdit creates an empty editor.
func NewTextEdit() *TextEdit {
	return &TextEdit{selAnchor: -1}
}

// SetValue replaces content and places the cursor at the end.
func (e *TextEdit) SetValue(s string) {
	e.runes = []rune(s)
	e.cursor = len(e.runes)
	e.ClearSelection()
	e.undo = nil
	e.redo = nil
}

// Value returns the current text.
func (e *TextEdit) Value() string {
	return string(e.runes)
}

// Len returns rune count.
func (e *TextEdit) Len() int { return len(e.runes) }

// Cursor returns the rune cursor index.
func (e *TextEdit) Cursor() int { return e.cursor }

// SetCursor sets the cursor, clearing selection unless extend is true.
func (e *TextEdit) SetCursor(i int, extend bool) {
	if i < 0 {
		i = 0
	}
	if i > len(e.runes) {
		i = len(e.runes)
	}
	if extend {
		if !e.selActive {
			e.selAnchor = e.cursor
			e.selActive = true
		}
	} else {
		e.ClearSelection()
	}
	e.cursor = i
}

// ClearSelection drops the selection.
func (e *TextEdit) ClearSelection() {
	e.selActive = false
	e.selAnchor = -1
}

// HasSelection reports an active non-empty selection.
func (e *TextEdit) HasSelection() bool {
	if !e.selActive {
		return false
	}
	a, b := e.SelectionRange()
	return a != b
}

// SelectionRange returns [start, end) rune indices.
func (e *TextEdit) SelectionRange() (int, int) {
	if !e.selActive {
		return e.cursor, e.cursor
	}
	a, b := e.selAnchor, e.cursor
	if a > b {
		a, b = b, a
	}
	return a, b
}

// SelectedText returns the selected substring.
func (e *TextEdit) SelectedText() string {
	a, b := e.SelectionRange()
	if a == b {
		return ""
	}
	return string(e.runes[a:b])
}

func (e *TextEdit) DeleteSelection() bool {
	if !e.HasSelection() {
		return false
	}
	e.recordEdit()
	e.deleteSelection()
	return true
}

// Insert inserts s at the cursor, replacing any selection.
func (e *TextEdit) Insert(s string) {
	r := []rune(s)
	// filter non-printable except newline/tab if present — single-line callers
	// should strip newlines before insert.
	if len(r) == 0 {
		return
	}
	e.recordEdit()
	if e.HasSelection() {
		e.deleteSelection()
	}
	out := make([]rune, 0, len(e.runes)+len(r))
	out = append(out, e.runes[:e.cursor]...)
	out = append(out, r...)
	out = append(out, e.runes[e.cursor:]...)
	e.runes = out
	e.cursor += len(r)
	e.ClearSelection()
}

// isEditableRune reports whether a rune is accepted into the editing engine.
// Printable runes are accepted; the grapheme-joining format characters ZWJ
// and ZWNJ (category Cf, not printable) are also kept so emoji sequences and
// Indic scripts pasted or typed through the engine survive intact.
func isEditableRune(r rune) bool {
	if unicode.IsPrint(r) {
		return true
	}
	return r == '\u200C' || r == '\u200D'
}

// InsertRunes inserts printable runes (and space), preserving grapheme-joining
// format characters.
func (e *TextEdit) InsertRunes(s string) {
	var clean []rune
	for _, r := range s {
		if r == '\n' || r == '\t' {
			continue
		}
		if isEditableRune(r) {
			clean = append(clean, r)
		}
	}
	if len(clean) == 0 {
		return
	}
	e.Insert(string(clean))
}

// Backspace deletes before cursor or the selection.
func (e *TextEdit) Backspace() {
	if e.HasSelection() {
		e.recordEdit()
		e.deleteSelection()
		return
	}
	if e.cursor == 0 {
		return
	}
	e.recordEdit()
	e.runes = append(e.runes[:e.cursor-1], e.runes[e.cursor:]...)
	e.cursor--
}

// Delete deletes after cursor or the selection.
func (e *TextEdit) Delete() {
	if e.HasSelection() {
		e.recordEdit()
		e.deleteSelection()
		return
	}
	if e.cursor >= len(e.runes) {
		return
	}
	e.recordEdit()
	e.runes = append(e.runes[:e.cursor], e.runes[e.cursor+1:]...)
}

func (e *TextEdit) deleteSelection() {
	a, b := e.SelectionRange()
	e.runes = append(e.runes[:a], e.runes[b:]...)
	e.cursor = a
	e.ClearSelection()
}

// MoveLeft moves one rune left.
func (e *TextEdit) MoveLeft(extend bool) {
	e.SetCursor(e.cursor-1, extend)
}

// MoveRight moves one rune right.
func (e *TextEdit) MoveRight(extend bool) {
	e.SetCursor(e.cursor+1, extend)
}

// MoveHome moves to start.
func (e *TextEdit) MoveHome(extend bool) {
	e.SetCursor(0, extend)
}

// MoveEnd moves to end.
func (e *TextEdit) MoveEnd(extend bool) {
	e.SetCursor(len(e.runes), extend)
}

// MoveWordLeft moves to previous word boundary.
func (e *TextEdit) MoveWordLeft(extend bool) {
	i := e.cursor
	if i == 0 {
		e.SetCursor(0, extend)
		return
	}
	i--
	// skip spaces
	for i > 0 && unicode.IsSpace(e.runes[i]) {
		i--
	}
	// skip word chars
	for i > 0 && !unicode.IsSpace(e.runes[i-1]) {
		i--
	}
	e.SetCursor(i, extend)
}

// MoveWordRight moves to next word boundary.
func (e *TextEdit) MoveWordRight(extend bool) {
	i := e.cursor
	n := len(e.runes)
	for i < n && !unicode.IsSpace(e.runes[i]) {
		i++
	}
	for i < n && unicode.IsSpace(e.runes[i]) {
		i++
	}
	e.SetCursor(i, extend)
}

// SelectAll selects entire content.
func (e *TextEdit) SelectAll() {
	e.selAnchor = 0
	e.selActive = true
	e.cursor = len(e.runes)
}

// DeleteToLineStart deletes from line start to cursor (Ctrl+U).
func (e *TextEdit) DeleteToLineStart() {
	if e.HasSelection() {
		e.recordEdit()
		e.deleteSelection()
		return
	}
	if e.cursor == 0 {
		return
	}
	e.recordEdit()
	e.runes = e.runes[e.cursor:]
	e.cursor = 0
}

// DeleteToLineEnd deletes from cursor to end (Ctrl+K).
func (e *TextEdit) DeleteToLineEnd() {
	if e.HasSelection() {
		e.recordEdit()
		e.deleteSelection()
		return
	}
	if e.cursor >= len(e.runes) {
		return
	}
	e.recordEdit()
	e.runes = e.runes[:e.cursor]
}

// DeleteWordBack deletes previous word (Ctrl+W).
func (e *TextEdit) DeleteWordBack() {
	if e.HasSelection() {
		e.recordEdit()
		e.deleteSelection()
		return
	}
	if e.cursor == 0 {
		return
	}
	e.recordEdit()
	end := e.cursor
	e.MoveWordLeft(false)
	start := e.cursor
	e.runes = append(e.runes[:start], e.runes[end:]...)
	e.cursor = start
}

// DeleteWordForward deletes through the next word boundary.
func (e *TextEdit) DeleteWordForward() {
	if e.HasSelection() {
		e.recordEdit()
		e.deleteSelection()
		return
	}
	if e.cursor >= len(e.runes) {
		return
	}
	e.recordEdit()
	start := e.cursor
	i := start
	for i < len(e.runes) && unicode.IsSpace(e.runes[i]) {
		i++
	}
	if i < len(e.runes) {
		class := runeClass(e.runes[i])
		for i < len(e.runes) && runeClass(e.runes[i]) == class {
			i++
		}
	}
	e.runes = append(e.runes[:start], e.runes[i:]...)
}

// SelectWordAt selects the word or punctuation run containing i.
func (e *TextEdit) SelectWordAt(i int) {
	if len(e.runes) == 0 {
		e.SetCursor(0, false)
		return
	}
	i = min(max(0, i), len(e.runes)-1)
	class := runeClass(e.runes[i])
	start, end := i, i+1
	for start > 0 && runeClass(e.runes[start-1]) == class {
		start--
	}
	for end < len(e.runes) && runeClass(e.runes[end]) == class {
		end++
	}
	e.selAnchor, e.cursor, e.selActive = start, end, true
}

func runeClass(r rune) int {
	if unicode.IsSpace(r) {
		return 0
	}
	if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
		return 1
	}
	return 2
}

func (e *TextEdit) snapshot() textEditSnapshot {
	return textEditSnapshot{runes: append([]rune(nil), e.runes...), cursor: e.cursor, selAnchor: e.selAnchor, selActive: e.selActive}
}

func (e *TextEdit) restore(s textEditSnapshot) {
	e.runes = append([]rune(nil), s.runes...)
	e.cursor, e.selAnchor, e.selActive = s.cursor, s.selAnchor, s.selActive
}

func (e *TextEdit) recordEdit() {
	e.undo = append(e.undo, e.snapshot())
	if len(e.undo) > 200 {
		e.undo = e.undo[len(e.undo)-200:]
	}
	e.redo = nil
}

func (e *TextEdit) Undo() bool {
	if len(e.undo) == 0 {
		return false
	}
	e.redo = append(e.redo, e.snapshot())
	last := len(e.undo) - 1
	e.restore(e.undo[last])
	e.undo = e.undo[:last]
	return true
}

func (e *TextEdit) Redo() bool {
	if len(e.redo) == 0 {
		return false
	}
	e.undo = append(e.undo, e.snapshot())
	last := len(e.redo) - 1
	e.restore(e.redo[last])
	e.redo = e.redo[:last]
	return true
}

// RuneAt returns the rune at index or 0.
func (e *TextEdit) RuneAt(i int) rune {
	if i < 0 || i >= len(e.runes) {
		return 0
	}
	return e.runes[i]
}

// CopyStateTo copies content, cursor, and selection into dst.
func (e *TextEdit) CopyStateTo(dst *TextEdit) {
	if e == nil || dst == nil {
		return
	}
	dst.runes = append([]rune(nil), e.runes...)
	dst.cursor = e.cursor
	dst.selAnchor = e.selAnchor
	dst.selActive = e.selActive
}

// EnsureUTF8 validates that SetValue paths stay valid (helper for tests).
func EnsureUTF8(s string) bool {
	return utf8.ValidString(s)
}
