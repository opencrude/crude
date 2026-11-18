package predictioncontext

import (
	"strings"
	"testing"

	"crude/types"
)

func TestRegionIsWholeLinesAroundTheCaret(t *testing.T) {
	var src strings.Builder
	for i := range 40 {
		src.WriteString("line " + string(rune('A'+i%26)) + "\n")
	}
	text := []byte(src.String())
	caret := strings.Index(src.String(), "line U") + len("line ")
	c, err := Build(Input{Path: "a.txt", Text: text, Caret: caret}, 4000)
	if err != nil {
		t.Fatal(err)
	}
	if c.Region[c.Caret:] == "" || !strings.HasPrefix(c.Region[c.Caret:], "U\n") {
		t.Fatalf("caret %d in region %q", c.Caret, c.Region)
	}
	if c.RegionStart > 0 && text[c.RegionStart-1] != '\n' || !strings.HasSuffix(c.Region, "\n") {
		t.Fatalf("region is not whole lines: %q", c.Region)
	}
	if lines := strings.Count(c.Region, "\n"); lines != regionLinesBefore+regionLinesAfter+1 {
		t.Fatalf("region lines = %d", lines)
	}
	if string(text[:c.RegionStart]) != c.Before || string(text[c.RegionEnd:]) != c.After {
		t.Fatal("excerpts do not touch the region")
	}
	if c.Prefix+c.Suffix != src.String() {
		t.Fatal("fill-in-the-middle windows lost text")
	}
}

func TestBudgetDropsOptionalContextFirst(t *testing.T) {
	text := []byte(strings.Repeat("filler line of code\n", 600) + "func target() {\n\t\n}\n")
	caret := len(text) - len("\n}\n")
	edits := []types.RecentEdit{{Path: "a.go", Line: 1, Inserted: strings.Repeat("x", 500), Author: "you"}}
	snippets := []Snippet{{Path: "b.go", Line: 1, Text: strings.Repeat("y", 1000)}}
	full, err := Build(Input{Path: "a.go", Text: text, Caret: caret, RecentEdits: edits, Snippets: snippets}, 100000)
	if err != nil || len(full.Snippets) != 1 || len(full.Edits) != 1 {
		t.Fatalf("full = %+v, %v", full.Snippets, err)
	}
	small, err := Build(Input{Path: "a.go", Text: text, Caret: caret, RecentEdits: edits, Snippets: snippets}, 400)
	if err != nil {
		t.Fatal(err)
	}
	if len(small.Snippets) != 0 || small.Region != full.Region || small.EstimatedTokens > 400 {
		t.Fatalf("budget kept snippets or changed the region: %d snippets, %d tokens", len(small.Snippets), small.EstimatedTokens)
	}
	if full.Digest == small.Digest {
		t.Fatal("different context has the same digest")
	}
	again, _ := Build(Input{Path: "a.go", Text: text, Caret: caret, RecentEdits: edits, Snippets: snippets}, 100000)
	if again.Digest != full.Digest {
		t.Fatal("digest is not deterministic")
	}
}

func TestDiagnosticsNearTheRegionAndUTF8Bounds(t *testing.T) {
	text := []byte("a\nb\nسلام\nd\n")
	caret := strings.Index(string(text), "لام")
	c, err := Build(Input{Path: "a.go", Text: text, Caret: caret, Diagnostics: []types.LSPDiagnostic{
		{Line: 2, Message: "near\nsecond line"}, {Line: 400, Message: "far"},
	}, Completions: []string{" first ", "", "second\nline"}}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Diagnostics) != 1 || c.Diagnostics[0] != "line 3: near" {
		t.Fatalf("diagnostics = %q", c.Diagnostics)
	}
	if strings.Join(c.Completions, ",") != "first,second" {
		t.Fatalf("completions = %q", c.Completions)
	}
	for _, bad := range []int{-1, len(text) + 1, caret + 1} {
		if _, err := Build(Input{Text: text, Caret: bad}, 1000); err == nil {
			t.Fatalf("caret %d accepted", bad)
		}
	}
	if _, err := Build(Input{Text: []byte{0xff}, Caret: 0}, 1000); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
}
