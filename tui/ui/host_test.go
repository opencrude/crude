package ui

import (
	"math/rand"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
)

type regionTestRoot struct {
	Base
	children []Component
	rects    []Rect
}

func (r *regionTestRoot) Children() []Component { return r.children }
func (r *regionTestRoot) Layout(ctx LayoutContext, bounds Rect) {
	r.SetBounds(bounds)
	for i, c := range r.children {
		c.Layout(ctx, r.rects[i])
	}
}

type regionTestLeaf struct {
	Base
	text                  string
	paints, cancellations int
}

func (c *regionTestLeaf) Paint(buf *surface.CellBuffer, ox, oy int, ctx PaintContext) {
	c.paints++
	r := c.Bounds()
	bg := cell.NewPaletteColor(0)
	if ctx.Focused {
		bg = cell.NewPaletteColor(2)
	}
	if ctx.Hovered {
		bg = cell.NewPaletteColor(3)
	}
	buf.WriteString(ox+r.X, oy+r.Y, c.text, cell.NewPaletteColor(7), bg, 0)
}
func (c *regionTestLeaf) Handle(EventContext, Event) EventResult {
	c.text = "changed"
	return EventResult{Handled: true, Invalidate: InvalidatePaint}
}
func (c *regionTestLeaf) CancelPointerCapture() { c.cancellations++ }

func regionHostFixture() (*Host, *regionTestRoot, *regionTestLeaf, *regionTestLeaf, *surface.CellBuffer) {
	a := &regionTestLeaf{Base: NewBase("a"), text: "wide 界👨‍👩‍👧‍👦"}
	b := &regionTestLeaf{Base: NewBase("b"), text: "sibling"}
	r := &regionTestRoot{Base: NewBase("root"), children: []Component{a, b}, rects: []Rect{{X: 1, Y: 1, W: 18, H: 3}, {X: 22, Y: 1, W: 16, H: 3}}}
	h := NewHost(theme.DefaultDark(), r)
	h.Resize(40, 8)
	return h, r, a, b, surface.NewCellBuffer(40, 8)
}

func BenchmarkHostStableLayout(b *testing.B) {
	h, _, _, _, _ := regionHostFixture()
	h.Layout()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		h.RefreshLayout()
		h.Layout()
	}
}

func TestHostStableLayoutDoesNotAllocateRegistryMaps(t *testing.T) {
	h, _, _, _, _ := regionHostFixture()
	h.Layout()
	allocs := testing.AllocsPerRun(20, func() {
		h.RefreshLayout()
		h.Layout()
	})
	if allocs > 1 {
		t.Fatalf("unchanged geometry allocates registry maps: %v", allocs)
	}
}

func assertRegionFullReference(t *testing.T, h *Host, buf *surface.CellBuffer, bg cell.Cell) {
	t.Helper()
	// A separate host starts with no retained damage or cells. Its unconditional
	// traversal is the oracle, not the partial painter or its selected regions.
	reference := NewHost(h.Theme(), h.Root())
	reference.Resize(buf.Width(), buf.Height())
	reference.Layout()
	reference.Focus(h.Focused())
	reference.SetHovered(h.Hovered())
	reference.SetFrame(h.frame)
	want := surface.NewCellBuffer(buf.Width(), buf.Height())
	want.Fill(bg)
	reference.Paint(want)
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			if got, expected := buf.Get(x, y), want.Get(x, y); got != expected {
				t.Fatalf("cell (%d,%d) got %+v want %+v", x, y, got, expected)
			}
		}
	}
}

func TestHostRegionPaintRetainsUnchangedSiblingAndClearsOldInk(t *testing.T) {
	h, _, a, b, buf := regionHostFixture()
	bg := cell.BlankCell(cell.NewPaletteColor(0))
	h.PaintRegions(buf, bg)
	a.text = "short"
	h.MarkComponentPaint(a.ID())
	before := b.paints
	h.RefreshLayout()
	h.PaintRegions(buf, bg)
	if b.paints != before || h.LastPaintStats().Full || h.LastPaintStats().Components != 1 {
		t.Fatalf("unchanged sibling painted: %+v", h.LastPaintStats())
	}
	assertRegionFullReference(t, h, buf, bg)
	h.PaintRegions(buf, bg)
	if h.LastPaintStats().Components != 0 {
		t.Fatal("presentation invalidated source")
	}
	h.Focus(b.ID())
	h.SetHovered(a.ID())
	h.PaintRegions(buf, bg)
	assertRegionFullReference(t, h, buf, bg)
}

func TestHostRegionPaintRandomFullEquivalence(t *testing.T) {
	h, root, a, b, buf := regionHostFixture()
	bg := cell.BlankCell(cell.NewPaletteColor(0))
	rng := rand.New(rand.NewSource(417))
	for i := 0; i < 300; i++ {
		switch rng.Intn(9) {
		case 0:
			a.text = []string{"", "x", "界é", "changed source"}[rng.Intn(4)]
			h.MarkComponentPaint(a.ID())
		case 1:
			h.Focus([]ComponentID{a.ID(), b.ID(), RootID}[rng.Intn(3)])
		case 2:
			h.SetHovered([]ComponentID{a.ID(), b.ID(), RootID}[rng.Intn(3)])
		case 3:
			root.rects[0].Y = 1 + rng.Intn(4)
			h.RefreshLayout()
		case 4:
			if len(root.children) == 2 {
				root.children = root.children[:1]
			} else {
				root.children = append(root.children, b)
			}
			h.RefreshLayout()
		case 5:
			h.SetTheme(theme.DefaultDark())
		case 6:
			w := 40 + rng.Intn(8)
			buf.Resize(w, 8)
			h.Resize(w, 8)
		case 7:
			h.Handle(tea.MouseClickMsg{X: 2, Y: root.rects[0].Y, Button: tea.MouseLeft})
		case 8:
			h.CancelPointerCapture()
		}
		h.PaintRegions(buf, bg)
		assertRegionFullReference(t, h, buf, bg)
	}
}

func TestHostRegionUnmountCancelsCapture(t *testing.T) {
	h, root, a, _, buf := regionHostFixture()
	bg := cell.BlankCell(cell.NewPaletteColor(0))
	h.PaintRegions(buf, bg)
	h.CapturePointer(a.ID(), tea.MouseLeft)
	root.children = nil
	h.RefreshLayout()
	h.PaintRegions(buf, bg)
	if h.HasPointerCapture() || a.cancellations != 1 {
		t.Fatal("unmount retained capture")
	}
	assertRegionFullReference(t, h, buf, bg)
}
