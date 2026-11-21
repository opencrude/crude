package bidi

// PlacedLine places a paragraph fragment at a fixed column on a structured
// row. Its line does not include columns that belong to another paragraph.
type PlacedLine struct {
	Column int
	Line   *Line
}

// JoinLines combines non-overlapping fragments supplied in column order.
// The gaps (for example table padding or separators) keep their positions.
func JoinLines(width int, parts []PlacedLine) *Line {
	if len(parts) == 0 {
		return nil
	}
	out := &Line{text: width, toVis: make([]int32, width), toLog: make([]int32, width), identity: true}
	for i := range out.toVis {
		out.toVis[i], out.toLog[i] = int32(i), int32(i)
	}
	end := 0
	gap := func(next int) {
		if next > end {
			out.blocks = append(out.blocks, Block{Unit: -1, Logical: end, Start: end, Visual: end, Width: next - end})
		}
	}
	for _, part := range parts {
		ln := part.Line
		if ln.Identity() || part.Column < end || part.Column+ln.Span() > width {
			continue
		}
		gap(part.Column)
		for _, block := range ln.Blocks() {
			block.Logical += part.Column
			block.Visual += part.Column
			block.Start += part.Column
			out.blocks = append(out.blocks, block)
		}
		for i := range ln.toVis {
			out.toVis[part.Column+i] = int32(part.Column) + ln.toVis[i]
			out.toLog[part.Column+i] = int32(part.Column) + ln.toLog[i]
		}
		end = part.Column + ln.Span()
		out.identity = false
	}
	if out.identity {
		return nil
	}
	gap(width)
	return out
}

// LineRun places an arranged paragraph fragment or a fixed run of decoration
// on a row. A nil Line keeps the run's cell order and glyphs.
type LineRun struct {
	Column, Width int
	Line          *Line
}

// ComposeLine combines runs supplied in visual order. They must cover each
// logical column exactly once. Decoration moves as a whole without entering
// any paragraph's bidi analysis.
func ComposeLine(width int, rtl bool, runs []LineRun) *Line {
	out := &Line{text: width, rtl: rtl, identity: true, toVis: make([]int32, width), toLog: make([]int32, width)}
	for i := range out.toVis {
		out.toVis[i] = -1
	}
	visual := 0
	for _, r := range runs {
		if r.Width == 0 {
			continue
		}
		if r.Width < 0 || r.Column < 0 || r.Column+r.Width > width || visual+r.Width > width {
			return nil
		}
		if r.Line == nil {
			out.blocks = append(out.blocks, Block{Unit: -1, Logical: r.Column, Start: r.Column, Visual: visual, Width: r.Width})
		} else {
			if r.Line.Span() != r.Width {
				return nil
			}
			for _, block := range r.Line.Blocks() {
				block.Logical += r.Column
				block.Start += r.Column
				block.Visual += visual
				out.blocks = append(out.blocks, block)
			}
			out.identity = out.identity && r.Line.Identity()
		}
		for i := 0; i < r.Width; i++ {
			logical := r.Column + r.Line.Logical(i)
			if out.toVis[logical] >= 0 {
				return nil
			}
			out.toVis[logical], out.toLog[visual+i] = int32(visual+i), int32(logical)
		}
		out.identity = out.identity && r.Column == visual
		visual += r.Width
	}
	if visual != width || out.identity {
		return nil
	}
	return out
}
