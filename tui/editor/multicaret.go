package editor

import (
	"cmp"
	"fmt"
	"slices"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"crude/document"
	"crude/tui/ui"
)

// SourceSelection is a private UTF-8 range. Cursor determines its direction.
// Secondary sets are immutable so history can retain them without a large
// per-edit fixed array or aliases to later selection changes.
type SourceSelection struct{ Anchor, Cursor int }
type caretGoal struct {
	col         int
	set, before bool
}
type caretSet struct {
	ranges []SourceSelection
	goals  []caretGoal
}
type selectionSpan struct {
	SourceSelection
	primary bool
	goal    caretGoal
}

func (s SourceSelection) bounds() (int, int) { return min(s.Anchor, s.Cursor), max(s.Anchor, s.Cursor) }

const MaxCarets = 256

type SelectionNoticeMsg struct{ Message string }

func (e *Editor) HasMultipleCarets() bool {
	return e.secondary != nil && len(e.secondary.ranges) > 0 && !e.multiDispatch
}
func (e *Editor) CaretCount() int {
	if e.secondary == nil {
		return 1
	}
	return 1 + len(e.secondary.ranges)
}
func (e *Editor) primaryRange() SourceSelection {
	if e.primaryHasSelection() {
		return SourceSelection{e.buf.positionToOffset(e.selAnchor), e.buf.positionToOffset(e.selEnd)}
	}
	off := e.buf.positionToOffset(e.cursor)
	return SourceSelection{off, off}
}
func (e *Editor) selectionSpans() []selectionSpan {
	out := []selectionSpan{{SourceSelection: e.primaryRange(), primary: true, goal: caretGoal{e.goalCol, e.goalSet, e.wrapBefore}}}
	if e.secondary != nil {
		for i, r := range e.secondary.ranges {
			goal := caretGoal{}
			if i < len(e.secondary.goals) {
				goal = e.secondary.goals[i]
			}
			out = append(out, selectionSpan{SourceSelection: r, goal: goal})
		}
	}
	slices.SortFunc(out, func(a, b selectionSpan) int {
		as, ae := a.bounds()
		bs, be := b.bounds()
		if n := cmp.Compare(as, bs); n != 0 {
			return n
		}
		return cmp.Compare(ae, be)
	})
	return out
}
func (e *Editor) SourceSelections() []SourceSelection {
	e.normalizeCarets()
	spans := e.selectionSpans()
	out := make([]SourceSelection, len(spans))
	for i, s := range spans {
		out[i] = s.SourceSelection
	}
	return out
}

// Display goals belong to geometry, not retained source selections.
func (e *Editor) resetSecondaryGoals() {
	if e.secondary == nil || len(e.secondary.goals) == 0 {
		return
	}
	e.retainSecondary(&caretSet{ranges: e.secondary.ranges})
}

func (e *Editor) clearSecondary() {
	if e.multiDispatch {
		return
	}
	e.secondary = nil
	e.multiDirty = false
	if e.buf != nil && e.snippet == nil && e.ghost == nil && e.ghostEdit == nil {
		delete(e.buf.interactionViews, e)
	}
}
func (e *Editor) retainSecondary(set *caretSet) {
	e.secondary = set
	if set == nil && e.buf != nil && e.snippet == nil && e.ghost == nil && e.ghostEdit == nil {
		delete(e.buf.interactionViews, e)
	}
	if set != nil {
		if e.buf.interactionViews == nil {
			e.buf.interactionViews = make(map[*Editor]struct{})
		}
		e.buf.interactionViews[e] = struct{}{}
	}
}

// setSelections validates the complete proposal before publishing it. Overlap
// and duplicate carets coalesce; adjacency alone is not an overlapping range.
func (e *Editor) setSelections(input []selectionSpan) error {
	if len(input) == 0 || len(input) > MaxCarets {
		return fmt.Errorf("use at most %d carets", MaxCarets)
	}
	input = slices.Clone(input)
	for _, s := range input {
		for _, off := range []int{s.Anchor, s.Cursor} {
			p := e.buf.positionAtOffset(off)
			if off < 0 || e.buf.positionToOffset(p) != off {
				return fmt.Errorf("selection is not on a source character boundary")
			}
		}
	}
	slices.SortFunc(input, func(a, b selectionSpan) int {
		as, ae := a.bounds()
		bs, be := b.bounds()
		if n := cmp.Compare(as, bs); n != 0 {
			return n
		}
		return cmp.Compare(ae, be)
	})
	out := input[:0]
	for _, s := range input {
		if len(out) > 0 {
			prev := &out[len(out)-1]
			ps, pe := prev.bounds()
			ss, se := s.bounds()
			if ss < pe || ss == ps || ss == pe && (ss == se || ps == pe) {
				cursor, anchor := max(pe, se), ps
				if s.primary && s.Cursor < s.Anchor || !s.primary && prev.Cursor < prev.Anchor {
					cursor, anchor = anchor, cursor
				}
				*prev = selectionSpan{SourceSelection: SourceSelection{anchor, cursor}, primary: prev.primary || s.primary}
				continue
			}
		}
		out = append(out, s)
	}
	primary := -1
	for i, s := range out {
		if s.primary {
			primary = i
			break
		}
	}
	if primary < 0 {
		primary = len(out) - 1
	}
	chosen := out[primary]
	e.cursor = e.buf.positionAtOffset(chosen.Cursor)
	e.selAnchor = e.buf.positionAtOffset(chosen.Anchor)
	e.selEnd = e.cursor
	e.selecting = chosen.Anchor != chosen.Cursor
	e.goalCol, e.goalSet, e.wrapBefore = chosen.goal.col, chosen.goal.set, chosen.goal.before
	var secondary *caretSet
	if len(out) > 1 {
		secondary = &caretSet{ranges: make([]SourceSelection, 0, len(out)-1)}
		for i, s := range out {
			if i != primary {
				secondary.ranges = append(secondary.ranges, s.SourceSelection)
				secondary.goals = append(secondary.goals, s.goal)
			}
		}
	}
	e.clearSecondary()
	e.retainSecondary(secondary)
	return nil
}

