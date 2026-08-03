package ui

import (
	"testing"

	"crude/tui/surface"
	"crude/tui/theme"
)

func TestFlex_RowGrowDistributesFreeSpace(t *testing.T) {
	a := newTestBox("a", 1, 1, 'a')
	b := newTestBox("b", 1, 1, 'b')
	f := NewFlex("f", FlexRow, 0, NewFlexChild(a, 1, 0, 0), NewFlexChild(b, 1, 0, 0))
	f.Layout(LayoutContext{}, Rect{X: 0, Y: 0, W: 10, H: 1})
	if a.Bounds() != (Rect{W: 5, H: 1}) {
		t.Fatalf("a=%+v", a.Bounds())
	}
	if b.Bounds() != (Rect{X: 5, W: 5, H: 1}) {
		t.Fatalf("b=%+v", b.Bounds())
	}
}

func TestFlex_RowGrowProportional(t *testing.T) {
	a := newTestBox("a", 1, 1, 'a')
	b := newTestBox("b", 1, 1, 'b')
	// grow 1 vs 3 in 12 cells: extra = 10, a gets 2 (rounding remainder lands
	// on the first grow child), b gets 7.
	f := NewFlex("f", FlexRow, 0, NewFlexChild(a, 1, 0, 0), NewFlexChild(b, 3, 0, 0))
	f.Layout(LayoutContext{}, Rect{W: 12, H: 1})
	if a.Bounds().W != 4 || b.Bounds().W != 8 {
		t.Fatalf("a=%+v b=%+v", a.Bounds(), b.Bounds())
	}
}

func TestFlex_RowShrinkOnOverflow(t *testing.T) {
	a := newTestBox("a", 8, 1, 'a')
	b := newTestBox("b", 8, 1, 'b')
	f := NewFlex("f", FlexRow, 0, NewFlexChild(a, 0, 1, 0), NewFlexChild(b, 0, 1, 0))
	f.Layout(LayoutContext{}, Rect{W: 10, H: 1})
	if a.Bounds().W != 5 || b.Bounds().W != 5 {
		t.Fatalf("a=%+v b=%+v", a.Bounds(), b.Bounds())
	}
	if b.Bounds().X != 5 {
		t.Fatalf("b.X=%d", b.Bounds().X)
	}
}

func TestFlex_ColumnStretchCross(t *testing.T) {
	a := newTestBox("a", 1, 1, 'a')
	b := newTestBox("b", 3, 1, 'b')
	f := NewFlex("f", FlexColumn, 0, NewFlexChild(a, 0, 0, 0), NewFlexChild(b, 0, 0, 0))
	f.SetCrossAxisAlignment(CrossStretch)
	f.Layout(LayoutContext{}, Rect{W: 8, H: 4})
	if a.Bounds().W != 8 || b.Bounds().W != 8 {
		t.Fatalf("stretch widths a=%+v b=%+v", a.Bounds(), b.Bounds())
	}
	if a.Bounds().Y != 0 || b.Bounds().Y != 1 {
		t.Fatalf("a.Y=%d b.Y=%d", a.Bounds().Y, b.Bounds().Y)
	}
}

func TestFlex_RowCrossAlignPerChild(t *testing.T) {
	a := newTestBox("a", 1, 2, 'a')
	b := newTestBox("b", 1, 1, 'b')
	f := NewFlex("f", FlexRow, 0, NewFlexChild(a, 0, 0, 0), NewFlexChild(b, 0, 0, 0))
	f.SetCrossAxisAlignment(CrossEnd)
	f.SetMainAxisAlignment(MainStart)
	f.Layout(LayoutContext{}, Rect{W: 4, H: 4})
	// b (cross 1) aligned to cross-end: Y = 4 - 1 = 3
	if a.Bounds().Y != 2 || b.Bounds().Y != 3 {
		t.Fatalf("a=%+v b=%+v", a.Bounds(), b.Bounds())
	}
}

func TestFlex_RowMainAxisAlignment(t *testing.T) {
	cases := []struct {
		align MainAxisAlignment
		want  []int
	}{
		{MainStart, []int{0, 2, 4}},
		{MainCenter, []int{3, 5, 7}},
		{MainEnd, []int{6, 8, 10}},
		{MainSpaceBetween, []int{0, 5, 10}},
		{MainSpaceAround, []int{1, 5, 9}},
		{MainSpaceEvenly, []int{1, 4, 7}},
	}
	for _, tc := range cases {
		a := newTestBox("a", 2, 1, 'a')
		b := newTestBox("b", 2, 1, 'b')
		c := newTestBox("c", 2, 1, 'c')
		f := NewFlex("f", FlexRow, 0, NewFlexChild(a, 0, 0, 0), NewFlexChild(b, 0, 0, 0), NewFlexChild(c, 0, 0, 0))
		f.SetMainAxisAlignment(tc.align)
		f.Layout(LayoutContext{}, Rect{W: 12, H: 1})
		got := []int{a.Bounds().X, b.Bounds().X, c.Bounds().X}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("align=%v got %v want %v", tc.align, got, tc.want)
			}
		}
	}
}

