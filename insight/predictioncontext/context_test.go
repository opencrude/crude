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

// BenchmarkBuildDense measures context assembly on the largest accepted
// source with every context source full. The target is 5 ms at p99 (see
// docs/reference/performance-baselines.md).
func BenchmarkBuildDense(b *testing.B) {
	var src strings.Builder
	for i := 0; src.Len() < MaxSourceBytes-200; i++ {
		src.WriteString("func handler(request Request) Response { return serve(request, " + strings.Repeat("x", i%40) + ") }\n")
	}
	text := []byte(src.String())
	in := Input{Path: "pkg/dense.go", Language: "go", Text: text, Caret: len(text) / 2}
	for i := range 64 {
		in.RecentEdits = append(in.RecentEdits, types.RecentEdit{Path: "pkg/dense.go", Line: 100 + i, Removed: "serve(request)", Inserted: "serve(request, options)", Author: types.RecentEditYou})
		in.Diagnostics = append(in.Diagnostics, types.LSPDiagnostic{Line: 9000 + i, Message: "undefined: options", Severity: 1})
		in.Completions = append(in.Completions, "handlerOption"+strings.Repeat("s", i%8))
	}
	for i := range 4 {
		in.Snippets = append(in.Snippets, Snippet{Path: "pkg/other.go", Line: i * 40, Text: strings.Repeat("func related(request Request) Response { return nil }\n", 40)})
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Build(in, 4000); err != nil {
			b.Fatal(err)
		}
	}
}
