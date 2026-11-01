package types

import (
	"unicode"
	"unicode/utf8"
)

// EditorPredictionMaxBytes bounds a literal candidate before transport, caching
// and projection. Geometry imposes its own combined source/inlay run limits.
const EditorPredictionMaxBytes = 4 << 10

// ValidEditorPredictionInsertion is the shared literal contract for admission
// and editor presentation. This first slice admits one nonempty UTF-8 line,
// never repairing controls, Unicode bidi directives or oversized output into
// different source. Grapheme placement and zero-width geometry belong to the
// editor's shared source-to-cell projection, not this presentation-neutral test.
func ValidEditorPredictionInsertion(text string) bool {
	if len(text) == 0 || len(text) > EditorPredictionMaxBytes || !utf8.ValidString(text) {
		return false
	}
	for _, r := range text {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' ||
			r == '\u061c' || r == '\u200e' || r == '\u200f' ||
			r >= '\u202a' && r <= '\u202e' || r >= '\u2066' && r <= '\u2069' {
			return false
		}
	}
	return true
}
