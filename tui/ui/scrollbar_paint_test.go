package ui

import (
	"testing"

	"crude/tui/cell"
	"crude/tui/theme"
)

func TestScrollbarCellThumbMath(t *testing.T) {
	scroll := NewScrollModel()
	scroll.SetContent(20)
	scroll.SetViewport(5)
	scroll.ScrollTo(5)
	top, th := scroll.ThumbBounds(5)
	if top < 0 || th < 1 || top+th > 5 {
		t.Fatalf("ThumbBounds(5) = top=%d th=%d", top, th)
	}
	ts := NewTokenSet(theme.DefaultDark())
	bg := ts.Color(ColorSubPaneBg)
	c := ScrollbarCell(scroll, 5, top, ts, bg)
	if c.Char != '█' {
		t.Fatalf("thumb row char=%q want █", string(c.Char))
	}
	track := ScrollbarCell(scroll, 5, 0, ts, bg)
	if track.Char != '░' && track.Char != '█' {
		t.Fatalf("track cell char=%q", string(track.Char))
	}
}

func TestScrollbarCellNoOverflowUsesTrackFill(t *testing.T) {
	scroll := NewScrollModel()
	scroll.SetContent(3)
	scroll.SetViewport(5)
	ts := NewTokenSet(theme.DefaultDark())
	bg := cell.NoColor
	c := ScrollbarCell(scroll, 5, 0, ts, bg)
	if c.Char != '░' {
		t.Fatalf("no-overflow gutter char=%q want ░", string(c.Char))
	}
}
