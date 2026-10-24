package ui

import (
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
)

func TestHostNestedSubtreeDamage(t *testing.T) {
	for _, scenario := range []string{"isolated", "overlapping-sibling", "ancestor-overlap", "overflow", "empty", "ancestor-and-child"} {
		t.Run(scenario, func(t *testing.T) {
			a := &regionTestLeaf{Base: NewBase("nested/a"), text: "wide 界"}
			b := &regionTestLeaf{Base: NewBase("nested/b"), text: "sibling"}
			nested := &regionTestRoot{Base: NewBase("nested"), children: []Component{a, b}, rects: []Rect{{X: 2, Y: 1, W: 12, H: 2}, {X: 16, Y: 1, W: 12, H: 2}}}
			root := &regionTestRoot{Base: NewBase("root"), children: []Component{nested}, rects: []Rect{{X: 3, Y: 2, W: 30, H: 5}}}
			switch scenario {
			case "overlapping-sibling":
				nested.rects[1].X = 8
			case "ancestor-overlap":
				cover := &regionTestLeaf{Base: NewBase("cover"), text: "overlaid"}
				root.children = append(root.children, cover)
				root.rects = append(root.rects, Rect{X: 5, Y: 3, W: 12, H: 2})
			case "overflow":
				nested.rects[0].X = 24
			case "empty":
				nested.rects[0] = Rect{}
				a.text = ""
			}
			h := NewHost(theme.DefaultDark(), root)
			h.Resize(40, 10)
			buf := surface.NewCellBuffer(40, 10)
			bg := cell.BlankCell(cell.NewPaletteColor(0))
			h.PaintRegions(buf, bg)
			before := b.paints
			if scenario != "empty" {
				a.text = "x"
			}
			h.MarkSubtreePaint(a.ID())
			if scenario == "ancestor-and-child" {
				h.MarkComponentPaint(nested.ID())
			}
			h.RefreshLayout()
			h.PaintRegions(buf, bg)
			stats := h.LastPaintStats()
			if scenario == "isolated" && (stats.Full || b.paints != before || stats.Cells != 24 || stats.Components != 1) {
				t.Fatalf("nested source repainted sibling: %+v", stats)
			}
			if (scenario == "overlapping-sibling" || scenario == "ancestor-overlap" || scenario == "overflow") && !stats.Full {
				t.Fatalf("unsafe nested damage did not fall back: %+v", stats)
			}
			if scenario == "ancestor-and-child" && stats.Components != 3 {
				t.Fatalf("descendant damage painted twice: %+v", stats)
			}
			assertRegionFullReference(t, h, buf, bg)
		})
	}
}

func TestHostNestedSubtreeRejectsOverflowingDescendant(t *testing.T) {
	leaf := &regionTestLeaf{Base: NewBase("leaf"), text: "overflow"}
	nested := &regionTestRoot{Base: NewBase("nested"), children: []Component{leaf}, rects: []Rect{{X: 6, Y: 1, W: 12, H: 2}}}
	root := &regionTestRoot{Base: NewBase("root"), children: []Component{nested}, rects: []Rect{{X: 2, Y: 1, W: 10, H: 4}}}
	h := NewHost(theme.DefaultDark(), root)
	h.Resize(30, 8)
	buf := surface.NewCellBuffer(30, 8)
	bg := cell.BlankCell(cell.NewPaletteColor(0))
	h.PaintRegions(buf, bg)
	leaf.text = "short"
	h.MarkSubtreePaint(nested.ID())
	h.PaintRegions(buf, bg)
	if !h.LastPaintStats().Full {
		t.Fatal("overflowing descendant needs complete traversal")
	}
	assertRegionFullReference(t, h, buf, bg)
}
