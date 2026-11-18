package types

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// EditorPredictionMaxBytes bounds suggestion text before transport, caching
// and projection.
const EditorPredictionMaxBytes = 4 << 10

// EditorPredictionMaxLines bounds the lines of one suggestion. The editor
// shows at most EditorPredictionShownLines of them and a "+N lines" marker.
const (
	EditorPredictionMaxLines   = 40
	EditorPredictionShownLines = 8
)

// ValidEditorPredictionText is the shared literal contract for admission and
// editor presentation. Text may span up to EditorPredictionMaxLines lines with
// LF line breaks and may contain tabs; it never contains other controls,
// carriage returns or Unicode bidi directives, which are refused rather than
// repaired into different source. Empty text is valid only as a deletion.
func ValidEditorPredictionText(text string) bool {
	if len(text) > EditorPredictionMaxBytes || !utf8.ValidString(text) ||
		strings.Count(text, "\n") >= EditorPredictionMaxLines {
		return false
	}
	for _, r := range text {
		if r == '\n' || r == '\t' {
			continue
		}
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' ||
			r == '\u061c' || r == '\u200e' || r == '\u200f' ||
			r >= '\u202a' && r <= '\u202e' || r >= '\u2066' && r <= '\u2069' {
			return false
		}
	}
	return true
}

// ValidEditorPredictionInsertion is ValidEditorPredictionText for one
// nonempty line without tabs: the inline text that completion also shows.
func ValidEditorPredictionInsertion(text string) bool {
	return text != "" && !strings.ContainsAny(text, "\n\t") && ValidEditorPredictionText(text)
}
