package types_test

import (
	"strings"
	"testing"

	"crude/types"
)

func TestEditorPredictionLiteralRemainsExactSingleLine(t *testing.T) {
	for _, text := range []string{
		" result", "\u00a0result", "👩‍💻 cafe\u0301", strings.Repeat("x", 4096),
	} {
		if !types.ValidEditorPredictionInsertion(text) {
			t.Fatalf("valid literal insertion was refused: %q", text[:min(len(text), 40)])
		}
	}
	for _, text := range []string{
		"", "two\nlines", "x\r", "\tindent", "x\x00", "\x1b[31mred",
		"x\u0085", "x\u009f", "x\u061c", "x\u200e", "x\u200f", "x\u2028",
		"x\u2029", "x\u202a", "x\u202e", "x\u2066", "x\u2069",
		strings.Repeat("x", 4097), string([]byte{0xff}),
	} {
		if types.ValidEditorPredictionInsertion(text) {
			t.Fatalf("unsafe literal insertion became usable: %q", text[:min(len(text), 40)])
		}
	}
}
