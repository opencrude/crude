package lspcompletion

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"unicode"
	"unicode/utf8"

	"crude/document"
	"crude/lspedit"
	"crude/snippet"
	"crude/types"
)

const (
	MaxAdditionalEdits = 64
	MaxSourceBytes     = 2 << 20
	maxPosition        = 1<<31 - 1
)

// ErrUnsupported distinguishes valid but unsupported insertion semantics from
// malformed evidence. Neither error permits falling back to a literal insertion.
var ErrUnsupported = errors.New("unsupported completion insertion")

func unsupported(why string) error { return fmt.Errorf("%w: %s", ErrUnsupported, why) }

// Insertion interprets only the initial Original object. In particular, neither
// a caller's display fields nor documentation returned by resolve can change the
// insertion. The returned primary, adjunct vector and snippet plan are detached.
// Source containment, overlapping edits and resulting document size are checked
// by PrimaryEdit and lspedit.Apply against the initial request's source.
func Insertion(item Item) (types.CompletionItem, error) {
	if err := validate(item.Original); err != nil {
		return types.CompletionItem{}, err
	}
	initial, err := readItem(item.Original)
	if err != nil {
		return types.CompletionItem{}, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(item.Original, &fields); err != nil {
		return types.CompletionItem{}, invalid("completion insertion object")
	}
	if _, exists := fields["command"]; exists {
		return types.CompletionItem{}, unsupported("provider command")
	}
	out := types.CompletionItem{
		Label: initial.Label, Detail: initial.Documentation.Detail,
		Documentation: initial.Documentation.Value,
	}
	if raw, exists := fields["kind"]; exists {
		n, err := insertionInteger(raw)
		if err != nil || n < 0 {
			return types.CompletionItem{}, invalid("completion kind requires a nonnegative int32")
		}
		out.Kind = n
	}
	format := 1
	if raw, exists := fields["insertTextFormat"]; exists {
		format, err = insertionInteger(raw)
		if err != nil {
			return types.CompletionItem{}, err
		}
		if format != 1 && format != 2 {
			return types.CompletionItem{}, unsupported("insertTextFormat")
		}
	}
	if raw, exists := fields["insertTextMode"]; exists {
		mode, err := insertionInteger(raw)
		if err != nil {
			return types.CompletionItem{}, err
		}
		if mode != 1 {
			return types.CompletionItem{}, unsupported("insertTextMode")
		}
	}
	if raw, exists := fields["insertText"]; exists {
		if !readString(raw, &out.InsertText) {
			return types.CompletionItem{}, invalid("insertText requires a bounded string")
		}
		out.HasInsertText = true
	}
	if raw, exists := fields["textEdit"]; exists {
		edit, err := insertionPrimary(raw)
		if err != nil {
			return types.CompletionItem{}, err
		}
		out.TextEdit = &edit
		out.InsertText = edit.NewText
	} else if !out.HasInsertText {
		out.InsertText = out.Label
	}
	if raw, exists := fields["additionalTextEdits"]; exists {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 || raw[0] != '[' {
			return types.CompletionItem{}, invalid("additionalTextEdits requires an array")
		}
		var edits []json.RawMessage
		if json.Unmarshal(raw, &edits) != nil || len(edits) > MaxAdditionalEdits {
			return types.CompletionItem{}, invalid("additionalTextEdits count or container")
		}
		out.AdditionalTextEdits = make([]types.LSPTextEdit, len(edits))
		for i, rawEdit := range edits {
			var fields map[string]json.RawMessage
			if json.Unmarshal(rawEdit, &fields) != nil || len(fields) != 2 {
				return types.CompletionItem{}, invalid("additional edit requires range and newText")
			}
			edit, err := insertionRange(fields["range"])
			if err != nil {
				return types.CompletionItem{}, err
			}
			if !readString(fields["newText"], &edit.NewText) {
				return types.CompletionItem{}, invalid("additional newText requires a bounded string")
			}
			out.AdditionalTextEdits[i] = edit
		}
	}
	if format == 2 {
		expanded, plan, err := snippet.Parse(out.InsertText)
		if err != nil {
			if errors.Is(err, snippet.ErrLimit) {
				return types.CompletionItem{}, invalid("numeric snippet exceeds its bound")
			}
			return types.CompletionItem{}, unsupported("numeric snippet grammar")
		}
		out.InsertText, out.Snippet = expanded, plan
		if out.TextEdit != nil {
			out.TextEdit.NewText = expanded
		}
	}
	return out, nil
}

func insertionInteger(raw []byte) (int, error) {
	raw = bytes.TrimSpace(raw)
	var n int32
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &n) != nil {
		return 0, invalid("integer requires signed-32 JSON integer")
	}
	return int(n), nil
}

func insertionPosition(raw []byte) (line, col int, err error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 2 {
		return 0, 0, invalid("position requires line and character")
	}
	line, err = insertionInteger(fields["line"])
	if err != nil {
		return 0, 0, err
	}
	col, err = insertionInteger(fields["character"])
	if err != nil || line < 0 || col < 0 {
		return 0, 0, invalid("position requires nonnegative int32 coordinates")
	}
	return line, col, nil
}