// Secondary byte anchors follow the same simultaneous vector as primary
// linked-view anchors. Immutable retained history never aliases the next set.
func (pt *PieceTable) remapCarets(edits []document.Edit) {
	for view := range pt.interactionViews {
		if view.secondary == nil || pt.editOwner == view && view.multiApplying {
			continue
		}
		next := &caretSet{ranges: make([]SourceSelection, len(view.secondary.ranges))}
		for i, s := range view.secondary.ranges {
			next.ranges[i] = remapSourceSelection(s, edits)
		}
		view.retainSecondary(next)
		view.multiDirty = true
		if view != pt.linkActor {
			view.normalizeCarets()
		}
	}
}
func remapSourceSelection(s SourceSelection, edits []document.Edit) SourceSelection {
	a, b := s.bounds()
	start, end := remapLinkedOffset(a, edits), remapLinkedOffset(b, edits)
	if a != b {
		for _, edit := range edits {
			if edit.Start == a && edit.End == a {
				start += len(edit.Text)
			}
		}
	}
	if s.Anchor > s.Cursor {
		return SourceSelection{end, start}
	}
	return SourceSelection{start, end}
}
func (e *Editor) normalizeCarets() {
	if !e.multiDirty || e.multiDispatch || e.multiApplying || e.secondary == nil {
		return
	}
	spans := e.selectionSpans()
	for i, s := range spans {
		spans[i].Anchor = e.buf.positionToOffset(e.buf.positionAtOffset(s.Anchor))
		spans[i].Cursor = e.buf.positionToOffset(e.buf.positionAtOffset(s.Cursor))
	}
	_ = e.setSelections(spans)
	e.multiDirty = false
}
func (e *Editor) selectionAction(id ui.ActionID) (bool, error) {
	switch id {
	case "select.singleCaret":
		e.clearSecondary()
		return true, nil
	case "select.nextOccurrence", "select.allOccurrences":
		start, end := e.primaryRange().bounds()
		spans := e.selectionSpans()
		if start == end {
			a, b := e.pointerUnitRange(pointerWord, e.cursor)
			start, end = e.buf.positionToOffset(a), e.buf.positionToOffset(b)
			if start == end {
				return true, nil
			}
			spans = []selectionSpan{{SourceSelection: SourceSelection{start, end}, primary: true}}
			if id == "select.nextOccurrence" {
				return true, e.setSelections(spans)
			}
		}
		e.buf.ensureCache()
		text := string(e.buf.cachedBytes)
		needle := text[start:end]
		offers := func(a, b int) bool {
			for _, s := range spans {
				lo, hi := s.bounds()
				if a < hi && lo < b {
					return false
				}
			}
			return true
		}
		add := func(a, b int) error {
			for i := range spans {
				spans[i].primary = false
			}
			spans = append(spans, selectionSpan{SourceSelection: SourceSelection{a, b}, primary: true})
			if len(spans) > MaxCarets {
				return fmt.Errorf("too many matches; use Replace All or select fewer matches")
			}
			return nil
		}
		from := end
		if id == "select.allOccurrences" {
			from = 0
		}
		scan := func(lo, hi int) (bool, error) {
			for at := lo; at <= hi-len(needle); {
				i := strings.Index(text[at:hi], needle)
				if i < 0 {
					break
				}
				a := at + i
				b := a + len(needle)
				at = b
				if !offers(a, b) {
					continue
				}
				if e.buf.positionToOffset(e.buf.positionAtOffset(a)) != a || e.buf.positionToOffset(e.buf.positionAtOffset(b)) != b {
					continue
				}
				if err := add(a, b); err != nil {
					return false, err
				}
				if id == "select.nextOccurrence" {
					return true, nil
				}
			}
			return false, nil
		}
		found, err := scan(from, len(text))
		if err != nil {
			return true, err
		}
		if !found && id == "select.nextOccurrence" {
			_, err = scan(0, start)
			if err != nil {
				return true, err
			}
		}
		return true, e.setSelections(spans)
	case "select.caretAbove", "select.caretBelow":
		spans := e.selectionSpans()
		pos := e.cursor
		delta := 1
		if id == "select.caretAbove" {
			delta = -1
		}
		line := pos.Line + delta
		if line < 0 || line >= e.LineCount() {
			return true, nil
		}
		e.unfoldSource(line, true)
		pos = e.buf.snapPosition(Position{Line: line, Col: pos.Col})
		off := e.buf.positionToOffset(pos)
		for i := range spans {
			spans[i].primary = false
		}
		return true, e.setSelections(append(spans, selectionSpan{SourceSelection: SourceSelection{off, off}, primary: true}))
	}
	return false, nil
}
func (e *Editor) multipleText() string {
	e.normalizeCarets()
	e.buf.ensureCache()
	var pieces []string
	for _, s := range e.selectionSpans() {
		a, b := s.bounds()
		if a != b {
			pieces = append(pieces, string(e.buf.cachedBytes[a:b]))
		}
	}
	return strings.Join(pieces, e.lineEnding())
}

