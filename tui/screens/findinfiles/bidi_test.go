package findinfiles

import (
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
)

// A Hebrew result line reads right to left, and the matched letter stays
// highlighted where it is shown.
func TestHighlightedResultArrangesRightToLeftText(t *testing.T) {
	buf := surface.NewCellBuffer(10, 1)
	fg, match := cell.NoColor, cell.NewTrueColor(255, 0, 0)
	end := writeHighlightedClipped(buf, 0, 10, 0, "אבג", []int{0}, fg, match, cell.NoColor, 0, cell.AttrBold)
	if end != 3 {
		t.Fatalf("end = %d, want 3", end)
	}
	if got := string([]rune{buf.Get(0, 0).Char, buf.Get(1, 0).Char, buf.Get(2, 0).Char}); got != "גבא" {
		t.Fatalf("row = %q, want visual order", got)
	}
	if buf.Get(2, 0).FG != match || buf.Get(0, 0).FG == match {
		t.Fatal("the highlight did not move with the matched letter")
	}
}