func TestFlex_RowOverflowNoPanic(t *testing.T) {
	a := newTestBox("a", 10, 1, 'a')
	b := newTestBox("b", 10, 1, 'b')
	f := NewFlex("f", FlexRow, 0, NewFlexChild(a, 0, 0, 0), NewFlexChild(b, 0, 0, 0))
	sz := f.Measure(LayoutContext{}, Tight(10, 1))
	if sz.W != 10 || sz.H != 1 {
		t.Fatalf("measure %+v", sz)
	}
	f.Layout(LayoutContext{}, Rect{W: 10, H: 1})
	if a.Bounds().X != 0 || b.Bounds().X != 10 {
		t.Fatalf("overflow positions a=%+v b=%+v", a.Bounds(), b.Bounds())
	}
}

func TestFlex_RowWrapLines(t *testing.T) {
	a := newTestBox("a", 4, 1, 'a')
	b := newTestBox("b", 4, 1, 'b')
	c := newTestBox("c", 4, 1, 'c')
	f := NewFlex("f", FlexRow, 1, NewFlexChild(a, 0, 0, 0), NewFlexChild(b, 0, 0, 0), NewFlexChild(c, 0, 0, 0))
	f.SetWrap(WrapLine)
	f.Layout(LayoutContext{}, Rect{W: 10, H: 5})
	if a.Bounds().Y != 0 || b.Bounds().Y != 0 || c.Bounds().Y != 2 {
		t.Fatalf("lines a=%+v b=%+v c=%+v", a.Bounds(), b.Bounds(), c.Bounds())
	}
	if b.Bounds().X != 5 {
		t.Fatalf("b.X=%d", b.Bounds().X)
	}
	sz := f.Measure(LayoutContext{}, Loose(10, 5))
	if sz.W != 9 || sz.H != 3 {
		t.Fatalf("measure %+v", sz)
	}
}

func TestFlex_FlexBasis(t *testing.T) {
	a := newTestBox("a", 1, 1, 'a')
	b := newTestBox("b", 1, 1, 'b')
	f := NewFlex("f", FlexRow, 0, NewFlexChild(a, 0, 0, 6), NewFlexChild(b, 0, 0, 0))
	sz := f.Measure(LayoutContext{}, Loose(20, 1))
	if sz.W != 7 {
		t.Fatalf("measure %+v", sz)
	}
	f.Layout(LayoutContext{}, Rect{W: 20, H: 1})
	if a.Bounds().W != 6 || b.Bounds().X != 6 {
		t.Fatalf("a=%+v b=%+v", a.Bounds(), b.Bounds())
	}
}

func TestGrid_FlowCells(t *testing.T) {
	a := newTestBox("a", 2, 1, 'a')
	b := newTestBox("b", 2, 1, 'b')
	c := newTestBox("c", 2, 1, 'c')
	d := newTestBox("d", 2, 1, 'd')
	g := NewGrid("g", 3, 1, a, b, c, d)
	g.Layout(LayoutContext{}, Rect{W: 8, H: 3})
	want := []Rect{
		{W: 2, H: 1},
		{X: 3, W: 2, H: 1},
		{X: 6, W: 2, H: 1},
		{Y: 2, W: 2, H: 1},
	}
	boxes := []*testBox{a, b, c, d}
	for i, box := range boxes {
		if box.Bounds() != want[i] {
			t.Fatalf("child %d bounds %+v want %+v", i, box.Bounds(), want[i])
		}
	}
	sz := g.Measure(LayoutContext{}, Loose(8, 3))
	if sz.W != 8 || sz.H != 3 {
		t.Fatalf("measure %+v", sz)
	}
}

func TestGrid_FixedAndFractionTracks(t *testing.T) {
	a := newTestBox("a", 2, 1, 'a')
	b := newTestBox("b", 2, 1, 'b')
	g := NewGrid("g", 2, 0, a, b)
	g.SetColSizes([]GridTrack{FixedGridTrack(2), FractionGridTrack(1)})
	g.Layout(LayoutContext{}, Rect{W: 6, H: 1})
	if a.Bounds().W != 2 || b.Bounds().X != 2 || b.Bounds().W != 4 {
		t.Fatalf("a=%+v b=%+v", a.Bounds(), b.Bounds())
	}
}

func TestGrid_ZeroSizeNoPanic(t *testing.T) {
	a := newTestBox("a", 2, 1, 'a')
	g := NewGrid("g", 2, 0, a)
	g.Layout(LayoutContext{}, Rect{W: 0, H: 0})
	if g.Measure(LayoutContext{}, Tight(0, 0)) != (Size{}) {
		t.Fatal("zero measure")
	}
}

