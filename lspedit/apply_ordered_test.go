package lspedit

import (
	"testing"

	"crude/types"
)

func TestApplyOrderedPreservesProviderOrderForZeroWidthInserts(t *testing.T) {
	got, err := ApplyOrdered("", []types.LSPTextEdit{
		{Line: 0, Col: 0, EndLine: 0, EndCol: 0, NewText: "one\n"},
		{Line: 0, Col: 0, EndLine: 0, EndCol: 0, NewText: "two\n"},
		{Line: 0, Col: 0, EndLine: 0, EndCol: 0, NewText: "three\n"},
	})
	if err != nil || got != "one\ntwo\nthree\n" {
		t.Fatalf("ordered inserts = %q, %v", got, err)
	}
	if _, err := ApplyOrdered("abc", []types.LSPTextEdit{
		{Line: 0, Col: 0, EndLine: 0, EndCol: 2, NewText: "x"},
		{Line: 0, Col: 1, EndLine: 0, EndCol: 3, NewText: "y"},
	}); err == nil {
		t.Fatal("overlapping replacements were accepted")
	}
	if _, err := ApplyOrdered("abc", []types.LSPTextEdit{
		{Line: 0, Col: 1, EndLine: 0, EndCol: 1, NewText: "x"},
		{Line: 0, Col: 1, EndLine: 0, EndCol: 2, NewText: "y"},
	}); err == nil {
		t.Fatal("equal-start insert/replacement ambiguity was accepted")
	}
}