func insertionRange(raw []byte) (types.LSPTextEdit, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 2 {
		return types.LSPTextEdit{}, invalid("range requires start and end")
	}
	line, col, err := insertionPosition(fields["start"])
	if err != nil {
		return types.LSPTextEdit{}, err
	}
	endLine, endCol, err := insertionPosition(fields["end"])
	if err != nil {
		return types.LSPTextEdit{}, err
	}
	if endLine < line || endLine == line && endCol < col {
		return types.LSPTextEdit{}, invalid("reversed range")
	}
	return types.LSPTextEdit{Line: line, Col: col, EndLine: endLine, EndCol: endCol}, nil
}

func insertionPrimary(raw []byte) (types.LSPTextEdit, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return types.LSPTextEdit{}, invalid("textEdit requires an object")
	}
	var edit types.LSPTextEdit
	var err error
	if rawRange, exists := fields["range"]; exists {
		if len(fields) != 2 {
			return types.LSPTextEdit{}, invalid("mixed or partial textEdit union")
		}
		edit, err = insertionRange(rawRange)
	} else {
		if len(fields) != 3 {
			return types.LSPTextEdit{}, invalid("insert/replace requires both ranges and newText")
		}
		var insert types.LSPTextEdit
		insert, err = insertionRange(fields["insert"])
		if err == nil {
			edit, err = insertionRange(fields["replace"])
		}
		if err == nil && (insert.Line != edit.Line || insert.Col != edit.Col ||
			insert.Line != insert.EndLine || insert.EndLine != edit.EndLine || insert.EndCol > edit.EndCol) {
			err = invalid("insert range must be a same-start prefix of replacement")
		}
	}
	if err != nil {
		return types.LSPTextEdit{}, err
	}
	if edit.Line != edit.EndLine {
		return types.LSPTextEdit{}, invalid("completion primary range must be single-line")
	}
	if !readString(fields["newText"], &edit.NewText) {
		return types.LSPTextEdit{}, invalid("primary newText requires a bounded string")
	}
	return edit, nil
}

// PrimaryEdit returns the sole primary source edit for an initial completion.
// Explicit ranges must contain the strict UTF-16 cursor. Without one, only the
// backward Unicode letter/digit/underscore prefix is replaced. It owns no editor
// state, applies no edits, and never modifies the caller's item or snippet plan.
func PrimaryEdit(text string, line, col int, item types.CompletionItem) (types.LSPTextEdit, error) {
	if len(text) > MaxSourceBytes || len(text) > document.MaxBytes || !utf8.ValidString(text) ||
		!insertionCoordinate(line) || !insertionCoordinate(col) {
		return types.LSPTextEdit{}, invalid("completion source or cursor bound/encoding")
	}
	cursor, err := lspedit.Offset(text, line, col)
	if err != nil {
		return types.LSPTextEdit{}, invalid("completion cursor outside source boundary")
	}
	var edit types.LSPTextEdit
	if item.TextEdit != nil {
		edit = *item.TextEdit
		if !insertionCoordinate(edit.Line) || !insertionCoordinate(edit.Col) ||
			!insertionCoordinate(edit.EndLine) || !insertionCoordinate(edit.EndCol) ||
			edit.Line != edit.EndLine || edit.Line != line {
			return types.LSPTextEdit{}, invalid("primary requires single-line int32 range at cursor")
		}
		start, startErr := lspedit.Offset(text, edit.Line, edit.Col)
		end, endErr := lspedit.Offset(text, edit.EndLine, edit.EndCol)
		if startErr != nil || endErr != nil || start > cursor || cursor > end {
			return types.LSPTextEdit{}, invalid("primary range must contain cursor at source boundaries")
		}
	} else {
		start := cursor
		for start > 0 {
			r, size := utf8.DecodeLastRuneInString(text[:start])
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
				break
			}
			start -= size
		}
		startLine, startCol, err := lspedit.Position(text, start)
		if err != nil {
			return types.LSPTextEdit{}, invalid("prefix outside source boundary")
		}
		edit = types.LSPTextEdit{Line: startLine, Col: startCol, EndLine: line, EndCol: col, NewText: item.InsertText}
		if edit.NewText == "" && !item.HasInsertText && item.Snippet == nil {
			edit.NewText = item.Label
		}
		if edit.NewText == "" && !item.HasInsertText && item.Snippet == nil {
			return types.LSPTextEdit{}, invalid("missing completion replacement")
		}
	}
	if len(edit.NewText) > MaxStringBytes || !utf8.ValidString(edit.NewText) {
		return types.LSPTextEdit{}, invalid("primary replacement byte bound or UTF-8")
	}
	if item.Snippet != nil {
		if err := snippet.Validate(edit.NewText, item.Snippet); err != nil {
			return types.LSPTextEdit{}, invalid("invalid primary snippet plan")
		}
	}
	return edit, nil
}

func insertionCoordinate(n int) bool { return n >= 0 && n <= maxPosition }