type multiPaintRange struct {
	start, end, cursor Position
	before             bool
}
type multiPaintCache struct {
	set      *caretSet
	revision uint64
	width    int
	ranges   []multiPaintRange
}

func (e *Editor) prepareMultiplePaint() {
	if e.secondary == nil {
		e.multiPaint = multiPaintCache{}
		return
	}
	p := &e.multiPaint
	if p.set == e.secondary && p.revision == e.Revision() && p.width == e.TabWidth() {
		return
	}
	p.set, p.revision, p.width = e.secondary, e.Revision(), e.TabWidth()
	p.ranges = p.ranges[:0]
	for i, s := range e.secondary.ranges {
		a, b := s.bounds()
		before := i < len(e.secondary.goals) && e.secondary.goals[i].before
		p.ranges = append(p.ranges, multiPaintRange{start: e.buf.positionAtOffset(a), end: e.buf.positionAtOffset(b), cursor: e.buf.positionAtOffset(s.Cursor), before: before})
	}
}
func (e *Editor) secondarySelected(line, col int) bool {
	if e.secondary == nil {
		return false
	}
	e.prepareMultiplePaint()
	p := Position{Line: line, Col: col}
	i := sort.Search(len(e.multiPaint.ranges), func(i int) bool { return positionBefore(p, e.multiPaint.ranges[i].end) })
	return i < len(e.multiPaint.ranges) && !positionBefore(p, e.multiPaint.ranges[i].start)
}

func (e *Editor) toggleCaretAt(pos Position) {
	spans := e.selectionSpans()
	off := e.buf.positionToOffset(pos)
	for i, s := range spans {
		a, b := s.bounds()
		if a == off && b == off && len(spans) > 1 {
			spans = append(spans[:i], spans[i+1:]...)
			_ = e.setSelections(spans)
			e.ensureCursorVisible()
			return
		}
	}
	if len(spans) == MaxCarets {
		return
	}
	for i := range spans {
		spans[i].primary = false
	}
	_ = e.setSelections(append(spans, selectionSpan{SourceSelection: SourceSelection{off, off}, primary: true}))
	e.ensureCursorVisible()
}
func (e *Editor) projectRectangle(pos Position) {
	first, originCol := e.visualPosition(e.drag.start, false)
	last, _ := e.visualPosition(pos, e.wrapBefore)
	if max(last-first, first-last) >= MaxCarets {
		last = first + min(MaxCarets-1, max(1-MaxCarets, last-first))
	}
	left := originCol
	right := max(0, e.drag.x-e.gutterWidth())
	var ranges []selectionSpan
	for row := min(first, last); row <= max(first, last); row++ {
		a, _ := e.positionOnRow(row, left)
		b, _ := e.positionOnRow(row, right)
		if !e.wrapsRows() {
			line := e.rowAt(row).line
			a = e.buf.snapPosition(Position{Line: line, Col: e.sourceColumn(line, left)})
			b = e.buf.snapPosition(Position{Line: line, Col: e.sourceColumn(line, e.scrollX+right)})
		}
		ranges = append(ranges, selectionSpan{SourceSelection: SourceSelection{e.buf.positionToOffset(a), e.buf.positionToOffset(b)}, primary: row == last})
	}
	_ = e.setSelections(ranges)
	e.cursorBlink.MarkActivity()
}

func (e *Editor) expandMultiple() tea.Cmd {
	chain := e.expansion
	if !e.selectionExpansionCurrent() {
		chain = &selectionExpansion{buffer: e.buf, revision: e.Revision()}
	}
	previous := e.captureViewState()
	cmd := e.multipleMotion(func() { e.expansion = nil; e.expandSelection() })
	chain.previous = append(chain.previous, previous)
	chain.expected = e.captureViewState()
	e.expansion = chain
	return cmd
}
