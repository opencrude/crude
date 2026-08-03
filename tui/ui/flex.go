package ui

import (
	"crude/tui/surface"
)

// FlexDirection is the main axis of a Flex container.
type FlexDirection uint8

const (
	FlexRow FlexDirection = iota
	FlexColumn
)

// MainAxisAlignment packs children along the main axis when the flex has spare
// space. On overflow all alignments behave like MainStart.
type MainAxisAlignment uint8

const (
	MainStart MainAxisAlignment = iota
	MainCenter
	MainEnd
	MainSpaceBetween
	MainSpaceAround
	MainSpaceEvenly
)

// CrossAxisAlignment aligns children along the cross axis.
type CrossAxisAlignment uint8

const (
	CrossStart CrossAxisAlignment = iota
	CrossCenter
	CrossEnd
	CrossStretch
)

// WrapMode controls whether Flex children wrap onto new lines when they do not
// fit along the main axis.
type WrapMode uint8

const (
	WrapNone WrapMode = iota
	WrapLine
)

// FlexChild is one child of a Flex with its flex properties.
type FlexChild struct {
	Component  Component
	FlexGrow   int
	FlexShrink int
	FlexBasis  int
	CrossAlign *CrossAxisAlignment
}

// NewFlexChild builds a FlexChild. grow>0 lets the child absorb spare main-axis
// space proportionally to its factor; shrink>0 lets it give space up on
// overflow; basis>0 is a preferred main-axis size in cells (0 = auto, taken
// from Measure).
func NewFlexChild(c Component, grow, shrink, basis int) FlexChild {
	return FlexChild{Component: c, FlexGrow: grow, FlexShrink: shrink, FlexBasis: basis}
}

// SetCrossAlign overrides the Flex cross alignment for this child.
func (c *FlexChild) SetCrossAlign(a CrossAxisAlignment) *FlexChild {
	c.CrossAlign = &a
	return c
}

// Flex is a flexbox-style container. It lays out children along a main axis
// (row or column) and aligns them across the cross axis, distributing spare or
// overflowing space through flex-grow/flex-shrink factors.
//
// Algorithm (Flutter Row/Column + Expanded/Flexible reference):
//  1. Measure every child with loose constraints (flex-basis overrides the
//     measured main size).
//  2. Pack children into lines (one line unless WrapLine). If a child does not
//     fit the remaining main extent and the line is non-empty, start a new
//     line.
//  3. Per line, distribute free space to FlexGrow children proportionally to
//     their factors; on overflow, remove space from FlexShrink children
//     proportionally (each clamped at zero main size).
//  4. Stack lines along the cross axis with the flex gap.
//  5. Within each line, position children per MainAxisAlignment and each child
//     per CrossAxisAlignment (Stretch forces the cross size to the line extent).
//  6. Children receive parent-local rects; Paint and Handle are no-ops because
//     Host drives children directly.
type Flex struct {
	Base
	dir      FlexDirection
	main     MainAxisAlignment
	cross    CrossAxisAlignment
	wrap     WrapMode
	gap      int
	children []FlexChild
}

// NewFlex creates a flex container with row/column direction and a main-axis
// gap in cells.
func NewFlex(id ComponentID, dir FlexDirection, gap int, children ...FlexChild) *Flex {
	return &Flex{
		Base:     NewBase(id),
		dir:      dir,
		main:     MainStart,
		cross:    CrossStart,
		gap:      gap,
		children: children,
	}
}

// SetMainAxisAlignment sets the main-axis packing.
func (f *Flex) SetMainAxisAlignment(a MainAxisAlignment) { f.main = a }

// SetCrossAxisAlignment sets the cross-axis alignment.
func (f *Flex) SetCrossAxisAlignment(a CrossAxisAlignment) { f.cross = a }

// SetWrap enables line wrapping.
func (f *Flex) SetWrap(m WrapMode) { f.wrap = m }

// SetGap sets the main-axis gap and the gap between wrapped lines.
func (f *Flex) SetGap(g int) { f.gap = g }

// Children implements Parent.
func (f *Flex) Children() []Component {
	out := make([]Component, len(f.children))
	for i := range f.children {
		out[i] = f.children[i].Component
	}
	return out
}

