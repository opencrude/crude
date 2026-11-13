package rendering

import (
	"crude/tui/surface"
	"strings"
	"testing"
)

func TestHyperlinkStateSurvivesSGRAndWrapButNotClosingOrUnsafeTargets(t *testing.T) {
	target := "https://example.test/full/path?query=kept#anchor"
	for _, seq := range [][2]string{{"\x1b]8;id=test;", "\x1b\\"}, {"\x1b]8;;", "\a"}, {"\x9d8;;", "\x9c"}} {
		start, end := seq[0]+target+seq[1], seq[0]+seq[1]
		// Reset sequences change ink, not the active destination.
		raw := start + "界\x1b[0mtext" + end + " plain"
		runs := ParseAnsiRuns(raw)
		var plain strings.Builder
		for _, r := range runs {
			plain.WriteString(r.Text)
			want := ""
			if !strings.Contains(r.Text, "plain") {
				want = target
			}
			if r.Link.Target() != want {
				t.Fatalf("run %q points to %q, want %q", r.Text, r.Link.Target(), want)
			}
		}
		if plain.String() != "界text plain" {
			t.Fatal("hyperlink changed visible text", plain.String())
		}
		b := surface.NewCellBuffer(20, 1)
		RenderToBufferAt(b, 0, 0, raw)
		if b.Get(0, 0).Link.Target() != target || b.Get(1, 0).Link.Target() != target || b.Get(6, 0).Link != nil {
			t.Fatal("wide glyph/closing lost hyperlink ownership")
		}
	}
	var parser AnsiRunParser
	parser.ParseLine("\x1b]8;;" + target + "\x1b\\first")
	next := parser.ParseLine("second\x1b]8;;file:///etc/passwd\x1b\\unsafe")
	if len(next) != 2 || next[0].Link.Target() != target || next[1].Link != nil {
		t.Fatalf("line or unsafe-target boundary leaked: %+v", next)
	}
}
