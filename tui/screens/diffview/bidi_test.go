package diffview

import (
	"strings"
	"testing"

	"crude/tui/surface"
	"crude/tui/ui"
)

// Comparison panes show a Hebrew run in reading order inside a
// left-to-right source line, and a pointer on a shown letter addresses
// that letter's source column.
func TestComparisonArrangesRightToLeftRunsAndMapsThePointer(t *testing.T) {
	p := Prepare(Comparison{BeforeLabel: "Saved", AfterLabel: "Buffer", Before: "ab אבג\n", After: "ab אבג!\n"})
	s := New(nil)
	s.SetPrepared(p)
	h := host(s, 80, 8)
	buf := surface.NewCellBuffer(80, 8)
	h.Paint(buf)
	if !strings.Contains(text(buf), "ab גבא!") {
		t.Fatalf("after pane does not show the Hebrew run in reading order:\n%s", text(buf))
	}
	pane := s.after
	row := -1
	for i, r := range s.rows() {
		if r.AfterLine == 1 {
			row = i
		}
	}
	if row < 0 {
		t.Fatal("changed row not found")
	}
	y := row - s.scroll.Offset() + 1
	// Screen column 3 of the text shows ג, source column 5.
	at := pane.pointer(ui.EventContext{LocalX: pane.gutter() + 3, LocalY: y})
	if at.row != row || at.column != 5 {
		t.Fatalf("pointer on ג = %+v, want row %d column 5", at, row)
	}
}
