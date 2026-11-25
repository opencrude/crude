package ui

import (
	"sort"

	"crude/tui/rendering"
)

// Every consumer uses these source boundaries, including the two widths a
// reader measures with and without its scrollbar. Row lookup never rewraps.
type textAreaWrap struct {
	content, atoms, profile uint64
	width, count            int
	lines                   []wrappedTextLine
	clipped                 map[int]string // oversize object at its flat source offset
}
type wrappedTextLine struct {
	flat, first int
	rows        []rendering.RuneRange
}

func (e *textAreaEngine) wrapped(width int) *textAreaWrap {
	width = max(1, width)
	profile := rendering.WidthProfileGeneration()
	for i := range e.wrap {
		w := &e.wrap[i]
		if w.width == width && w.content == e.contentRevision && w.atoms == e.atomicRevision && w.profile == profile {
			if i != 0 {
				e.wrap[0], e.wrap[i] = e.wrap[i], e.wrap[0]
			}
			return &e.wrap[0]
		}
	}
	w := textAreaWrap{width: width, content: e.contentRevision, atoms: e.atomicRevision, profile: profile, lines: make([]wrappedTextLine, len(e.buf))}
	atoms := e.atomicRanges
	var objects []rendering.RuneRange
	flat := 0
	for i, line := range e.buf {
		objects = objects[:0]
		for len(atoms) > 0 && atoms[0].Start < flat+len(line) {
			a := atoms[0]
			atoms = atoms[1:]
			if a.Start < flat || a.End > flat+len(line) {
				continue
			}
			start, end := a.Start-flat, a.End-flat
			objects = append(objects, rendering.RuneRange{Start: start, End: end})
			label := string(line[start:end])
			if rendering.VisibleWidth(label) > width {
				if w.clipped == nil {
					w.clipped = make(map[int]string)
				}
				w.clipped[a.Start] = rendering.TruncateVisual(label, width, "…")
			}
		}
		rows := rendering.WordWrapRuneRanges(line, width, objects)
		w.lines[i] = wrappedTextLine{flat: flat, first: w.count, rows: rows}
		w.count += len(rows)
		flat += len(line) + 1
	}
	if e.wrap[0].content == w.content && e.wrap[0].atoms == w.atoms && e.wrap[0].profile == w.profile {
		e.wrap[1] = e.wrap[0]
	} else {
		e.wrap[1] = textAreaWrap{}
	}
	e.wrap[0] = w
	return &e.wrap[0]
}

// rowAtCursor assigns omitted wrap whitespace to the following visible row.
// At a hard break, a caret before the first character belongs on its row.
func (l wrappedTextLine) rowAtCursor(col int) int {
	i := sort.Search(len(l.rows), func(i int) bool { return l.rows[i].End >= col })
	if i+1 < len(l.rows) && l.rows[i+1].Start == col {
		return i + 1
	}
	return min(i, len(l.rows)-1)
}

func (w *textAreaWrap) lineAtRow(row int) int {
	return sort.Search(len(w.lines), func(i int) bool { return w.lines[i].first+len(w.lines[i].rows) > row })
}
