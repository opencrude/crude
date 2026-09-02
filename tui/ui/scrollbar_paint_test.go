package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

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
	if c.Char != '┃' {
		t.Fatalf("thumb row char=%q want ┃", string(c.Char))
	}
	track := ScrollbarCell(scroll, 5, 0, ts, bg)
	if track.Char != '│' && track.Char != '┃' {
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
	if c.Char != '│' {
		t.Fatalf("no-overflow gutter char=%q want │", string(c.Char))
	}
}

func TestScrollbarTrackClickPagesAndRequestsLayout(t *testing.T) {
	scroll := NewScrollModel()
	scroll.SetContent(100)
	scroll.SetViewport(10)
	bar := NewScrollbarControl("scroll", scroll, nil)
	bar.Layout(LayoutContext{}, Rect{W: 1, H: 10})
	var oldSeen, newSeen int
	bar.OnScroll(func(oldOffset, newOffset int) { oldSeen, newSeen = oldOffset, newOffset })

	res := bar.Handle(EventContext{LocalY: 9}, MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
	if scroll.Offset() != 9 {
		t.Fatalf("track click offset=%d want one page (9)", scroll.Offset())
	}
	if bar.IsDragging() {
		t.Fatal("track click must page, not turn into a thumb drag")
	}
	if oldSeen != 0 || newSeen != 9 {
		t.Fatalf("OnScroll=(%d,%d) want (0,9)", oldSeen, newSeen)
	}
	if res.Invalidate&InvalidateLayout == 0 || res.Invalidate&InvalidatePaint == 0 {
		t.Fatalf("scroll invalidation=%v must include layout and paint", res.Invalidate)
	}
}

func TestScrollbarNeededForCurrentLayoutViewport(t *testing.T) {
	scroll := NewScrollModel()
	scroll.SetContent(8)
	scroll.SetViewport(20) // previous frame
	bar := NewScrollbarControl("scroll", scroll, nil)
	if !bar.NeededForViewport(4) {
		t.Fatal("shrinking viewport must expose scrollbar in the same layout pass")
	}
	scroll.SetViewport(4)
	if bar.NeededForViewport(8) {
		t.Fatal("growing viewport must hide scrollbar in the same layout pass")
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
	if ch := buf.Get(0, 0).Char; ch == '│' || ch == '┃' || ch == '█' {
		t.Fatalf("idle scrollbar painted track/thumb %q", ch)
	}

	scroll.SetContent(20)
	if !bar.Needed() {
		t.Fatal("scrollbar must appear when content overflows")
	}
	bar.Paint(buf, 0, 0, PaintContext{})
	if buf.Get(0, 0).Char != '│' && buf.Get(0, 0).Char != '┃' && buf.Get(0, 0).Char != '█' {
		t.Fatalf("overflow scrollbar char=%q", buf.Get(0, 0).Char)
	}
}

func TestScrollbarPaddedHoverAndHorizontalOrientation(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	scroll := NewScrollModel()
	scroll.SetContent(100)
	scroll.SetViewport(10)
	bar := NewScrollbarControl("scroll", scroll, tokens)
	bar.Layout(LayoutContext{}, Rect{W: 3, H: 10})
	buf := surface.NewCellBuffer(3, 10)
	bar.Paint(buf, 0, 0, PaintContext{})
	if buf.Get(0, 0).Char != ' ' || buf.Get(2, 0).Char != ' ' || buf.Get(1, 0).Char != '┃' {
		t.Fatalf("padded vertical thumb = %q %q %q", buf.Get(0, 0).Char, buf.Get(1, 0).Char, buf.Get(2, 0).Char)
	}
	bar.Paint(buf, 0, 0, PaintContext{Hovered: true})
	if buf.Get(1, 0).Char != '█' {
		t.Fatalf("hover thumb = %q", buf.Get(1, 0).Char)
	}

	bar.SetDirection(Horizontal)
	bar.Layout(LayoutContext{}, Rect{W: 10, H: 3})
	buf = surface.NewCellBuffer(10, 3)
	bar.Paint(buf, 0, 0, PaintContext{})
	if buf.Get(0, 0).Char != ' ' || buf.Get(0, 2).Char != ' ' || buf.Get(0, 1).Char != '▁' {
		t.Fatalf("horizontal thumb/padding = %q %q %q", buf.Get(0, 0).Char, buf.Get(0, 1).Char, buf.Get(0, 2).Char)
	}
	bar.Paint(buf, 0, 0, PaintContext{Hovered: true})
	if got := buf.Get(0, 1); got.Char != '▄' || !got.BG.Equal(tokens.Color(ColorBg)) {
		t.Fatalf("horizontal hover thumb=%+v want lower block without hover fill", got)
	}
}

// TestScrollbarSurfaceBackgroundRole pins the gutter fill contract: the fill
// adopts the surface role so a popup-hosted scrollbar matches the popup body
// instead of painting a darker ColorBg strip. The shell default must remain
// ColorBg, and track/thumb FG roles are unaffected.
func TestScrollbarSurfaceBackgroundRole(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	scroll := NewScrollModel()
	scroll.SetContent(100)
	scroll.SetViewport(10)
	bar := NewScrollbarControl("scroll", scroll, tokens)
	bar.Layout(LayoutContext{}, Rect{W: 1, H: 10})
	buf := surface.NewCellBuffer(1, 10)
	bar.Paint(buf, 0, 0, PaintContext{})
	if bg := buf.Get(0, 5).BG; !bg.Equal(tokens.Color(ColorBg)) {
		t.Fatalf("default gutter bg=%v want ColorBg %v", bg, tokens.Color(ColorBg))
	}

	bar.SetSurfaceBackground(ColorPopupBg)
	buf = surface.NewCellBuffer(1, 10)
	bar.Paint(buf, 0, 0, PaintContext{})
	popup := tokens.Color(ColorPopupBg)
	if popup.Equal(tokens.Color(ColorBg)) {
		t.Fatal("harness: dark theme popup bg must differ from shell bg")
	}
	if bg := buf.Get(0, 5).BG; !bg.Equal(popup) {
		t.Fatalf("popup gutter bg=%v want ColorPopupBg %v", bg, popup)
	}
	// The track glyph color is independent of the surface fill.
	if fg := buf.Get(0, 5).FG; !fg.Equal(tokens.Color(ColorBorder)) {
		t.Fatalf("track fg=%v want ColorBorder %v", fg, tokens.Color(ColorBorder))
	}
}
