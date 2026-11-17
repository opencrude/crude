// Package bidi lays out bidirectional text for Crude's character grid.
//
// Terminals draw cells left to right and most of them cannot arrange
// right-to-left text without breaking a full-screen layout, so Crude arranges
// it before the cells reach the terminal: the Unicode Bidirectional Algorithm
// (UAX #9, pinned in third_party/typesetting) resolves embedding levels per
// paragraph; Line applies rules L1 and L2 to one display line; mirrored
// characters take their mirror glyph (L4); Arabic letters take their joined
// presentation forms, because a grid cannot join glyphs of separate cells.
//
// Text models stay logical. Painters write cells in logical order, then move
// them into visual order with the Line of that display line, so selection,
// search, diagnostics and the cursor travel with their characters. Pointer
// input maps a visual column back to a logical column through the same Line.
// See docs/reference/bidi.md.
package bidi

import (
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/go-text/typesetting/arabic"
	gt "github.com/go-text/typesetting/bidi"
)

// Direction is the requested base direction of a paragraph.
type Direction uint8

const (
	// Auto takes the direction of the first strong character (rules P2 and
	// P3), like the HTML dir="auto" attribute.
	Auto Direction = iota
	// LeftToRight forces a left-to-right paragraph, as code editors do.
	LeftToRight
	// RightToLeft forces a right-to-left paragraph.
	RightToLeft
)

// Presentation is the process-wide arrangement policy. Settings and the host
// terminal's reported BiDi mode decide it; every painter reads it here.
type Presentation struct {
	// Arrange puts right-to-left text in visual order inside Crude. When it
	// is off, cells keep logical order (the terminal arranges them, or
	// nothing does).
	Arrange bool
	// Join draws Arabic-script letters in their joined forms. It has no
	// effect unless Arrange is on.
	Join bool
}

type presentationState struct {
	value      Presentation
	generation uint64
}

var presentation atomic.Pointer[presentationState]

func init() {
	presentation.Store(&presentationState{value: Presentation{Arrange: true, Join: true}, generation: 1})
}

// Current returns the active presentation policy.
func Current() Presentation { return presentation.Load().value }

// Generation changes with every policy change. Caches of arranged text key on
// it, as width caches key on the terminal width profile generation.
func Generation() uint64 { return presentation.Load().generation }

// SetPresentation publishes a policy and reports whether it changed.
func SetPresentation(p Presentation) bool {
	for {
		old := presentation.Load()
		if old.value == p {
			return false
		}
		next := &presentationState{value: p, generation: old.generation + 1}
		if presentation.CompareAndSwap(old, next) {
			return true
		}
	}
}

// Unit is one logical display unit of a paragraph: a grapheme cluster, or an
// inline object (an inlay hint, a chip) that moves as one block and keeps its
// own internal order. Width is the unit's terminal columns; it may be zero.
type Unit struct {
	Text   string
	Width  int
	Object bool
}

// objectReplacement stands in for inline objects, as UAX #9 section 3.2
// specifies (bidi class ON).
const objectReplacement = '\uFFFC'

// Layout is one resolved paragraph. It is immutable and safe to share.
type Layout struct {
	rtl     bool
	level   []gt.Level
	class   []gt.Class
	col     []int32 // logical start column of each unit, plus the total width
	source  []string
	display []string // arranged glyphs of each unit; "" when unchanged
}

// RTL reports the paragraph's resolved base direction.
func (l *Layout) RTL() bool { return l != nil && l.rtl }

// Width is the paragraph's total width in columns.
func (l *Layout) Width() int {
	if l == nil || len(l.col) == 0 {
		return 0
	}
	return int(l.col[len(l.col)-1])
}

// Units returns the number of units the layout was built from.
func (l *Layout) Units() int {
	if l == nil {
		return 0
	}
	return len(l.level)
}

// analyzer holds the reusable working storage of one analysis.
type analyzer struct {
	para  gt.Paragraph
	runes []rune
	owner []int32
	level []gt.Level
	forms []arabic.Form
}

var analyzers = sync.Pool{New: func() any { return new(analyzer) }}

