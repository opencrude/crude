package editor

import (
	"errors"
	"slices"
	"sort"

	"crude/lspedit"
	"crude/types"
)

// occurrence is one passive mark of the symbol at the caret, in visual columns.
type occurrence struct {
	start, end Position
	// maxEnd is the furthest end in this start-sorted prefix. Unlike end,
	// it remains monotonic even when ranges overlap or contain other ranges.
	maxEnd Position
	write  bool
}

// occurrenceSpan is the part of one occurrence on one line, [from, to).
type occurrenceSpan struct {
	from, to int
	write    bool
}

// SetOccurrences installs a validated answer for the exact source and caret
// that requested it. Marks are presentation only: they never select, move the
// caret or edit. They hide on the next edit or when the caret leaves them.
func (e *Editor) SetOccurrences(result *types.LSPHighlights) error {
	if result == nil || !e.MatchesInsightSource(result.Boundary.Source) {
		return errors.New("occurrences do not match the current source")
	}
	if line, col := e.LSPCursorPosition(); line != result.Line || col != result.Col {
		return errors.New("occurrences do not match the current caret")
	}
	// Resolve endpoints once per source line. Sorting lets one walker carry
	// grapheme/tab geometry forward instead of rescanning every line for each
	// occurrence. The shared strict UTF-16 source index still validates offsets.
	type endpoint struct {
		position types.LSPNavigationPosition
		index    int
		end      bool
	}
	marks := make([]occurrence, len(result.Highlights))
	points := make([]endpoint, 0, 2*len(marks))
	for i, h := range result.Highlights {
		marks[i].write = h.Kind == types.LSPHighlightWrite
		points = append(points, endpoint{position: h.Range.Start, index: i}, endpoint{position: h.Range.End, index: i, end: true})
	}
	sort.Slice(points, func(i, j int) bool {
		a, b := points[i].position, points[j].position
		return a.Line < b.Line || a.Line == b.Line && a.Character < b.Character
	})
	for first := 0; first < len(points); {
		line := points[first].position.Line
		raw, err := e.buf.lineView(line)
		if err != nil {
			return err
		}
		source, err := lspedit.NewSource(string(raw))
		if err != nil {
			return err
		}
		walker := visualColumnWalker[[]byte]{line: raw, tabWidth: e.TabWidth()}
		last := first
		for last < len(points) && points[last].position.Line == line {
			p := points[last]
			offset, err := source.Offset(0, p.position.Character)
			if err != nil {
				return err
			}
			position := Position{Line: line, Col: walker.advance(offset)}
			if p.end {
				marks[p.index].end = position
			} else {
				marks[p.index].start = position
			}
			last++
		}
		first = last
	}
	marks = slices.DeleteFunc(marks, func(mark occurrence) bool { return !positionBefore(mark.start, mark.end) })
	sort.SliceStable(marks, func(i, j int) bool { return positionBefore(marks[i].start, marks[j].start) })
	for i := range marks {
		marks[i].maxEnd = marks[i].end
		if i > 0 && positionBefore(marks[i].maxEnd, marks[i-1].maxEnd) {
			marks[i].maxEnd = marks[i-1].maxEnd
		}
	}
	e.occurrences, e.occurrenceRevision, e.occurrenceTabWidth = marks, e.Revision(), e.TabWidth()
	return nil
}

func (e *Editor) ClearOccurrences() { e.occurrences = nil }

// OccurrencesCoverCaret reports whether current marks still describe the
// symbol under the caret, so moving within it needs no new request.
func (e *Editor) OccurrencesCoverCaret() bool {
	return e.occurrenceAtCaret()
}

// occurrenceAtCaret is true while the marks are current and the caret is
// inside or at either end of one of them. A single mark is only the symbol
// itself; it adds nothing and is not shown.
func (e *Editor) occurrenceAtCaret() bool {
	if len(e.occurrences) < 2 || e.occurrenceRevision != e.Revision() || e.occurrenceTabWidth != e.TabWidth() {
		return false
	}
	i := sort.Search(len(e.occurrences), func(i int) bool { return positionBefore(e.cursor, e.occurrences[i].start) })
	return i > 0 && !positionBefore(e.occurrences[i-1].maxEnd, e.cursor)
}

// lineOccurrences returns the marks on one painted line. The prefix maximum
// lets the search include enclosing ranges without scanning earlier lines.
func (e *Editor) lineOccurrences(line int, visible bool) []occurrenceSpan {
	if !visible {
		return nil
	}
	var spans []occurrenceSpan
	first := sort.Search(len(e.occurrences), func(i int) bool { return e.occurrences[i].maxEnd.Line >= line })
	for _, o := range e.occurrences[first:] {
		if o.start.Line > line {
			break
		}
		if o.end.Line < line {
			continue
		}
		span := occurrenceSpan{from: 0, to: int(^uint(0) >> 1), write: o.write}
		if o.start.Line == line {
			span.from = o.start.Col
		}
		if o.end.Line == line {
			span.to = o.end.Col
		}
		spans = append(spans, span)
	}
	return spans
}

func occurrenceAt(spans []occurrenceSpan, col int) (write, ok bool) {
	for _, s := range spans {
		if col >= s.from && col < s.to {
			return s.write, true
		}
	}
	return false, false
}
