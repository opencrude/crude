package ui

import (
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
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

func TestScrollbarControlOnDemandWhenContentFits(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	scroll := NewScrollModel()
	scroll.SetContent(5)
	scroll.SetViewport(10)
	bar := NewScrollbarControl("scroll", scroll, tokens)
	bar.Layout(LayoutContext{}, Rect{X: 0, Y: 0, W: 1, H: 10})
	if bar.Needed() {
		t.Fatal("scrollbar must stay hidden when content fits")
	}
	buf := surface.NewCellBuffer(1, 10)
	bar.Paint(buf, 0, 0, PaintContext{})
	if ch := buf.Get(0, 0).Char; ch == '░' || ch == '█' {
		t.Fatalf("idle scrollbar painted track/thumb %q", ch)
	}

	scroll.SetContent(20)
	if !bar.Needed() {
		t.Fatal("scrollbar must appear when content overflows")
	}
	bar.Paint(buf, 0, 0, PaintContext{})
	if buf.Get(0, 0).Char != '░' && buf.Get(0, 0).Char != '█' {
		t.Fatalf("overflow scrollbar char=%q", buf.Get(0, 0).Char)
	}
}
