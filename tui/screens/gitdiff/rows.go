package gitdiff

import (
	"iter"
	"sort"
)

// A presentation index holds one span per hunk or folded gap, not another row
// object for every source line. Install, split and fold are hunk-sized work on
// Update; paint resolves only the visible rows from immutable prepared files.
type rowSpan struct {
	start, count int
	base         row
	lines        []line
	pairs        []pair
	gap          bool
}

type rowIndex struct {
	spans []rowSpan
	count int
}

func (r *rowIndex) reset()   { clear(r.spans); r.spans = r.spans[:0]; r.count = 0 }
func (r *rowIndex) Len() int { return r.count }
func (r *rowIndex) add(span rowSpan) {
	if span.count == 0 {
		return
	}
	span.start = r.count
	r.count += span.count
	r.spans = append(r.spans, span)
}
func (r *rowIndex) single(value row) { r.add(rowSpan{count: 1, base: value}) }
func (r *rowIndex) At(index int) row {
	i := sort.Search(len(r.spans), func(i int) bool { return r.spans[i].start+r.spans[i].count > index })
	s := &r.spans[i]
	out, offset := s.base, index-s.start
	switch {
	case s.lines != nil:
		l := s.lines[offset]
		out.old, out.new, out.lk = l.old, l.new, l.kind
	case s.pairs != nil:
		p := s.pairs[offset]
		out.old, out.new, out.lk = p.old, p.new, lineContext
		if p.changed {
			out.lk = lineAdded
		}
	case s.gap:
		out.old += offset
		out.new += offset
	}
	return out
}
func (r *rowIndex) All() iter.Seq2[int, row] {
	return func(yield func(int, row) bool) {
		for i := 0; i < r.count; i++ {
			if !yield(i, r.At(i)) {
				return
			}
		}
	}
}