// Analyze resolves one paragraph. It returns nil when the paragraph needs no
// arrangement: Crude does not arrange text, or the text is left-to-right
// throughout. Callers treat nil as "paint in logical order". fallbackRTL
// gives a paragraph without strong characters (for example an empty line in
// a Persian document) a right-to-left direction under Auto.
func Analyze(units []Unit, dir Direction, fallbackRTL bool) *Layout {
	policy := Current()
	if !policy.Arrange || len(units) == 0 && dir != RightToLeft && (dir != Auto || !fallbackRTL) {
		return nil
	}
	if dir == LeftToRight || dir == Auto && !fallbackRTL {
		needed := false
		for i := range units {
			if !units[i].Object && NeedsLayout(units[i].Text) {
				needed = true
				break
			}
		}
		if !needed {
			return nil
		}
	}
	a := analyzers.Get().(*analyzer)
	defer analyzers.Put(a)
	return a.analyze(units, dir, fallbackRTL, policy.Join)
}

// AnalyzeString resolves a paragraph of plain text, one unit per grapheme
// cluster measured with the shared terminal width profile.
func AnalyzeString(s string, dir Direction, fallbackRTL bool) *Layout {
	if !Current().Arrange || dir != RightToLeft && (dir != Auto || !fallbackRTL) && !NeedsLayout(s) {
		return nil
	}
	return Analyze(Clusters(s, nil), dir, fallbackRTL)
}

// Clusters appends the grapheme clusters of s with their terminal widths.
func Clusters(s string, dst []Unit) []Unit {
	for len(s) > 0 {
		cluster, width := ansi.FirstGraphemeCluster(s, ansi.GraphemeWidth)
		if cluster == "" {
			break
		}
		dst = append(dst, Unit{Text: cluster, Width: width})
		s = s[len(cluster):]
	}
	return dst
}

func (a *analyzer) analyze(units []Unit, dir Direction, fallbackRTL, join bool) *Layout {
	a.runes, a.owner = a.runes[:0], a.owner[:0]
	for i := range units {
		if units[i].Object || units[i].Text == "" {
			a.runes = append(a.runes, objectReplacement)
			a.owner = append(a.owner, int32(i))
			continue
		}
		for _, r := range units[i].Text {
			a.runes = append(a.runes, r)
			a.owner = append(a.owner, int32(i))
		}
	}
	var runs gt.Runs
	switch dir {
	case LeftToRight:
		runs = a.para.Segment(a.runes, gt.LeftToRight)
	case RightToLeft:
		runs = a.para.Segment(a.runes, gt.RightToLeft)
	default:
		runs = a.para.SegmentAuto(a.runes, fallbackRTL)
	}
	base := a.para.ParagraphLevel()
	a.level = append(a.level[:0], runs.Levels()...)
	for len(a.level) < len(a.runes) {
		a.level = append(a.level, base)
	}

	n := len(units)
	l := &Layout{
		rtl:     base%2 == 1,
		level:   make([]gt.Level, n),
		class:   make([]gt.Class, n),
		col:     make([]int32, n+1),
		source:  make([]string, n),
		display: make([]string, n),
	}
	col := int32(0)
	for i := range units {
		l.col[i] = col
		col += int32(max(0, units[i].Width))
		l.source[i] = units[i].Text
		l.level[i] = base
		l.class[i] = gt.ON
	}
	l.col[n] = col
	for i, r := range a.runes {
		u := a.owner[i]
		if i == 0 || a.owner[i-1] != u {
			l.level[u] = a.level[i]
			if units[u].Object || units[u].Text == "" {
				l.class[u] = gt.ON
			} else {
				l.class[u] = gt.LookupClass(r)
			}
		}
	}

	// Rule L4 and Arabic joining work on code points; the result is the
	// glyph text of each unit. Marks stay after their base character.
	if join {
		if cap(a.forms) < len(a.runes) {
			a.forms = make([]arabic.Form, len(a.runes))
		}
		a.forms = a.forms[:len(a.runes)]
		for start := 0; start < len(a.runes); {
			end := start + 1
			for end < len(a.runes) && a.level[end] == a.level[start] {
				end++
			}
			arabic.Join(a.runes[start:end], a.forms[start:end])
			start = end
		}
	}
	trivial := !l.rtl
	// All arranged glyph strings share one backing string.
	type span struct{ unit, from, to int }
	var glyphs []byte
	var spans []span
	for i := 0; i < len(a.runes); {
		u := a.owner[i]
		end := i + 1
		for end < len(a.runes) && a.owner[end] == u {
			end++
		}
		if l.level[u]%2 == 1 {
			trivial = false
		}
		if !units[u].Object && units[u].Text != "" {
			changed := false
			glyph := a.runes[i]
			if a.level[i]%2 == 1 {
				if m, ok := gt.Mirror(glyph); ok {
					glyph, changed = m, true
				}
			}
			if join {
				if p, ok := arabic.PresentationForm(glyph, a.forms[i]); ok {
					glyph, changed = p, true
				}
			}
			if changed {
				from := len(glyphs)
				glyphs = utf8.AppendRune(glyphs, glyph)
				for _, r := range a.runes[i+1 : end] {
					glyphs = utf8.AppendRune(glyphs, r)
				}
				spans = append(spans, span{int(u), from, len(glyphs)})
				trivial = false
			}
		}
		i = end
	}
	if trivial {
		return nil
	}
	backing := string(glyphs)
	for _, s := range spans {
		l.display[s.unit] = backing[s.from:s.to]
	}
	return l
}

