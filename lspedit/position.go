package lspedit

import (
	"fmt"
	"unicode/utf16"
	"unicode/utf8"

	"crude/document"
)

// Position converts an exact UTF-8 source byte offset to a zero-based LSP
// line and UTF-16 column. It is the strict inverse of Offset, not a display
// projection: rune boundaries inside graphemes are valid, but split UTF-8
// runes and the LF byte of a CRLF terminator are not. The CR byte of CRLF
// (or an ordinary LF byte) denotes the preceding line's end; after LF starts
// the next line. Bare CR is ordinary content.
func Position(text string, offset int) (line, col int, err error) {
	if len(text) > document.MaxBytes || offset < 0 || offset > len(text) || !utf8.ValidString(text) {
		return 0, 0, fmt.Errorf("LSP edit: invalid offset or document encoding/byte bound")
	}
	if offset < len(text) {
		if !utf8.RuneStart(text[offset]) {
			return 0, 0, fmt.Errorf("LSP edit: offset splits a UTF-8 rune")
		}
		if offset > 0 && text[offset] == '\n' && text[offset-1] == '\r' {
			return 0, 0, fmt.Errorf("LSP edit: offset splits a CRLF terminator")
		}
	}
	for _, r := range text[:offset] {
		if r == '\n' {
			line++
			col = 0
		} else {
			col += utf16.RuneLen(r)
		}
	}
	return line, col, nil
}
