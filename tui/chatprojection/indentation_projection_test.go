package chatprojection

import (
	"strings"
	"testing"

	"crude/tui/tools"
)

func TestNormalizeDiffPresentationRowsUsesIndependentBlockBaselines(t *testing.T) {
	rows := []tools.DiffRow{{Content: strings.Repeat(" ", 20) + "outer"}, {Content: strings.Repeat(" ", 24) + "nested"}, tools.EllipsisRow(), {Content: strings.Repeat(" ", 8) + "other"}, {Content: strings.Repeat(" ", 12) + "child"}}
	got := normalizeDiffPresentationRows(rows)
	want := []string{"outer", "  nested", "", "other", "  child"}
	for i := range want {
		if got[i].Content != want[i] {
			t.Fatalf("row %d=%q want %q", i, got[i].Content, want[i])
		}
	}
}

func TestNormalizeDiffPresentationRowsPreservesRemainderAndBlankLines(t *testing.T) {
	rows := []tools.DiffRow{{Content: "\tbase"}, {Content: "\t  aligned"}, {Content: "\t\tchild"}, {Content: "    "}}
	got := normalizeDiffPresentationRows(rows)
	want := []string{"base", "  aligned", "  child", ""}
	for i := range want {
		if got[i].Content != want[i] {
			t.Fatalf("row %d=%q want %q", i, got[i].Content, want[i])
		}
	}
}