// Block is one run of cells on a display line: a unit (or the visible part
// of one), or one padding column after the text.
type Block struct {
	// Unit is the unit index in the layout, or -1 for padding.
	Unit int
	// Logical is the block's first column in logical order and Visual its
	// first column on screen, both relative to the start of the line.
	Logical, Visual int
	// Start is the unit's own first logical column relative to the line. It
	// is below Logical when the line starts inside the unit.
	Start int
	Width int
	Level gt.Level
	// Source and Display are the unit's logical text and arranged glyphs.
	// Display is empty when the glyphs are unchanged.
	Source, Display string
}

// Line is the visual arrangement of one display line of a paragraph.
type Line struct {
	blocks   []Block // visual order
	toVis    []int32 // logical column -> visual column
	toLog    []int32 // visual column -> logical column
	text     int     // logical columns that hold text; the rest is padding
	rtl      bool
	identity bool
}

// Line arranges the units that cover logical columns [colStart, colEnd) on a
// display line `span` columns wide. Columns past colEnd-colStart are padding:
// they take the paragraph direction, which right-aligns a right-to-left
// paragraph. A wrapped row and a horizontally scrolled window are both just
// display lines of the same paragraph.
func (l *Layout) Line(colStart, colEnd, span int) *Line {
	if l == nil {
		return nil
	}
	colStart = max(0, colStart)
	colEnd = max(colStart, min(colEnd, l.Width()))
	text := colEnd - colStart
	span = max(span, text)
	base := gt.Level(0)
	if l.rtl {
		base = 1
	}
	ln := &Line{text: text, rtl: l.rtl}
	type item struct {
		Block
		class gt.Class
	}
	items := make([]item, 0, span)
	for u := range l.level {
		from, to := int(l.col[u]), int(l.col[u+1])
		if to <= colStart || from >= colEnd || from == to {
			continue
		}
		lo, hi := max(from, colStart), min(to, colEnd)
		items = append(items, item{Block: Block{
			Unit: u, Logical: lo - colStart, Start: from - colStart, Width: hi - lo,
			Level: l.level[u], Source: l.source[u], Display: l.display[u],
		}, class: l.class[u]})
	}
	for c := text; c < span; c++ {
		items = append(items, item{Block: Block{Unit: -1, Logical: c, Start: c, Width: 1, Level: base}, class: gt.WS})
	}

	// Rule L1: segment and paragraph separators, the whitespace before
	// them, and the whitespace at the end of the line take the paragraph
	// level.
	trailing := true
	for i := len(items) - 1; i >= 0; i-- {
		switch class := items[i].class; {
		case class == gt.S || class == gt.B:
			items[i].Level = base
			trailing = true
		case lineWhitespace(class):
			if trailing {
				items[i].Level = base
			}
		default:
			trailing = false
		}
	}

	// Rule L2: from the highest level down to the lowest odd level, reverse
	// every run at that level or higher.
	highest, lowestOdd := gt.Level(0), gt.Level(127)
	for i := range items {
		lv := items[i].Level
		highest = max(highest, lv)
		if lv%2 == 1 {
			lowestOdd = min(lowestOdd, lv)
		}
	}
	for lv := highest; lv >= lowestOdd && lv > 0; lv-- {
		for i := 0; i < len(items); {
			if items[i].Level < lv {
				i++
				continue
			}
			j := i
			for j < len(items) && items[j].Level >= lv {
				j++
			}
			for a, b := i, j-1; a < b; a, b = a+1, b-1 {
				items[a], items[b] = items[b], items[a]
			}
			i = j
		}
	}

	ln.blocks = make([]Block, len(items))
	ln.toVis = make([]int32, span)
	ln.toLog = make([]int32, span)
	ln.identity = true
	visual := 0
	for i := range items {
		b := items[i].Block
		b.Visual = visual
		for k := 0; k < b.Width; k++ {
			ln.toVis[b.Logical+k] = int32(visual + k)
			ln.toLog[visual+k] = int32(b.Logical + k)
		}
		if b.Logical != visual || b.Display != "" {
			ln.identity = false
		}
		visual += b.Width
		ln.blocks[i] = b
	}
	return ln
}

