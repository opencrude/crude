package ui

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"crude/tui/surface"
	"crude/tui/theme"
)

// layout_constraint_fuzz_test.go: Phase 4 "layout constraint solver fuzz".
// Native fuzz targets drive the Measure -> Layout -> collectScreen ->
// paintNode -> HitTest pipeline with random constraints and assert solver
// invariants on the Host's screen-absolute geometry (not just local bounds).
// The testBox leaf used by every target is shared with
// layout_transform_property_test.go.

// FuzzFlexConstraints exercises the Flex solver over random sizes,
// alignments, wrap modes, and per-child grow/shrink/basis factors derived
// deterministically from childSeed.
func FuzzFlexConstraints(f *testing.F) {
	f.Add(40, 10, 3, 1, 3, 3, 0, 7)
	f.Add(1, 1, 0, 0, 0, 0, 0, 0)
	f.Add(200, 50, 8, 4, 5, 2, 1, 99)
	f.Add(0, 0, 4, 2, 1, 0, 1, 42)
	f.Add(120, 30, 6, 0, 4, 3, 0, 1)
	f.Add(10, 5, 8, 2, 1, 1, 1, 123)

	f.Fuzz(func(t *testing.T, width, height, nChildren, gap, mainAlign, crossAlign, wrap, childSeed int) {
		width = normMod(width, 201)
		height = normMod(height, 51)
		nChildren = normMod(nChildren, 9)
		gap = normMod(gap, 5)
		mainAlign = normMod(mainAlign, 6)
		crossAlign = normMod(crossAlign, 4)
		wrap = normMod(wrap, 2)
		seed := normMod(childSeed, 1<<30)

		rng := rand.New(rand.NewPCG(uint64(seed), 0x9E3779B97F4A7C15))
		root := NewFlex("root", FlexDirection(seed%2), gap)
		root.SetMainAxisAlignment(MainAxisAlignment(mainAlign))
		root.SetCrossAxisAlignment(CrossAxisAlignment(crossAlign))
		root.SetWrap(WrapMode(wrap))
		for i := 0; i < nChildren; i++ {
			root.AddChild(NewFlexChild(
				newTestBox(NewID("root", fmt.Sprintf("b%d", i)), 1+rng.IntN(15), 1+rng.IntN(6), rune('a'+i%26)),
				rng.IntN(4), rng.IntN(4), rng.IntN(21)))
		}

		host := NewHost(theme.DefaultDark(), root)
		host.Resize(width, height)
		assertLayoutInvariants(t, host, fuzzProbes(width, height, normMod(childSeed, 201), normMod(childSeed, 51)))
	})
}

// FuzzGridConstraints exercises the Grid solver over random column counts,
// gap sizes, and per-axis track rules (Auto/Fixed/Fraction).
func FuzzGridConstraints(f *testing.F) {
	f.Add(40, 10, 3, 6, 1, 0, 5)
	f.Add(1, 1, 1, 0, 0, 2, 0)
	f.Add(120, 30, 6, 24, 3, 1, 33)
	f.Add(0, 0, 4, 5, 2, 2, 77)
	f.Add(200, 50, 2, 17, 2, 0, 201)
	f.Add(15, 8, 5, 20, 1, 1, 987654321)

	f.Fuzz(func(t *testing.T, width, height, cols, nChildren, gap, trackKind, trackSeed int) {
		width = normMod(width, 201)
		height = normMod(height, 51)
		cols = normMod(cols, 6) + 1
		nChildren = normMod(nChildren, 25)
		gap = normMod(gap, 4)
		trackKind = normMod(trackKind, 3)
		seed := normMod(trackSeed, 1<<30)

		rng := rand.New(rand.NewPCG(uint64(seed), 0x2545F4914F6CDD1D))
		rows := (nChildren + cols - 1) / cols
		root := NewGrid("root", cols, gap)
		colSizes := make([]GridTrack, 0, cols)
		for c := 0; c < cols; c++ {
			colSizes = append(colSizes, fuzzTrack(rng, trackKind))
		}
		root.SetColSizes(colSizes)
		rowSizes := make([]GridTrack, 0, rows)
		for r := 0; r < rows; r++ {
			rowSizes = append(rowSizes, fuzzTrack(rng, trackKind))
		}
		root.SetRowSizes(rowSizes)
		for i := 0; i < nChildren; i++ {
			root.Add(newTestBox(NewID("root", fmt.Sprintf("c%d", i)), 1+rng.IntN(15), 1+rng.IntN(6), rune('a'+i%26)))
		}

		host := NewHost(theme.DefaultDark(), root)
		host.Resize(width, height)
		assertLayoutInvariants(t, host, fuzzProbes(width, height, normMod(trackSeed, 201), normMod(trackSeed, 51)))
	})
}

