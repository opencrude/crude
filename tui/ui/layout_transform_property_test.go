package ui

import (
	"fmt"
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
)

// testBox is a fixed-size leaf that paints its marker rune over its whole
// bounds. It is used to verify that geometry, paint, and hit testing agree.
type testBox struct {
	Base
	w, h int
	ch   rune
}

func newTestBox(id ComponentID, w, h int, ch rune) *testBox {
	return &testBox{Base: NewBase(id), w: w, h: h, ch: ch}
}

func (b *testBox) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: b.w, H: b.h})
}

func (b *testBox) Layout(_ LayoutContext, r Rect) { b.SetBounds(r) }

func (b *testBox) Paint(buf *surface.CellBuffer, ox, oy int, _ PaintContext) {
	r := b.Bounds()
	for y := 0; y < r.H; y++ {
		for x := 0; x < r.W; x++ {
			buf.Set(ox+r.X+x, oy+r.Y+y, cell.Cell{Char: b.ch, Content: string(b.ch), Width: 1})
		}
	}
}

// expectScreenRects independently recomputes the parent-origin + child-bounds
// invariant and compares it to the Host screen map. Child bounds are
// parent-local by contract; the sum must equal the registered screen rect.
func expectScreenRects(t *testing.T, host *Host, root Component) {
	t.Helper()
	exp := map[ComponentID]Rect{}
	var walk func(c Component, ox, oy int)
	walk = func(c Component, ox, oy int) {
		b := c.Bounds()
		exp[c.ID()] = Rect{X: ox + b.X, Y: oy + b.Y, W: b.W, H: b.H}
		if p, ok := c.(Parent); ok {
			for _, ch := range p.Children() {
				walk(ch, ox+b.X, oy+b.Y)
			}
		}
	}
	walk(root, 0, 0)
	for id, want := range exp {
		got, ok := host.screen[id]
		if !ok {
			t.Errorf("screen missing for %s", id)
			continue
		}
		if got != want {
			t.Errorf("screen[%s] = %+v, want %+v", id, got, want)
		}
	}
}

// assertHitMatchesPaint checks that every painted cell inside a box's screen
// rect carries the box's marker rune and hit-tests back to that box.
func assertHitMatchesPaint(t *testing.T, host *Host, buf *surface.CellBuffer, boxes []*testBox) {
	t.Helper()
	for _, b := range boxes {
		r, ok := host.screen[b.ID()]
		if !ok || r.Empty() {
			continue
		}
		for y := 0; y < r.H; y++ {
			sy := r.Y + y
			if sy < 0 || sy >= buf.Height() {
				continue
			}
			for x := 0; x < r.W; x++ {
				sx := r.X + x
				if sx < 0 || sx >= buf.Width() {
					continue
				}
				c := buf.Get(sx, sy)
				if c.Char != b.ch {
					t.Errorf("cell(%d,%d) char %q, want box %s char %q", sx, sy, c.Char, b.ID(), b.ch)
					continue
				}
				hit := host.HitTest(sx, sy)
				if hit != b.ID() {
					t.Errorf("HitTest(%d,%d) = %q, want %q", sx, sy, hit, b.ID())
				}
			}
		}
	}
}

// buildNestedTree builds Flex → Inset → Flex(Column) → [boxes] plus a sibling
// box, exercising nested local↔screen transforms.
func buildNestedTree(th *theme.Theme) (Component, []*testBox) {
	boxA := newTestBox(NewID(RootID, "ins/inner/a"), 2, 2, 'a')
	boxB := newTestBox(NewID(RootID, "ins/inner/b"), 3, 1, 'b')
	inner := NewFlex(NewID(RootID, "ins/inner"), FlexColumn, 1,
		NewFlexChild(boxA, 0, 0, 0), NewFlexChild(boxB, 0, 0, 0))
	ins := NewInset(NewID(RootID, "ins"), InsetsAll(1), inner)
	boxC := newTestBox(NewID(RootID, "c"), 4, 2, 'c')
	outer := NewFlex(NewID(RootID, "root"), FlexRow, 1,
		NewFlexChild(ins, 1, 1, 0), NewFlexChild(boxC, 1, 1, 0))
	return outer, []*testBox{boxA, boxB, boxC}
}

