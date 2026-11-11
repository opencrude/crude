package editor

import (
	"cmp"
	"crypto/sha256"
	"slices"
	"sort"
	"strings"

	"crude/syntax"
	"crude/tui/ui"
)

// FoldRange hides complete source lines after Start, through End. Endpoints
// are source lines, never visible rows. Each view owns its collapsed choices.
type FoldRange struct{ Start, End int }
type foldState struct {
	buffer               *PieceTable
	revision, generation uint64
	hiddenGeneration     uint64
	native               *syntax.Result
	lexical              *syntaxDocument
	ranges               []FoldRange
	collapsed            []FoldRange
	hidden               []FoldRange // non-overlapping outer intervals, binary-searchable
}

func (e *Editor) remapFolds() {
	f := &e.folds
	if f.buffer == e.buf && f.revision == e.Revision() {
		return
	}
	if f.buffer != e.buf || f.revision > e.Revision() || e.Revision()-f.revision > uint64(len(e.buf.lineChanges)) {
		f.ranges, f.collapsed = nil, nil
	} else {
		for next := f.revision + 1; next <= e.Revision(); next++ {
			c := e.buf.lineChanges[next%uint64(len(e.buf.lineChanges))]
			if c.revision != next {
				f.ranges, f.collapsed = nil, nil
				break
			}
			remap := func(in []FoldRange) []FoldRange {
				out := in[:0]
				for _, r := range in {
					if c.end < r.Start {
						r.Start += c.delta
						r.End += c.delta
					} else if c.start <= r.End {
						continue
					}
					out = append(out, r)
				}
				return out
			}
			f.ranges, f.collapsed = remap(f.ranges), remap(f.collapsed)
		}
	}
	unchangedEmpty := len(f.ranges) == 0 && len(f.collapsed) == 0 && len(f.hidden) == 0
	f.buffer, f.revision = e.buf, e.Revision()
	f.native, f.lexical = nil, nil
	if !unchangedEmpty {
		f.changed()
	}
}
func (f *foldState) changed() {
	f.generation++
	slices.SortFunc(f.collapsed, compareFold)
	// Candidate/paint refreshes must not invalidate wrapped source geometry.
	var next []FoldRange
	for _, r := range f.collapsed {
		if len(next) == 0 || r.Start > next[len(next)-1].End {
			next = append(next, r)
		}
	}
	if !slices.Equal(next, f.hidden) {
		f.hiddenGeneration++
		f.hidden = next
	}
}
func compareFold(a, b FoldRange) int {
	if v := cmp.Compare(a.Start, b.Start); v != 0 {
		return v
	}
	return cmp.Compare(b.End, a.End)
}
func normalizeFolds(ranges []FoldRange) []FoldRange {
	slices.SortFunc(ranges, compareFold)
	out := ranges[:0]
	var stack []int
	for _, r := range ranges {
		if r.Start < 0 || r.End <= r.Start || len(out) > 0 && out[len(out)-1].Start == r.Start {
			continue
		}
		for len(stack) > 0 && r.Start > stack[len(stack)-1] {
			stack = stack[:len(stack)-1]
		}
		if len(stack) > 0 && r.End > stack[len(stack)-1] {
			continue
		} // crossing provider ranges are not safe folds
		out = append(out, r)
		stack = append(stack, r.End)
	}
	return out
}
func (e *Editor) refreshFolds() {
	e.remapFolds()
	f := &e.folds
	native, lexical := e.structuralSource()
	if native == nil && lexical == nil || native == f.native && lexical == f.lexical {
		return
	}
	f.native, f.lexical = native, lexical
	var ranges []syntax.Range
	var brackets []syntax.Bracket
	if native != nil {
		ranges, brackets = native.Selections, native.Brackets
	} else {
		ranges, brackets = lexical.selections, lexical.brackets
	}
	e.buf.ensureCache()
	offsets := e.buf.lineOffsets
	size := len(e.buf.cachedBytes)
	lineAt := func(off int) int {
		return max(0, sort.Search(len(offsets), func(i int) bool { return offsets[i] > off })-1)
	}
	var next []FoldRange
	offer := func(start, end int) {
		if start < 0 || end <= start || end > size {
			return
		}
		a, b := lineAt(start), lineAt(end-1)
		if b > a {
			next = append(next, FoldRange{a, b})
		}
	}
	for _, r := range ranges {
		if r.Start == 0 && int(r.End) >= size {
			continue
		}
		offer(int(r.Start), int(r.End))
	}
	for _, r := range brackets {
		if r.Offset < r.Mate {
			offer(int(r.Offset), int(r.Mate)+1)
		}
	}
	// Multi-line literal/comment spans come from the same lexical owner. A
	// quoted bracket never creates a separate guessed fold.
	if native != nil {
		for _, s := range native.Spans {
			if s.Kind == syntax.Comment || s.Kind == syntax.String {
				offer(int(s.Start), int(s.End))
			}
		}
	} else {
		start := 0
		for _, s := range lexical.spans {
			if structuralExcluded(s.kind) {
				offer(start, s.end)
			}
			start = s.end
		}
	}
	next = normalizeFolds(next)
	if slices.Equal(next, f.ranges) {
		return
	}
	f.ranges = next
	f.collapsed = slices.DeleteFunc(f.collapsed, func(r FoldRange) bool { return !containsFold(next, r) })
	f.changed()
}
func (e *Editor) foldAt(line int) (FoldRange, bool) {
	r := e.folds.ranges
	i := sort.Search(len(r), func(i int) bool { return r[i].Start >= line })
	if i < len(r) && r[i].Start == line {
		return r[i], true
	}
	return FoldRange{}, false
}
func (e *Editor) hiddenFold(line int) (FoldRange, bool) {
	r := e.folds.hidden
	i := sort.Search(len(r), func(i int) bool { return r[i].End >= line })
	if i < len(r) && line > r[i].Start {
		return r[i], true
	}
	return FoldRange{}, false
}
func (e *Editor) unfoldSource(line int, header bool) {
	e.remapFolds()
	f := &e.folds
	before := len(f.collapsed)
	f.collapsed = slices.DeleteFunc(f.collapsed, func(r FoldRange) bool { return line <= r.End && (line > r.Start || header && line == r.Start) })
	if len(f.collapsed) != before {
		f.changed()
	}
}
func (e *Editor) toggleFold(line int) {
	e.refreshFolds()
	r, ok := e.foldAt(line)
	if !ok {
		return
	}
	if containsFold(e.folds.collapsed, r) {
		e.folds.collapsed = slices.DeleteFunc(e.folds.collapsed, func(old FoldRange) bool { return old == r })
	} else {
		e.folds.collapsed = append(e.folds.collapsed, r)
	}
	e.finishFoldChange()
}
func (e *Editor) foldAction(id ui.ActionID) {
	e.refreshFolds()
	f := &e.folds
	switch id {
	case "view.unfoldAll":
		f.collapsed = nil
	case "view.foldAll":
		f.collapsed = slices.Clone(f.ranges)
	case "view.fold", "view.unfold":
		want := id == "view.fold"
		chosen := -1
		for i, r := range f.ranges {
			if r.Start <= e.cursor.Line && r.End >= e.cursor.Line && containsFold(f.collapsed, r) != want {
				chosen = i
			}
		}
		if chosen < 0 {
			return
		}
		r := f.ranges[chosen]
		if want {
			f.collapsed = append(f.collapsed, r)
		} else {
			f.collapsed = slices.DeleteFunc(f.collapsed, func(old FoldRange) bool { return old == r })
		}
	default:
		present := make(map[FoldRange]bool, len(f.collapsed))
		for _, r := range f.collapsed {
			present[r] = true
		}
		level := int(strings.TrimPrefix(string(id), "view.foldLevel")[0] - '0')
		var stack []int
		for _, r := range f.ranges {
			for len(stack) > 0 && r.Start > stack[len(stack)-1] {
				stack = stack[:len(stack)-1]
			}
			if len(stack)+1 == level && !present[r] {
				f.collapsed = append(f.collapsed, r)
			}
			stack = append(stack, r.End)
		}
	}
	e.finishFoldChange()
}
func (e *Editor) finishFoldChange() {
	e.folds.changed()
	e.wrapBefore = false
	e.wrapOffset = 0
	e.stopPointerSelection()
	e.CancelPointerCapture()
	e.ClearHover()
	e.clearGoal()
	if r, hidden := e.hiddenFold(e.cursor.Line); hidden {
		e.cursor = Position{Line: r.Start, Col: e.lineVisualWidth(r.Start)}
		e.ClearSelection()
	}
	if r, hidden := e.hiddenFold(e.scrollY); hidden {
		e.scrollY = r.Start
	}
	e.ensureCursorVisible()
}

