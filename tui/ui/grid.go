package ui

import (
	"crude/tui/surface"
)

// GridTrackKind selects how a Grid track size is computed.
type GridTrackKind uint8

const (
	GridTrackAuto GridTrackKind = iota
	GridTrackFixed
	GridTrackFraction
)

// GridTrack is the sizing rule for one grid column or row.
type GridTrack struct {
	Kind GridTrackKind
	Size int // cells for Fixed, flex weight for Fraction, 0 for Auto
}

// FixedGridTrack returns a track of a fixed cell size.
func FixedGridTrack(size int) GridTrack {
	return GridTrack{Kind: GridTrackFixed, Size: size}
}

// AutoGridTrack returns a track sized to its widest/tallest child.
func AutoGridTrack() GridTrack {
	return GridTrack{Kind: GridTrackAuto}
}

// FractionGridTrack returns a track that shares the remaining space by weight.
func FractionGridTrack(weight int) GridTrack {
	return GridTrack{Kind: GridTrackFraction, Size: weight}
}

// Grid is a 2D cell layout with a fixed column count and rows that flow in
// child order. Tracks default to auto (widest/tallest child in the track);
// fixed and fractional tracks can be set per axis via SetColSizes/SetRowSizes.
type Grid struct {
	Base
	cols     int
	gap      int
	children []Component
	colSizes []GridTrack
	rowSizes []GridTrack
}

// NewGrid creates a grid with cols columns and a uniform gap in cells.
func NewGrid(id ComponentID, cols, gap int, children ...Component) *Grid {
	if cols < 1 {
		cols = 1
	}
	return &Grid{Base: NewBase(id), cols: cols, gap: gap, children: children}
}

// SetColSizes sets column track rules (cycles if shorter than the column count).
func (g *Grid) SetColSizes(tracks []GridTrack) { g.colSizes = tracks }

// SetRowSizes sets row track rules (cycles if shorter than the row count).
func (g *Grid) SetRowSizes(tracks []GridTrack) { g.rowSizes = tracks }

// Children implements Parent.
func (g *Grid) Children() []Component { return g.children }

// Add appends a child.
func (g *Grid) Add(c Component) { g.children = append(g.children, c) }

// Measure computes track sizes and sums rows+gaps for height, cols+gaps for
// width, clamped to constraints.
func (g *Grid) Measure(ctx LayoutContext, cs Constraints) Size {
	cols := g.cols
	n := len(g.children)
	if n == 0 {
		return cs.ClampSize(Size{})
	}
	rows := (n + cols - 1) / cols
	sizes := g.measureChildren(ctx, Loose(cs.MaxW, cs.MaxH))
	widths := g.trackSizes(ctx, cols, g.colSizes, cs.MaxW, sizes, true)
	heights := g.trackSizes(ctx, rows, g.rowSizes, cs.MaxH, sizes, false)
	totalW := sumInts(widths) + g.gap*(cols-1)
	totalH := sumInts(heights) + g.gap*(rows-1)
	return cs.ClampSize(Size{W: totalW, H: totalH})
}

// Layout assigns each child a parent-local cell rect in row-major order.
func (g *Grid) Layout(ctx LayoutContext, r Rect) {
	g.SetBounds(r)
	cols := g.cols
	n := len(g.children)
	if n == 0 {
		return
	}
	rows := (n + cols - 1) / cols
	sizes := g.measureChildren(ctx, Loose(r.W, r.H))
	widths := g.trackSizes(ctx, cols, g.colSizes, r.W, sizes, true)
	heights := g.trackSizes(ctx, rows, g.rowSizes, r.H, sizes, false)

	colX := make([]int, cols)
	rowY := make([]int, rows)
	cx := 0
	for c := 0; c < cols; c++ {
		colX[c] = cx
		cx += widths[c] + g.gap
	}
	cy := 0
	for rw := 0; rw < rows; rw++ {
		rowY[rw] = cy
		cy += heights[rw] + g.gap
	}
	for i := 0; i < n; i++ {
		c, rw := i%cols, i/cols
		g.children[i].Layout(ctx, Rect{X: colX[c], Y: rowY[rw], W: widths[c], H: heights[rw]})
	}
}

func (g *Grid) measureChildren(ctx LayoutContext, cs Constraints) []Size {
	sizes := make([]Size, len(g.children))
	for i, c := range g.children {
		sizes[i] = c.Measure(ctx, cs)
	}
	return sizes
}

// trackSizes computes sizes for count tracks within avail main extent.
// horizontal selects whether a track measures children by width (columns).
func (g *Grid) trackSizes(ctx LayoutContext, count int, rules []GridTrack, avail int, sizes []Size, horizontal bool) []int {
	out := make([]int, count)
	consumed := 0
	totalFrac := 0
	fracWeight := make([]int, count)
	for t := 0; t < count; t++ {
		rule := trackAt(rules, t)
		switch rule.Kind {
		case GridTrackFixed:
			out[t] = rule.Size
			consumed += rule.Size
		case GridTrackFraction:
			w := max(1, rule.Size)
			fracWeight[t] = w
			totalFrac += w
		default:
			sz := 0
			for i := range sizes {
				if !g.childInTrack(i, t, horizontal) {
					continue
				}
				s := sizes[i]
				if horizontal {
					if s.W > sz {
						sz = s.W
					}
				} else if s.H > sz {
					sz = s.H
				}
			}
			out[t] = sz
			consumed += sz
		}
	}
	if totalFrac > 0 {
		remain := avail - consumed - g.gap*(count-1)
		if remain < 0 {
			remain = 0
		}
		for t := 0; t < count; t++ {
			if fracWeight[t] > 0 {
				out[t] = remain * fracWeight[t] / totalFrac
			}
		}
	}
	return out
}

// childInTrack reports whether child i belongs to track t. Columns are assigned
// by column index (i%cols), rows by row index (i/cols).
func (g *Grid) childInTrack(i, t int, horizontal bool) bool {
	if horizontal {
		return i%g.cols == t
	}
	return i/g.cols == t
}

func trackAt(rules []GridTrack, i int) GridTrack {
	if len(rules) == 0 {
		return GridTrack{Kind: GridTrackAuto}
	}
	return rules[i%len(rules)]
}

func sumInts(v []int) int {
	t := 0
	for _, x := range v {
		t += x
	}
	return t
}

// Paint is a no-op; children are painted by Host or PaintTree.
func (g *Grid) Paint(_ *surface.CellBuffer, _, _ int, _ PaintContext) {}

// Handle ignores; Host hit-testing routes to children.
func (g *Grid) Handle(_ EventContext, _ Event) EventResult {
	return EventResult{}
}

// Semantics reports a group role.
func (g *Grid) Semantics() Semantics {
	return Semantics{Role: RoleGroup, Label: string(g.ID())}
}
