// Package lspedit provides bounded, passive UTF-16 source transformations and
// lossless text-only WorkspaceEdit parsing. It owns no files or document state.
package lspedit

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"crude/document"
	"crude/types"
)

// Offset is strict: unlike display helpers it never clamps an invalid
// endpoint or accepts the middle of a surrogate pair. Newline bytes are not
// part of a line's UTF-16 column range.
func Offset(text string, line, col int) (int, error) {
	if len(text) > document.MaxBytes || line < 0 || col < 0 || !utf8.ValidString(text) {
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

// Apply validates all edits against the same immutable source before producing
// output. Equal-start edits are deliberately unsupported: their order is ambiguous.
// Neither the caller's edit vector nor its source is modified.
func Apply(text string, edits []types.LSPTextEdit) (string, error) {
	return apply(text, edits, false)
}

// ApplyOrdered preserves provider order for valid equal-start zero-width
// inserts. Overlapping ranges and equal-start replacements remain invalid.
func ApplyOrdered(text string, edits []types.LSPTextEdit) (string, error) {
	return apply(text, edits, true)
}

func apply(text string, edits []types.LSPTextEdit, ordered bool) (string, error) {
	if len(text) > document.MaxBytes || len(edits) > document.MaxEdits || !utf8.ValidString(text) {
		return "", fmt.Errorf("LSP edit: source encoding or document/edit bound exceeded")
	}
	// Check replacement sizes before cloning or sorting caller-owned inputs.
	for _, edit := range edits {
		if len(edit.NewText) > document.MaxBytes || !utf8.ValidString(edit.NewText) {
			return "", fmt.Errorf("LSP edit: invalid replacement or byte bound exceeded")
		}
	}
	type position struct {
		start, end int
		text       string
	}
	positions := make([]position, 0, len(edits))
	size := int64(len(text))
	for _, edit := range edits {
		start, err := Offset(text, edit.Line, edit.Col)
		if err != nil {
			return "", err
		}
		end, err := Offset(text, edit.EndLine, edit.EndCol)
		if err != nil {
			return "", err
		}
		if end < start {
			return "", fmt.Errorf("LSP edit: reversed range")
		}
		size += int64(len(edit.NewText)) - int64(end-start)
		positions = append(positions, position{start, end, edit.NewText})
	}
	sort.SliceStable(positions, func(i, j int) bool { return positions[i].start < positions[j].start })
	for i := 1; i < len(positions); i++ {
		previous, current := positions[i-1], positions[i]
		if current.start < previous.end {
			return "", fmt.Errorf("LSP edit: overlapping or ambiguous edits")
		}
		if current.start == previous.start && (!ordered || previous.end > previous.start || current.end > current.start) {
			return "", fmt.Errorf("LSP edit: overlapping or ambiguous edits")
		}
	}
	if size < 0 || size > document.MaxBytes {
		return "", fmt.Errorf("LSP edit: result exceeds document byte bound")
	}
	var out strings.Builder
	out.Grow(int(size))
	last := 0
	for _, edit := range positions {
		out.WriteString(text[last:edit.start])
		out.WriteString(edit.text)
		last = edit.end
	}
	out.WriteString(text[last:])
	return out.String(), nil
}