// lineWhitespace lists the classes rule L1 treats as whitespace: WS, the
// isolate controls, and the characters rule X9 removes.
func lineWhitespace(c gt.Class) bool {
	switch c {
	case gt.WS, gt.FSI, gt.LRI, gt.RLI, gt.PDI, gt.BN, gt.LRE, gt.RLE, gt.LRO, gt.RLO, gt.PDF:
		return true
	}
	return false
}

// Blocks returns the line's blocks in visual order. The slice is shared.
func (ln *Line) Blocks() []Block {
	if ln == nil {
		return nil
	}
	return ln.blocks
}

// Span is the line's width in columns.
func (ln *Line) Span() int {
	if ln == nil {
		return 0
	}
	return len(ln.toLog)
}

// TextWidth is the number of logical columns that hold text.
func (ln *Line) TextWidth() int {
	if ln == nil {
		return 0
	}
	return ln.text
}

// RTL reports the paragraph direction of the line.
func (ln *Line) RTL() bool { return ln != nil && ln.rtl }

// Identity reports whether the line paints exactly as written.
func (ln *Line) Identity() bool { return ln == nil || ln.identity }

// Logical maps a visual column to the logical column painted there.
// Columns outside the line map to themselves.
func (ln *Line) Logical(visual int) int {
	if ln == nil || visual < 0 || visual >= len(ln.toLog) {
		return visual
	}
	return int(ln.toLog[visual])
}

// Visual maps a logical column to the visual column that shows it.
func (ln *Line) Visual(logical int) int {
	if ln == nil || logical < 0 || logical >= len(ln.toVis) {
		return logical
	}
	return int(ln.toVis[logical])
}

// Step moves a caret one cell to the right or left on screen. The caret sits
// on the unit that starts at logical column `logical`; the end of the text
// (logical == TextWidth) sits on the first padding column. It returns the
// logical start column of the unit on that side, or false at the edge of the
// line, where the caller continues in text order: the next line for the
// paragraph's forward direction, the previous line for the other.
func (ln *Line) Step(logical int, right bool) (int, bool) {
	if ln == nil {
		return logical, false
	}
	at := ln.blockAt(ln.Visual(logical))
	if at < 0 {
		return logical, false
	}
	for next := at; ; {
		if right {
			next++
		} else {
			next--
		}
		if next < 0 || next >= len(ln.blocks) {
			return logical, false
		}
		b := ln.blocks[next]
		if b.Width == 0 {
			continue
		}
		if b.Unit < 0 {
			if b.Logical != ln.text {
				return logical, false
			}
			return ln.text, true
		}
		return b.Start, true
	}
}

func (ln *Line) blockAt(visual int) int {
	for i := range ln.blocks {
		b := &ln.blocks[i]
		if visual >= b.Visual && visual < b.Visual+b.Width {
			return i
		}
	}
	return -1
}

var visualCaret atomic.Bool

func init() { visualCaret.Store(true) }

// VisualCaret reports whether Left and Right move the caret on screen in
// right-to-left text (the default), instead of in text order.
func VisualCaret() bool { return visualCaret.Load() }

// SetVisualCaret publishes the caret movement preference.
func SetVisualCaret(visual bool) { visualCaret.Store(visual) }

// ArrowForward reports whether an arrow key moves forward in text order on
// a line: the arrow that points the way the line reads. Word and edge
// movement use it, so Left means "next" in a right-to-left paragraph when
// the caret moves on screen.
func ArrowForward(right bool, ln *Line) bool {
	if ln == nil || !VisualCaret() {
		return right
	}
	return right != ln.RTL()
}