// FuzzNestedLayoutTree builds a random tree of all five new containers plus
// Stack at depth 1..4 from structureSeed and asserts the full pipeline on the
// screen-absolute geometry, including paint clipping of nested viewports.
func FuzzNestedLayoutTree(f *testing.F) {
	f.Add(40, 24, 3, 0, 20, 12)
	f.Add(80, 30, 4, 1, 79, 29)
	f.Add(1, 1, 2, 2, 0, 0)
	f.Add(200, 50, 4, 3, 199, 49)
	f.Add(0, 0, 1, 4, 0, 0)
	f.Add(15, 10, 2, 5, 14, 9)
	f.Add(25, 8, 4, 6, 3, 7)

	f.Fuzz(func(t *testing.T, width, height, depth, structureSeed, x, y int) {
		width = normMod(width, 201)
		height = normMod(height, 51)
		depth = normMod(depth, 4) + 1
		seed := normMod(structureSeed, 1<<30)
		x = normMod(x, 201)
		y = normMod(y, 51)

		b := &nestedBuilder{rng: rand.New(rand.NewPCG(uint64(seed), 0x6A09E667F3BCC909))}
		root := b.build(depth)
		host := NewHost(theme.DefaultDark(), root)
		host.Resize(width, height)
		buf := assertLayoutInvariants(t, host, fuzzProbes(width, height, x, y))
		assertNestedViewportClip(t, host, buf, b.viewports)
	})
}

// FuzzViewportScrollClip exercises the Viewport's scroll offset and paint
// clipping: no child cell may paint outside the viewport rect, and the
// visible cells must match the expected scrolled sub-rect of the content.
func FuzzViewportScrollClip(f *testing.F) {
	f.Add(40, 10, 100, 50, 20, 10, 5, 5)
	f.Add(1, 1, 1, 1, 0, 0, 0, 0)
	f.Add(80, 30, 400, 200, 399, 199, 79, 29)
	f.Add(0, 0, 50, 30, 25, 15, 1, 1)
	f.Add(10, 5, 200, 100, 195, 97, 9, 4)

	f.Fuzz(func(t *testing.T, width, height, contentW, contentH, scrollX, scrollY, hitX, hitY int) {
		width = normMod(width, 81)
		height = normMod(height, 31)
		contentW = normMod(contentW, 400) + 1
		contentH = normMod(contentH, 200) + 1
		scrollX = normMod(scrollX, 401)
		scrollY = normMod(scrollY, 201)
		hitX = normMod(hitX, 401)
		hitY = normMod(hitY, 201)

		box := newTestBox(NewID("vp", "content"), contentW, contentH, 'V')
		vp := NewViewport("vp", box)
		vp.SetContentSize(contentW, contentH)
		vp.SetScrollOffset(scrollX, scrollY)

		host := NewHost(theme.DefaultDark(), vp)
		host.Resize(width, height)
		buf := assertLayoutInvariants(t, host, fuzzProbes(width, height, hitX, hitY))
		assertViewportClip(t, host, buf, box)
	})
}

// normMod reduces n into [0, m) without overflowing for MinInt.
func normMod(n, m int) int {
	n %= m
	if n < 0 {
		n += m
	}
	return n
}

// fuzzProbes returns the fuzz hit point plus the root corners and center.
func fuzzProbes(w, h, x, y int) [][2]int {
	p := [][2]int{{x, y}}
	if w > 0 && h > 0 {
		p = append(p, [2]int{0, 0})
		p = append(p, [2]int{w - 1, 0})
		p = append(p, [2]int{0, h - 1})
		p = append(p, [2]int{w - 1, h - 1})
		p = append(p, [2]int{w / 2, h / 2})
	}
	return p
}

func fuzzTrack(rng *rand.Rand, kind int) GridTrack {
	switch kind {
	case 1:
		return FixedGridTrack(1 + rng.IntN(30))
	case 2:
		return FractionGridTrack(1 + rng.IntN(5))
	default:
		return AutoGridTrack()
	}
}