// FoldMemory is private client presentation, not an edit/history record. The
// exact content hash prevents restoring old ranges on different file bytes.
type FoldMemory struct {
	Source [sha256.Size]byte
	Ranges []FoldRange
}

func (e *Editor) FoldGeneration() uint64 { e.remapFolds(); return e.folds.generation }
func (e *Editor) ExportFolds() *FoldMemory {
	e.remapFolds()
	if len(e.folds.collapsed) == 0 {
		return nil
	}
	e.buf.ensureCache()
	return &FoldMemory{Source: sha256.Sum256(e.buf.cachedBytes), Ranges: slices.Clone(e.folds.collapsed)}
}
func (e *Editor) RestoreFolds(memory *FoldMemory) {
	if memory == nil || len(memory.Ranges) > syntax.MaxStructure {
		return
	}
	e.buf.ensureCache()
	if sha256.Sum256(e.buf.cachedBytes) != memory.Source {
		return
	}
	for _, r := range memory.Ranges {
		if r.Start < 0 || r.End <= r.Start || r.End >= e.LineCount() {
			return
		}
	}
	next := normalizeFolds(slices.Clone(memory.Ranges))
	if len(next) != len(memory.Ranges) {
		return
	}
	e.folds = foldState{buffer: e.buf, revision: e.Revision(), ranges: slices.Clone(next), collapsed: next, generation: e.folds.generation + 1}
	e.finishFoldChange()
}

func containsFold(ranges []FoldRange, r FoldRange) bool {
	_, found := slices.BinarySearchFunc(ranges, r, compareFold)
	return found
}
