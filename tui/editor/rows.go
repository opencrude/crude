package editor

import (
	"context"
	"sort"
)

// Row geometry never stores source text or edits. Short/narrow-unit lines use
// arithmetic runs; repeated wrap lengths share one run instead of one allocation
// per visual row. A line's source metrics survive width-only reflow.
type rowSpan struct{ start, end int }
type rowMetrics struct {
	width  int
	atomic []rowSpan // only indivisible multi-cell graphemes; tab cells may wrap
	breaks []int     // after whitespace, without dropping it from source
}

func measureRowMetrics(raw string, tabW int) rowMetrics {
	m, _, _, _ := appendRowMetrics(context.Background(), raw, tabW, nil, nil)
	return m
}

// Cold document preparation shares arenas across lines. Each metric receives a
// capped immutable slice, so later appends cannot overwrite another line.
func appendRowMetrics(ctx context.Context, raw string, tabW int, atomic []rowSpan, breaks []int) (rowMetrics, []rowSpan, []int, error) {
	a, b := len(atomic), len(breaks)
	var m rowMetrics
	seenText := false
	nextCheck := 0
	for off := 0; off < len(raw); {
		if ctx != nil && off >= nextCheck {
			if err := ctx.Err(); err != nil {
				return rowMetrics{}, atomic, breaks, err
			}
			nextCheck = off + 4096
		}
		n, w := firstSourceUnit(tabW, raw[off:], m.width)
		if n == 0 {
			break
		}
		white := raw[off] == ' ' || raw[off] == '\t'
		if !white && w > 1 {
			atomic = append(atomic, rowSpan{m.width, m.width + w})
		}
		m.width += w
		if white && seenText {
			// One opportunity at the end of a whitespace run is enough. It retains
			// every source cell, including leading/trailing and tab padding.
			if off+n == len(raw) || raw[off+n] != ' ' && raw[off+n] != '\t' {
				breaks = append(breaks, m.width)
			}
		} else if !white {
			seenText = true
		}
		off += n
	}
	m.atomic, m.breaks = atomic[a:len(atomic):len(atomic)], breaks[b:len(breaks):len(breaks)]
	return m, atomic, breaks, nil
}

type rowRun struct{ row, col, count, step int }
type rowLine struct {
	width, count int
	stride       int // uniform narrow-unit rows need no slice
	runs         []rowRun
}

func wrapRowMetrics(m rowMetrics, width int, reuse []rowRun) rowLine {
	width = max(1, width)
	line := rowLine{width: m.width, runs: reuse[:0]}
	if m.width <= width {
		return rowLine{width: m.width, count: 1, stride: width}
	}
	if len(m.atomic) == 0 && len(m.breaks) == 0 {
		return rowLine{width: m.width, count: max(1, (m.width+width-1)/width), stride: width}
	}
	add := func(col, step, count int) {
		if count <= 0 {
			return
		}
		step = max(1, step)
		if n := len(line.runs); n > 0 {
			last := &line.runs[n-1]
			if last.step == step && last.col+last.count*step == col {
				last.count += count
				line.count += count
				return
			}
		}
		line.runs = append(line.runs, rowRun{line.count, col, count, step})
		line.count += count
	}
	for start := 0; start < m.width; {
		end := min(m.width, start+width)
		if end < m.width {
			i := sort.Search(len(m.atomic), func(i int) bool { return m.atomic[i].end > end })
			if i < len(m.atomic) && m.atomic[i].start < end {
				end = m.atomic[i].start
				if end <= start {
					end = m.atomic[i].end
				} // a glyph wider than this entire pane
			}
			stop := sort.Search(len(m.breaks), func(i int) bool { return m.breaks[i] > end }) - 1
			if stop >= 0 && m.breaks[stop] > start {
				end = m.breaks[stop]
			}
		}
		add(start, end-start, 1)
		start = end
	}
	if line.count == 0 {
		add(0, width, 1)
	}
	return line
}

func (l rowLine) start(row int) int {
	if len(l.runs) == 0 {
		return min(max(0, row), max(0, l.count-1)) * l.stride
	}
	row = min(max(0, row), max(0, l.count-1))
	index := sort.Search(len(l.runs), func(i int) bool { return l.runs[i].row+l.runs[i].count > row })
	run := l.runs[index]
	return run.col + (row-run.row)*run.step
}
func (l rowLine) end(row int) int {
	if row+1 >= l.count {
		return l.width
	}
	return l.start(row + 1)
}
func (l rowLine) atColumn(col int) int {
	if len(l.runs) == 0 {
		return min(max(0, l.count-1), max(0, col)/max(1, l.stride))
	}
	col = min(max(0, col), max(0, l.width-1))
	index := sort.Search(len(l.runs), func(i int) bool { return l.runs[i].col+l.runs[i].count*l.runs[i].step > col })
	run := l.runs[min(index, len(l.runs)-1)]
	return min(l.count-1, run.row+max(0, col-run.col)/run.step)
}

// sourceRows is an order-statistics index over source lines. A hidden line has
// count zero, so folding and wrapping can share the same row/line conversions.
// Reflow changes only affected line counts. Prefix arithmetic is lazy; source
// geometry is never rebuilt while answering a pointer or scroll query.
type sourceRows struct {
	lines  []rowLine
	prefix []int
	valid  int
}

func (r *sourceRows) reset(count int) {
	r.lines = make([]rowLine, count)
	r.prefix = make([]int, count+1)
	r.valid = 0
}
func (r *sourceRows) set(line int, value rowLine) {
	r.lines[line] = value
	r.valid = min(r.valid, line)
}
func (r *sourceRows) before(line int) int {
	line = min(max(0, line), len(r.lines))
	for r.valid < line {
		r.prefix[r.valid+1] = r.prefix[r.valid] + r.lines[r.valid].count
		r.valid++
	}
	return r.prefix[line]
}
func (r *sourceRows) total() int { return r.before(len(r.lines)) }
func (r *sourceRows) at(row int) (line, local int) {
	total := r.total()
	if total == 0 {
		return 0, 0
	}
	row = min(max(0, row), total-1)
	line = sort.Search(len(r.lines), func(i int) bool { return r.prefix[i+1] > row })
	return line, row - r.prefix[line]
}