func cloneScreen(m map[ComponentID]Rect) map[ComponentID]Rect {
	out := make(map[ComponentID]Rect, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func screenEq(a, b map[ComponentID]Rect) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// assertLayoutInvariants runs the full pipeline (Measure, Layout, Paint,
// forced relayout, HitTest) and asserts every solver invariant. Returns the
// painted buffer so callers can run container-specific paint checks.
func assertLayoutInvariants(t *testing.T, host *Host, probes [][2]int) *surface.CellBuffer {
	t.Helper()
	root := host.root
	if root == nil {
		t.Fatal("layout fuzz: nil root")
	}
	w, h := host.width, host.height
	if w <= 0 || h <= 0 {
		for _, p := range probes {
			if id := host.HitTest(p[0], p[1]); id != RootID {
				t.Fatalf("degenerate HitTest(%d,%d) = %q, want RootID", p[0], p[1], id)
			}
		}
		return nil
	}

	lctx := LayoutContext{Theme: host.theme}
	cs := Loose(w, h)
	mBefore := root.Measure(lctx, cs)

	host.Layout()
	before := cloneScreen(host.screen)
	buf := surface.NewCellBuffer(w, h)
	host.Paint(buf)

	// Invariant 7a: Measure must be pure w.r.t. layout/paint side effects.
	// A solver whose Layout or Paint mutated state that Measure reads would
	// diverge here.
	mAfter := root.Measure(lctx, cs)
	if mAfter != mBefore {
		t.Fatalf("Measure not pure across layout/paint: %+v -> %+v", mBefore, mAfter)
	}
	if mAfter.W < 0 || mAfter.H < 0 {
		t.Fatalf("Measure returned negative size %+v", mAfter)
	}

	// Invariant 6: painting must not mutate layout state. Force a relayout
	// and require the screen map to be identical to the pre-paint snapshot.
	host.MarkLayout()
	host.Layout()
	if !screenEq(host.screen, before) {
		t.Fatalf("screen map changed after repaint; paint mutated layout state")
	}

	// Invariant 7b: Measure/Layout agreement. Under Tight constraints
	// ClampSize always yields the tight size, so this is a consistency guard;
	// the meaningful agreement is Measure purity checked above.
	rootR := root.Bounds()
	tight := Tight(w, h).ClampSize(mAfter)
	if tight.W != rootR.W || tight.H != rootR.H {
		t.Fatalf("Measure/Layout disagreement: Tight(%d,%d).ClampSize(%+v)=%+v vs bounds %+v", w, h, mAfter, tight, rootR)
	}

	// Invariant 2: no negative rects.
	for id, c := range host.registry {
		if id == RootID {
			continue
		}
		b := c.Bounds()
		if b.W < 0 || b.H < 0 {
			t.Fatalf("%s negative bounds %+v", id, b)
		}
	}

	// Invariant 3: children within parent. The origin side must always hold.
	// The far edge holds only when the parent's content collectively fits:
	// containers may overflow the far edge by design (Flex without shrink on
	// overflow, Grid with oversized fixed/auto tracks, an Inset larger than
	// its child, and WrapLine stacking more lines than fit). Viewport children
	// are exempt entirely: they are unregistered (Viewport is a leaf) and are
	// scrolled out by intent. epsilon is 1 cell for SpaceBetween/SpaceAround
	// rounding.
	childrenOf := map[ComponentID][]ComponentID{}
	for id := range host.registry {
		if id == RootID {
			continue
		}
		if p, ok := host.parent[id]; ok {
			childrenOf[p] = append(childrenOf[p], id)
		}
	}
	const eps = 1
	for pID, kids := range childrenOf {
		pb := host.registry[pID].Bounds()
		maxX, maxY := 0, 0
		for _, kid := range kids {
			kb := host.registry[kid].Bounds()
			if kb.X < 0 || kb.Y < 0 {
				t.Fatalf("%s local bounds %+v before parent %s origin (parent %+v)", kid, kb, pID, pb)
			}
			if kb.X+kb.W > maxX {
				maxX = kb.X + kb.W
			}
			if kb.Y+kb.H > maxY {
				maxY = kb.Y + kb.H
			}
		}
		if maxX <= pb.W+eps && maxY <= pb.H+eps {
			for _, kid := range kids {
				kb := host.registry[kid].Bounds()
				if kb.X+kb.W > pb.W+eps || kb.Y+kb.H > pb.H+eps {
					t.Fatalf("%s local bounds %+v exceed parent %s %+v", kid, kb, pID, pb)
				}
			}
		}
	}

	// Invariant 4: screen-rect consistency (parent-origin + child-bounds).
	expectScreenRects(t, host, root)

	// Invariant 5: HitTest containment. Inside the root the result must be a
	// registered component whose screen rect contains the point; outside it
	// must be RootID.
	for _, probe := range probes {
		x, y := probe[0], probe[1]
		id := host.HitTest(x, y)
		if !rootR.Contains(x, y) {
			if id != RootID {
				t.Fatalf("HitTest(%d,%d) outside root %v = %q, want RootID", x, y, rootR, id)
			}
			continue
		}
		if id == RootID {
			t.Fatalf("HitTest(%d,%d) inside root %v returned RootID", x, y, rootR)
		}
		if _, ok := host.registry[id]; !ok {
			t.Fatalf("HitTest(%d,%d) = %q not registered", x, y, id)
		}
		r, ok := host.screen[id]
		if !ok {
			t.Fatalf("HitTest(%d,%d) = %q missing from screen map", x, y, id)
		}
		if !r.Contains(x, y) {
			t.Fatalf("HitTest(%d,%d) = %q bounds %v do not contain point", x, y, id, r)
		}
	}
	return buf
}

// viewportChild records a nested viewport whose direct child is a testBox so
// paint clipping and the scrolled sub-rect can be checked against it.
type viewportChild struct {
	vpID ComponentID
	box  *testBox
}

// assertNestedViewportClip verifies each recorded nested viewport clips its
// child to the viewport's screen rect and that the visible cells match the
// expected scrolled content sub-rect. Leaf chars are globally unique across
// the tree, so a leak of a child char outside its viewport is a real failure.
// Containers may overflow by design, so a later sibling may legitimately paint
// over a viewport cell; that is tolerated as long as the cell is still painted
// (never blank) and the viewport's own char never appears out of place.
func assertNestedViewportClip(t *testing.T, host *Host, buf *surface.CellBuffer, vps []viewportChild) {
	t.Helper()
	if buf == nil {
		return
	}
	for _, v := range vps {
		r, ok := host.screen[v.vpID]
		if !ok || r.Empty() {
			continue
		}
		cb := v.box.Bounds()
		if cb.W < 0 || cb.H < 0 {
			t.Fatalf("viewport %s child negative bounds %+v", v.vpID, cb)
		}
		for by := 0; by < buf.Height(); by++ {
			for bx := 0; bx < buf.Width(); bx++ {
				got := buf.Get(bx, by).Char
				if got == v.box.ch {
					if !r.Contains(bx, by) {
						t.Fatalf("viewport %s child %q leaked outside %v at (%d,%d)", v.vpID, v.box.ch, r, bx, by)
					}
					cx := bx - r.X - cb.X
					cy := by - r.Y - cb.Y
					if cx < 0 || cx >= cb.W || cy < 0 || cy >= cb.H {
						t.Fatalf("viewport %s child %q painted outside scrolled content at (%d,%d) (cx=%d,cy=%d)", v.vpID, v.box.ch, bx, by, cx, cy)
					}
					continue
				}
				if !r.Contains(bx, by) {
					continue
				}
				cx := bx - r.X - cb.X
				cy := by - r.Y - cb.Y
				if cx < 0 || cx >= cb.W || cy < 0 || cy >= cb.H {
					continue
				}
				if got == 0 {
					t.Fatalf("viewport %s failed to paint %q at (%d,%d) (cx=%d,cy=%d)", v.vpID, v.box.ch, bx, by, cx, cy)
				}
			}
		}
	}
}

// assertViewportClip runs the strict scroll-clip assertions for the root
// viewport, which has no siblings to overwrite it: every visible cell must
// match the expected scrolled sub-rect of the content exactly.
func assertViewportClip(t *testing.T, host *Host, buf *surface.CellBuffer, box *testBox) {
	t.Helper()
	if buf == nil {
		return
	}
	r := host.screen[host.root.ID()]
	if r.Empty() {
		return
	}
	cb := box.Bounds()
	if cb.W < 0 || cb.H < 0 {
		t.Fatalf("viewport child negative bounds %+v", cb)
	}
	for by := 0; by < buf.Height(); by++ {
		for bx := 0; bx < buf.Width(); bx++ {
			got := buf.Get(bx, by).Char
			cx := bx - r.X - cb.X
			cy := by - r.Y - cb.Y
			inContent := r.Contains(bx, by) && cx >= 0 && cx < cb.W && cy >= 0 && cy < cb.H
			switch {
			case got == box.ch && !inContent:
				t.Fatalf("child %q painted outside scrolled content at (%d,%d) (cx=%d,cy=%d)", box.ch, bx, by, cx, cy)
			case got != box.ch && inContent:
				t.Fatalf("viewport missing %q at (%d,%d) (cx=%d,cy=%d)", box.ch, bx, by, cx, cy)
			}
		}
	}
}

// nestedBuilder deterministically grows a random tree from a seeded PRNG.
// Every leaf gets a globally unique rune so paint-clip checks cannot collide.
type nestedBuilder struct {
	rng       *rand.Rand
	n         int
	viewports []viewportChild
}

func (b *nestedBuilder) build(depth int) Component {
	b.n++
	id := NewID("root", fmt.Sprintf("n%d", b.n))
	if depth == 0 {
		return newTestBox(id, 1+b.rng.IntN(15), 1+b.rng.IntN(6), rune(0x10000+b.n))
	}
	switch b.rng.IntN(6) {
	case 0:
		f := NewFlex(id, FlexDirection(b.rng.IntN(2)), b.rng.IntN(4))
		f.SetMainAxisAlignment(MainAxisAlignment(b.rng.IntN(6)))
		f.SetCrossAxisAlignment(CrossAxisAlignment(b.rng.IntN(4)))
		f.SetWrap(WrapMode(b.rng.IntN(2)))
		for i := 0; i < 1+b.rng.IntN(4); i++ {
			f.AddChild(NewFlexChild(b.build(depth-1), b.rng.IntN(4), b.rng.IntN(4), b.rng.IntN(21)))
		}
		return f
	case 1:
		cols := 1 + b.rng.IntN(4)
		g := NewGrid(id, cols, b.rng.IntN(3))
		colSizes := make([]GridTrack, 0, cols)
		for c := 0; c < cols; c++ {
			colSizes = append(colSizes, b.randomTrack())
		}
		g.SetColSizes(colSizes)
		rowSizes := make([]GridTrack, 0, 1+b.rng.IntN(3))
		for r := 0; r < 1+b.rng.IntN(3); r++ {
			rowSizes = append(rowSizes, b.randomTrack())
		}
		g.SetRowSizes(rowSizes)
		for i := 0; i < 1+b.rng.IntN(4); i++ {
			g.Add(b.build(depth - 1))
		}
		return g
	case 2:
		return NewInset(id, InsetsFromSides(b.rng.IntN(4), b.rng.IntN(4), b.rng.IntN(4), b.rng.IntN(4)), b.build(depth-1))
	case 3:
		s := NewSplitPane(id, Direction(b.rng.IntN(2)), b.build(depth-1), b.build(depth-1))
		s.SetFraction(float64(b.rng.IntN(11)) / 10)
		if b.rng.IntN(2) == 0 {
			s.SetDividerPosition(b.rng.IntN(40))
		}
		return s
	case 4:
		st := NewStack(id, Direction(b.rng.IntN(2)), b.rng.IntN(3))
		for i := 0; i < 1+b.rng.IntN(4); i++ {
			st.Add(b.build(depth - 1))
		}
		return st
	default:
		child := b.build(depth - 1)
		vp := NewViewport(id, child)
		vp.SetContentSize(1+b.rng.IntN(60), 1+b.rng.IntN(30))
		vp.SetScrollOffset(b.rng.IntN(40), b.rng.IntN(30))
		if box, ok := child.(*testBox); ok {
			b.viewports = append(b.viewports, viewportChild{vpID: id, box: box})
		}
		return vp
	}
}

func (b *nestedBuilder) randomTrack() GridTrack {
	switch b.rng.IntN(3) {
	case 0:
		return AutoGridTrack()
	case 1:
		return FixedGridTrack(1 + b.rng.IntN(20))
	default:
		return FractionGridTrack(1 + b.rng.IntN(4))
	}
}
