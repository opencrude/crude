package fileio

import (
	"strings"
	"testing"
)

// Models trained on exact string replacement (Claude Opus among them) start
// and end old_string inside a line. The line-based phases used to widen such a
// fragment to whole lines, so the replacement silently dropped the rest of the
// first and last line. These are the shapes that lost code in practice.

func TestFragmentEditKeepsTextAfterTheMatchOnTheLastLine(t *testing.T) {
	content := "func f() {\n\tif !request.selection.Explicit && (msg.preview.Quiet || m.overlayHost == nil) {\n\t\treturn\n\t}\n}\n"
	got, err := ApplySearchReplace(content, []SearchReplace{{
		Search:  "\tif !request.selection.Explicit",
		Replace: "\tif !request.selection.Explicit && !request.forced",
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "func f() {\n\tif !request.selection.Explicit && !request.forced && (msg.preview.Quiet || m.overlayHost == nil) {\n\t\treturn\n\t}\n}\n", got)
}

// The exact edit that lost a link while this fix was documented.
func TestFragmentEditEndingInsideAMarkdownLinkKeepsTheLink(t *testing.T) {
	content := "  provider failure classification.\n- [Workspace collaboration runtime](reference/workspace-collaboration.md) —\n  durable agents.\n"
	got, err := ApplySearchReplace(content, []SearchReplace{{
		Search:  "  provider failure classification.\n- [Workspace collaboration runtime]",
		Replace: "  provider failure classification.\n- [Model tool failures](reference/model-tool-failures.md)\n- [Workspace collaboration runtime]",
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "  provider failure classification.\n- [Model tool failures](reference/model-tool-failures.md)\n- [Workspace collaboration runtime](reference/workspace-collaboration.md) —\n  durable agents.\n", got)
}

func TestFragmentEditAcrossLinesKeepsBothEdges(t *testing.T) {
	content := "a := call(first,\n\tsecond,\n\tthird) // keep this comment\nb := 2\n"
	got, err := ApplySearchReplace(content, []SearchReplace{{
		Search:  "call(first,\n\tsecond,\n\tthird)",
		Replace: "call(first, second, third)",
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "a := call(first, second, third) // keep this comment\nb := 2\n", got)
}

// When the fragment is not byte-exact (here the file has trailing spaces the
// model did not copy), the substring phase must still keep the text outside
// the match.
func TestFuzzyFragmentEditKeepsEdgeText(t *testing.T) {
	content := "x := compute(a,  \n\tb) + tail\n"
	got, err := ApplySearchReplace(content, []SearchReplace{{
		Search:  "compute(a,\n\tb)",
		Replace: "compute(a, b)",
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "x := compute(a, b) + tail\n", got)
}

// A middle line that matches only part of its content line is not the text
// the model saw; replacing it would delete the rest of that line.
func TestPartialMiddleLineIsRefusedWithoutChanges(t *testing.T) {
	content := "start(\n\tvalue, extra,\n\tend)\n"
	_, err := ApplySearchReplace(content, []SearchReplace{{
		Search:  "start(\n\tvalue,\n\tend)",
		Replace: "start(value, end)",
	}}, nil)
	if err == nil || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("partial middle line was not refused: %v", err)
	}
}

func TestExactFragmentStillRefusesAmbiguousText(t *testing.T) {
	content := "x := 1\ny := 1\n"
	if _, err := ApplySearchReplace(content, []SearchReplace{{Search: " := 1", Replace: " := 2"}}, nil); err == nil {
		t.Fatal("an ambiguous fragment was replaced")
	}
}

func TestExactFragmentKeepsCRLF(t *testing.T) {
	content := "a = one(x) + two\r\nb\r\n"
	got, err := ApplySearchReplace(content, []SearchReplace{{Search: "one(x) +", Replace: "one(y) -"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "a = one(y) - two\r\nb\r\n", got)
}
