package rendering

import (
	"strings"
	"testing"

	"crude/tui/surface"
)

// An ANSI line is one label with an automatic direction: a right-to-left
// phrase split by color changes is arranged as one phrase, the line takes
// the direction of its first letter, and each letter keeps its color.
func TestANSILineArrangesAcrossStyleChanges(t *testing.T) {
	buf := surface.NewCellBuffer(20, 2)
	RenderToBufferAt(buf, 0, 0, "\x1b[31mאב\x1b[32mגד\x1b[0m ok\nplain")
	var row strings.Builder
	for x := 0; x < 7; x++ {
		row.WriteRune(buf.Get(x, 0).Char)
	}
	if got := row.String(); got != "ok דגבא" {
		t.Fatalf("row 0 = %q, want a right-to-left line: the Hebrew phrase at the right, then ok", got)
	}
	// אב were written red and sit at the right end; גד green, left of them.
	if buf.Get(6, 0).FG != buf.Get(5, 0).FG || buf.Get(4, 0).FG != buf.Get(3, 0).FG || buf.Get(6, 0).FG == buf.Get(3, 0).FG {
		t.Fatal("colors did not move with their letters")
	}
	if got := surface.ReadLogical(buf.Row(0)[:7]); got != "אבגד ok" {
		t.Fatalf("copy of row 0 = %q", got)
	}
	if buf.Get(0, 1).Char != 'p' {
		t.Fatal("the next ANSI line was arranged with the first")
	}
}