// Add appends a non-flex child.
func (f *Flex) Add(c Component) {
	f.children = append(f.children, NewFlexChild(c, 0, 0, 0))
}

// AddChild appends a flex child.
func (f *Flex) AddChild(fc FlexChild) {
	f.children = append(f.children, fc)
}

// flexChildGeom carries per-child layout geometry.
type flexChildGeom struct {
	child     FlexChild
	main      int // final main size
	cross     int // measured cross size
	mainPos   int
	crossPos  int
	crossSize int
}

// flexLineStats aggregates flex factors per line.
type flexLineStats struct {
	baseMain int
	grow     int
	shrink   int
}

// compute packs children within mainTotal/crossTotal and returns geometry plus
// the resulting totals. Used by both Measure and Layout so geometry is shared.
func (f *Flex) compute(ctx LayoutContext, mainTotal, crossTotal int) ([]flexChildGeom, int, int) {
	n := len(f.children)
	if n == 0 {
		return nil, 0, 0
	}
	row := f.dir == FlexRow

	geoms := make([]flexChildGeom, n)
	lineOf := make([]int, n)
	line := 0
	used := 0
	for i, fc := range f.children {
		var childCS Constraints
		if row {
			childCS = Loose(mainTotal, crossTotal)
		} else {
			childCS = Loose(crossTotal, mainTotal)
		}
		var sz Size
		if fc.Component != nil {
			sz = fc.Component.Measure(ctx, childCS)
		}
		main, cross := sz.W, sz.H
		if !row {
			main, cross = sz.H, sz.W
		}
		if fc.FlexBasis > 0 {
			main = fc.FlexBasis
		}
		if i > 0 && f.wrap == WrapLine && used > 0 && used+main > mainTotal {
			line++
			used = 0
		}
		lineOf[i] = line
		if used > 0 {
			used += f.gap
		}
		used += main
		geoms[i] = flexChildGeom{child: fc, main: main, cross: cross}
	}
	lineCount := line + 1

	stats := make([]flexLineStats, lineCount)
	for i := range geoms {
		l := lineOf[i]
		if stats[l].baseMain > 0 {
			stats[l].baseMain += f.gap
		}
		stats[l].baseMain += geoms[i].main
		stats[l].grow += max(0, geoms[i].child.FlexGrow)
		stats[l].shrink += max(0, geoms[i].child.FlexShrink)
	}

	lineMain := make([]int, lineCount)
	lineCross := make([]int, lineCount)
	for i := range geoms {
		l := lineOf[i]
		if geoms[i].cross > lineCross[l] {
			lineCross[l] = geoms[i].cross
		}
	}

	// Flex grow/shrink per line.
	for l := 0; l < lineCount; l++ {
		free := mainTotal - stats[l].baseMain
		if free > 0 && stats[l].grow > 0 {
			left := free
			for i := range geoms {
				if lineOf[i] != l || geoms[i].child.FlexGrow <= 0 {
					continue
				}
				extra := free * geoms[i].child.FlexGrow / stats[l].grow
				geoms[i].main += extra
				left -= extra
			}
			for i := range geoms {
				if lineOf[i] == l && geoms[i].child.FlexGrow > 0 {
					geoms[i].main += left
					break
				}
			}
		} else if free < 0 && stats[l].shrink > 0 {
			over := -free
			for i := range geoms {
				if lineOf[i] != l || geoms[i].child.FlexShrink <= 0 {
					continue
				}
				loss := over * geoms[i].child.FlexShrink / stats[l].shrink
				if loss > geoms[i].main {
					loss = geoms[i].main
				}
				geoms[i].main -= loss
			}
		}
	}

	// Line main totals post-flex and line cross maxima.
	for i := range geoms {
		l := lineOf[i]
		if lineMain[l] > 0 {
			lineMain[l] += f.gap
		}
		lineMain[l] += geoms[i].main
	}

	// Stack lines along the cross axis.
	lineY := make([]int, lineCount)
	crossCursor := 0
	for l := 0; l < lineCount; l++ {
		lineY[l] = crossCursor
		crossCursor += lineCross[l]
		if l < lineCount-1 {
			crossCursor += f.gap
		}
	}

	// Per-line main positioning and per-child cross alignment.
	for l := 0; l < lineCount; l++ {
		free := mainTotal - lineMain[l]
		lead := 0
		gapB := f.gap
		count := 0
		for i := range geoms {
			if lineOf[i] == l {
				count++
			}
		}
		switch {
		case free <= 0:
			// overflow: pack from the front with the base gap
		case f.main == MainEnd:
			lead = free
		case f.main == MainCenter:
			lead = free / 2
		case f.main == MainSpaceBetween && count > 1:
			gapB += free / (count - 1)
		case f.main == MainSpaceAround:
			unit := free / (count * 2)
			lead = unit
			gapB += unit * 2
		case f.main == MainSpaceEvenly:
			unit := free / (count + 1)
			lead = unit
			gapB += unit
		}
		x := lead
		for i := range geoms {
			if lineOf[i] != l {
				continue
			}
			geoms[i].mainPos = x
			x += geoms[i].main + gapB
		}
		for i := range geoms {
			if lineOf[i] != l {
				continue
			}
			g := &geoms[i]
			align := f.cross
			if g.child.CrossAlign != nil {
				align = *g.child.CrossAlign
			}
			lineCrossSize := crossTotal
			if f.wrap == WrapLine {
				lineCrossSize = lineCross[l]
			}
			switch align {
			case CrossCenter:
				g.crossPos = lineY[l] + (lineCrossSize-g.cross)/2
				g.crossSize = g.cross
			case CrossEnd:
				g.crossPos = lineY[l] + lineCrossSize - g.cross
				g.crossSize = g.cross
			case CrossStretch:
				g.crossPos = lineY[l]
				g.crossSize = lineCrossSize
			default:
				g.crossPos = lineY[l]
				g.crossSize = g.cross
			}
		}
	}

	totalMain := 0
	for l := 0; l < lineCount; l++ {
		if lineMain[l] > totalMain {
			totalMain = lineMain[l]
		}
	}
	totalCross := 0
	for l := 0; l < lineCount; l++ {
		if totalCross > 0 {
			totalCross += f.gap
		}
		totalCross += lineCross[l]
	}
	return geoms, totalMain, totalCross
}

