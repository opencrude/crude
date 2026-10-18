package editor

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	model "crude/types"
)

// LSPCursorPosition returns the visual editor cursor as a UTF-16 wire position.
func (e *Editor) LSPCursorPosition() (line, col int) {
	line = e.cursor.Line
	raw := e.LineText(line)
	off := VisualColToByteOffset(raw, e.cursor.Col, tabWidth)
	for _, r := range raw[:off] {
		col += utf16.RuneLen(r)
	}
	return line, col
}

// SetLSPCursorPosition rejects invalid wire positions without moving the cursor.
// Valid positions within a grapheme are displayed at that grapheme's start.
func (e *Editor) SetLSPCursorPosition(line, col int) error {
	if e.buf == nil {
		return fmt.Errorf("LSP cursor: no document")
	}
	text := e.Value()
	off, err := lspEditOffset(text, line, col)
	if err != nil {
		return err
	}
	pos := e.buf.positionAtOffset(off)
	e.SetCursorPosition(pos.Line, pos.Col)
	return nil
}

type completionEdit struct {
	start, end int
	text       string
	primary    bool
}

// ApplyCompletion validates all UTF-16 ranges against one immutable document,
// then commits one undo group and one notification. It never writes to disk.
// InsertText and NewText must already have been expanded from snippets.
func (e *Editor) ApplyCompletion(item model.CompletionItem) error {
	if e.buf == nil || !e.open || e.readOnly {
		return fmt.Errorf("completion: document is not editable")
	}
	if state := e.DocumentState(); state.UndoPending || state.HistoryQueued {
		return ErrDocumentPending // these ranges describe the pre-undo snapshot
	}
	before := e.Value()
	if !utf8.ValidString(before) {
		return fmt.Errorf("completion: document is not UTF-8")
	}
	edits := make([]completionEdit, 0, 1+len(item.AdditionalTextEdits))
	if item.TextEdit != nil {
		edit, err := validateCompletionEdit(before, *item.TextEdit)
		if err != nil {
			return err
		}
		edit.primary = true
		edits = append(edits, edit)
	} else {
		line, col := e.LSPCursorPosition()
		end, err := lspEditOffset(before, line, col)
		if err != nil {
			return err
		}
		start := end
		for start > 0 {
			r, size := utf8.DecodeLastRuneInString(before[:start])
			if !isWordChar(r) {
				break
			}
			start -= size
		}
		text := item.InsertText
		if text == "" {
			text = item.Label
		}
		if text == "" || !utf8.ValidString(text) {
			return fmt.Errorf("completion: missing or invalid UTF-8 replacement")
		}
		edits = append(edits, completionEdit{start: start, end: end, text: text, primary: true})
	}
	for _, wire := range item.AdditionalTextEdits {
		edit, err := validateCompletionEdit(before, wire)
		if err != nil {
			return err
		}
		edits = append(edits, edit)
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	for i := 1; i < len(edits); i++ {
		previous, current := edits[i-1], edits[i]
		if current.start < previous.end || current.start == previous.start {
			return fmt.Errorf("completion: overlapping or ambiguous edits")
		}
	}
	var result strings.Builder
	last, cursorOffset := 0, 0
	for _, edit := range edits {
		result.WriteString(before[last:edit.start])
		result.WriteString(edit.text)
		if edit.primary {
			cursorOffset = result.Len()
		}
		last = edit.end
	}
	result.WriteString(before[last:])
	after := result.String()
	if after == before {
		return nil
	}
	// Whole-document recording also supports valid rune boundaries inside a
	// grapheme, which the PieceTable's visual-position edit API cannot express.
	cursorBefore := e.cursor
	e.beginUndoGroup()
	defer e.endUndoGroup()
	lastLine := e.LineCount() - 1
	e.deleteRecorded(Position{}, Position{Line: lastLine, Col: e.lineVisualWidth(lastLine)}, cursorBefore)
	e.insertRecorded(Position{}, after, cursorBefore)
	e.cursor = e.buf.positionAtOffset(cursorOffset)
	e.ClearSelection()
	e.finishEdit()
	return nil
}

func validateCompletionEdit(text string, wire model.LSPTextEdit) (completionEdit, error) {
	start, err := lspEditOffset(text, wire.Line, wire.Col)
	if err != nil {
		return completionEdit{}, err
	}
	end, err := lspEditOffset(text, wire.EndLine, wire.EndCol)
	if err != nil {
		return completionEdit{}, err
	}
	if end < start || !utf8.ValidString(wire.NewText) {
		return completionEdit{}, fmt.Errorf("completion: reversed range or invalid UTF-8 replacement")
	}
	return completionEdit{start: start, end: end, text: wire.NewText}, nil
}

// lspEditOffset is strict: unlike display helpers it never clamps an invalid
// endpoint or accepts the middle of a surrogate pair. Newline bytes are not
// part of a line's UTF-16 column range.
func lspEditOffset(text string, line, col int) (int, error) {
	if line < 0 || col < 0 || !utf8.ValidString(text) {
		return 0, fmt.Errorf("LSP edit: invalid position or document encoding")
	}
	start := 0
	for n := 0; n < line; n++ {
		next := strings.IndexByte(text[start:], '\n')
		if next < 0 {
			return 0, fmt.Errorf("LSP edit: line outside document")
		}
		start += next + 1
	}
	end := len(text)
	if next := strings.IndexByte(text[start:], '\n'); next >= 0 {
		end = start + next
		if end > start && text[end-1] == '\r' {
			end--
		}
	}
	units := 0
	for off, r := range text[start:end] {
		if units == col {
			return start + off, nil
		}
		units += utf16.RuneLen(r)
		if units > col {
			return 0, fmt.Errorf("LSP edit: column splits a surrogate pair")
		}
	}
	if units == col {
		return end, nil
	}
	return 0, fmt.Errorf("LSP edit: column outside line")
}

// ApplyLSPTextEdits is the pure, strict UTF16 transformation used by reviewed
// multi-file rename preparation. All ranges are checked before returning bytes.
func ApplyLSPTextEdits(text string, edits []model.LSPTextEdit) (string, error) {
	positions := make([]completionEdit, 0, len(edits))
	for _, wire := range edits {
		edit, err := validateCompletionEdit(text, wire)
		if err != nil {
			return "", err
		}
		positions = append(positions, edit)
	}
	sort.Slice(positions, func(i, j int) bool { return positions[i].start < positions[j].start })
	var out strings.Builder
	last := -1
	for _, edit := range positions {
		if last >= 0 && edit.start < last {
			return "", fmt.Errorf("overlapping LSP edits")
		}
		last = edit.end
	}
	for i := 1; i < len(positions); i++ {
		if positions[i-1].start == positions[i].start {
			return "", fmt.Errorf("ambiguous LSP edits")
		}
	}
	last = 0
	for _, edit := range positions {
		out.WriteString(text[last:edit.start])
		out.WriteString(edit.text)
		last = edit.end
	}
	out.WriteString(text[last:])
	return out.String(), nil
}