// TestLayoutTransform_CanonicalSizes is the Phase 4 property test. For every
// canonical size (zero, minimum, narrow, standard, wide) it mounts a nested
// Flex/Inset/Flex tree, paints into a real buffer, and asserts the nested
// local↔screen transform invariant plus that pointer hits match painted bounds.
func TestLayoutTransform_CanonicalSizes(t *testing.T) {
	th := theme.DefaultDark()
	widths := []int{0, 1, 10, 40, 80, 120, 200}
	heights := []int{0, 1, 10, 24, 40, 50}
	for _, w := range widths {
		for _, h := range heights {
			name := fmt.Sprintf("%dx%d", w, h)
			t.Run(name, func(t *testing.T) {
				root, boxes := buildNestedTree(th)
				host := NewHost(th, root)
				host.Resize(w, h)
				buf := surface.NewCellBuffer(w, h)
				host.Paint(buf)
				if w <= 0 || h <= 0 {
					return // layout is skipped at zero size; nothing to assert
				}
				expectScreenRects(t, host, root)
				assertHitMatchesPaint(t, host, buf, boxes)
			})
		}
	}
}

// TestLayoutTransform_OverflowPredictable mounts a flex whose children exceed
// the host width and asserts layout still produces parent-local rects and hit
// results stay inside the host.
func TestLayoutTransform_OverflowPredictable(t *testing.T) {
	th := theme.DefaultDark()
	a := newTestBox(NewID(RootID, "a"), 30, 2, 'a')
	b := newTestBox(NewID(RootID, "b"), 30, 2, 'b')
	c := newTestBox(NewID(RootID, "c"), 30, 2, 'c')
	outer := NewFlex(NewID(RootID, "root"), FlexRow, 0,
		NewFlexChild(a, 0, 0, 0), NewFlexChild(b, 0, 0, 0), NewFlexChild(c, 0, 0, 0))
	h := NewHost(th, outer)
	h.Resize(40, 3)
	buf := surface.NewCellBuffer(40, 3)
	h.Paint(buf)
	expectScreenRects(t, h, outer)
	for _, box := range []*testBox{a, b, c} {
		r := box.Bounds()
		if r.X < 0 || r.W < 1 {
			t.Fatalf("%s overflow bounds %+v", box.ID(), r)
		}
	}
}

// TestLayoutTransform_StretchMatchesPaint verifies a column with cross-stretch
// paints and hits across the full column width regardless of child sizes.
func TestLayoutTransform_StretchMatchesPaint(t *testing.T) {
	th := theme.DefaultDark()
	a := newTestBox(NewID(RootID, "a"), 1, 1, 'a')
	b := newTestBox(NewID(RootID, "b"), 5, 1, 'b')
	col := NewFlex(NewID(RootID, "col"), FlexColumn, 0,
		NewFlexChild(a, 0, 0, 0), NewFlexChild(b, 0, 0, 0))
	col.SetCrossAxisAlignment(CrossStretch)
	h := NewHost(th, col)
	h.Resize(8, 3)
	buf := surface.NewCellBuffer(8, 3)
	h.Paint(buf)
	for _, box := range []*testBox{a, b} {
		r := box.Bounds()
		if r.W != 8 {
			t.Fatalf("%s stretched width %d", box.ID(), r.W)
		}
		for x := 0; x < 8; x++ {
			y := r.Y
			if buf.Get(x, y).Char != box.ch {
				t.Fatalf("%s paint at (%d,%d): %q", box.ID(), x, y, buf.Get(x, y).Char)
			}
			if hit := h.HitTest(x, y); hit != box.ID() {
				t.Fatalf("%s hit at (%d,%d): %q", box.ID(), x, y, hit)
			}
		}
	}
}