// Measure sums children along the main axis (after grow/shrink) and the max
// along the cross axis, clamped to constraints.
func (f *Flex) Measure(ctx LayoutContext, cs Constraints) Size {
	if len(f.children) == 0 {
		return cs.ClampSize(Size{})
	}
	mainTotal, crossTotal := cs.MaxW, cs.MaxH
	if f.dir == FlexColumn {
		mainTotal, crossTotal = cs.MaxH, cs.MaxW
	}
	_, totalMain, totalCross := f.compute(ctx, mainTotal, crossTotal)
	if f.dir == FlexRow {
		return cs.ClampSize(Size{W: totalMain, H: totalCross})
	}
	return cs.ClampSize(Size{W: totalCross, H: totalMain})
}

// Layout assigns each child a parent-local rect.
func (f *Flex) Layout(ctx LayoutContext, r Rect) {
	f.SetBounds(r)
	if len(f.children) == 0 {
		return
	}
	mainTotal, crossTotal := r.W, r.H
	if f.dir == FlexColumn {
		mainTotal, crossTotal = r.H, r.W
	}
	geoms, _, _ := f.compute(ctx, mainTotal, crossTotal)
	for i := range geoms {
		g := &geoms[i]
		if g.child.Component == nil {
			continue
		}
		var cr Rect
		if f.dir == FlexRow {
			cr = Rect{X: g.mainPos, Y: g.crossPos, W: g.main, H: g.crossSize}
		} else {
			cr = Rect{X: g.crossPos, Y: g.mainPos, W: g.crossSize, H: g.main}
		}
		g.child.Component.Layout(ctx, cr)
	}
}

// Paint is a no-op; children are painted by Host or PaintTree.
func (f *Flex) Paint(_ *surface.CellBuffer, _, _ int, _ PaintContext) {}

// Handle ignores; Host hit-testing routes to children.
func (f *Flex) Handle(_ EventContext, _ Event) EventResult {
	return EventResult{}
}

// Semantics reports a group role.
func (f *Flex) Semantics() Semantics {
	return Semantics{Role: RoleGroup, Label: string(f.ID())}
}
