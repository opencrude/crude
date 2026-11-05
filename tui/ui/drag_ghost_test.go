package ui

import (
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
)

func ghostRow(buf *surface.CellBuffer, y int) string {
	var b strings.Builder
	for x := 0; x < buf.Width(); x++ {
		if c := buf.Get(x, y); c.Width > 0 && c.Char != 0 {
			b.WriteRune(c.Char)
		}
	}
	return b.String()
}

// The ghost names the item and the drop below-right of the pointer, and its
// shadow darkens the content under it without erasing it.
func TestDragGhostFollowsPointerWithShadow(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	under := tokens.Color(ColorBg)
	buf := surface.NewCellBuffer(60, 12)
	buf.Fill(cell.NewCell('x', tokens.Color(ColorFg), under, 0))
	ghost := DragGhost{Label: "notes.txt", Intent: "Move to src", X: 10, Y: 3}
	PaintDragGhost(buf, ghost, tokens)

	r := ghost.Rect(60, 12)
	if r.X <= ghost.X || r.Y <= ghost.Y {
		t.Fatalf("ghost covers the pointer: %+v", r)
	}
	if !strings.Contains(ghostRow(buf, r.Y), "notes.txt") || !strings.Contains(ghostRow(buf, r.Y+1), "Move to src") {
		t.Fatalf("ghost rows: %q / %q", ghostRow(buf, r.Y), ghostRow(buf, r.Y+1))
	}
	if c := buf.Get(ghost.X, ghost.Y); c.Char != 'x' || c.BG != under {
		t.Fatal("the cell under the pointer changed")
	}
	shadow := buf.Get(r.X+r.W, r.Y+1)
	if shadow.Char != 'x' || shadow.BG == under {
		t.Fatalf("shadow must darken but keep the content under it: %+v", shadow)
	}
	if c := buf.Get(r.X+r.W, r.Y); c.BG != under {
		t.Fatal("shadow must fall below the card's top edge, not beside it")
	}
}

// Near the right and bottom edges the ghost moves to the other side of the
// pointer so it stays complete on screen.
func TestDragGhostFlipsAtScreenEdges(t *testing.T) {
	ghost := DragGhost{Label: "a-rather-long-file-name.go", Intent: "Open to the right", X: 58, Y: 11}
	r := ghost.Rect(60, 12)
	if r.X+r.W >= ghost.X || r.Y+r.H > ghost.Y || r.X < 0 || r.Y < 0 {
		t.Fatalf("ghost did not flip inside the screen: %+v", r)
	}
}

// A position where a release does nothing reads as muted guidance, not as an
// accepted drop.
func TestDragGhostBlockedIntentIsMuted(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	buf := surface.NewCellBuffer(60, 12)
	ghost := DragGhost{Label: "src", Intent: "Drop on a folder to move", Blocked: true, X: 4, Y: 2}
	PaintDragGhost(buf, ghost, tokens)
	r := ghost.Rect(60, 12)
	if got := buf.Get(r.X+2, r.Y+1); got.Char != 'D' || got.FG != tokens.Color(ColorMuted) {
		t.Fatalf("blocked intent ink: %+v", got)
	}
	if got := buf.Get(r.X, r.Y); got.FG != tokens.Color(ColorMuted) {
		t.Fatal("blocked ghost kept the accent edge")
	}
}