func TestInset_OffsetAndClamp(t *testing.T) {
	c := newTestBox("c", 5, 2, 'c')
	ins := NewInset("ins", InsetsFromSides(1, 2, 3, 4), c)
	sz := ins.Measure(LayoutContext{}, Loose(100, 100))
	if sz.W != 11 || sz.H != 6 {
		t.Fatalf("measure %+v", sz)
	}
	ins.Layout(LayoutContext{}, Rect{W: 20, H: 10})
	if c.Bounds() != (Rect{X: 4, Y: 1, W: 14, H: 6}) {
		t.Fatalf("child bounds %+v", c.Bounds())
	}
	ins2 := NewInset("ins2", InsetsAll(1), newTestBox("c2", 50, 50, 'x'))
	ins2.Layout(LayoutContext{}, Rect{W: 2, H: 2})
	if ins2.Children()[0].Bounds() != (Rect{X: 1, Y: 1, W: 0, H: 0}) {
		t.Fatalf("clamped child %+v", ins2.Children()[0].Bounds())
	}
}

func TestSplitPane_FractionAndDivider(t *testing.T) {
	a := newTestBox("a", 1, 1, 'a')
	b := newTestBox("b", 1, 1, 'b')
	s := NewSplitPane("s", Horizontal, a, b)
	s.Layout(LayoutContext{}, Rect{W: 10, H: 5})
	if a.Bounds() != (Rect{W: 5, H: 5}) {
		t.Fatalf("first %+v", a.Bounds())
	}
	if b.Bounds() != (Rect{X: 6, W: 4, H: 5}) {
		t.Fatalf("second %+v", b.Bounds())
	}
	s.SetDividerPosition(7)
	s.Layout(LayoutContext{}, Rect{W: 10, H: 5})
	if a.Bounds().W != 7 || b.Bounds().X != 8 || b.Bounds().W != 2 {
		t.Fatalf("abs pos first=%+v second=%+v", a.Bounds(), b.Bounds())
	}
	s.SetDividerPosition(100)
	s.Layout(LayoutContext{}, Rect{W: 10, H: 5})
	if a.Bounds().W != 9 {
		t.Fatalf("clamped first=%+v", a.Bounds())
	}
}

func TestSplitPane_Vertical(t *testing.T) {
	a := newTestBox("a", 1, 1, 'a')
	b := newTestBox("b", 1, 1, 'b')
	s := NewSplitPane("s", Vertical, a, b)
	s.Layout(LayoutContext{}, Rect{W: 10, H: 10})
	if a.Bounds() != (Rect{W: 10, H: 5}) || b.Bounds() != (Rect{Y: 6, W: 10, H: 4}) {
		t.Fatalf("a=%+v b=%+v", a.Bounds(), b.Bounds())
	}
}

func TestViewport_LayoutScroll(t *testing.T) {
	c := newTestBox("c", 20, 10, 'c')
	vp := NewViewport("vp", c)
	vp.SetContentSize(20, 10)
	vp.SetScrollOffset(4, 2)
	vp.Layout(LayoutContext{}, Rect{X: 5, Y: 5, W: 8, H: 3})
	if c.Bounds() != (Rect{X: -4, Y: -2, W: 20, H: 10}) {
		t.Fatalf("child bounds %+v", c.Bounds())
	}
	sz := vp.Measure(LayoutContext{}, Loose(30, 10))
	if sz.W != 30 || sz.H != 10 {
		t.Fatalf("measure %+v", sz)
	}
}

func TestViewport_AutoContentSize(t *testing.T) {
	c := newTestBox("c", 20, 10, 'c')
	vp := NewViewport("vp", c)
	vp.Layout(LayoutContext{}, Rect{W: 8, H: 3})
	if c.Bounds() != (Rect{W: 20, H: 10}) {
		t.Fatalf("auto content %+v", c.Bounds())
	}
}

// TestHost_ViewportClipsPaintAndHit mounts a marker box next to a scrolled
// viewport whose child content is wider than the viewport. Without clipping the
// child's paint would bleed left over the marker; the leaf viewport must keep
// both the paint and the hit results inside its own bounds.
func TestHost_ViewportClipsPaintAndHit(t *testing.T) {
	th := theme.DefaultDark()
	marker := newTestBox("marker", 3, 1, 'M')
	content := newTestBox("content", 20, 5, 'C')
	vp := NewViewport("vp", content)
	vp.SetContentSize(20, 5)
	vp.SetScrollOffset(5, 0)
	root := NewStack("root", Horizontal, 0, marker, vp)
	h := NewHost(th, root)
	h.Resize(13, 5)
	buf := surface.NewCellBuffer(13, 5)
	h.Paint(buf)
	if buf.Get(1, 2).Char != 'M' {
		t.Fatalf("marker overwritten: %q", buf.Get(1, 2).Char)
	}
	if buf.Get(5, 2).Char != 'C' {
		t.Fatalf("content missing at (5,2): %q", buf.Get(5, 2).Char)
	}
	if buf.Get(12, 2).Char != 'C' {
		t.Fatalf("content missing at (12,2): %q", buf.Get(12, 2).Char)
	}
	if got := h.HitTest(1, 2); got != marker.ID() {
		t.Fatalf("hit marker = %q", got)
	}
	// The viewport is a leaf; the painted content region is hit as the viewport.
	if got := h.HitTest(5, 2); got != vp.ID() {
		t.Fatalf("hit viewport = %q", got)
	}
}
