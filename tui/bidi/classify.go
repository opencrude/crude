package bidi

import (
	"unicode/utf8"

	"github.com/go-text/typesetting/arabic"
	gt "github.com/go-text/typesetting/bidi"
)

// Triggers reports whether r can change the visual order of a paragraph
// that starts left-to-right: a strong right-to-left character (R, AL), an
// Arabic number (AN), or an explicit right-to-left embedding, override or
// isolate. Text without such a character paints exactly as written.
func Triggers(r rune) bool {
	if !mayTrigger(r) {
		return false
	}
	switch gt.LookupClass(r) {
	case gt.R, gt.AL, gt.AN, gt.RLE, gt.RLO, gt.RLI, gt.FSI:
		return true
	}
	return false
}

// mayTrigger is a cheap superset of Triggers. Every right-to-left letter,
// Arabic number and right-to-left control lies in these blocks;
// TestTriggerBlocksCoverUnicode checks that against the pinned data.
func mayTrigger(r rune) bool {
	switch {
	case r < 0x0590:
		return false
	case r <= 0x08FF:
		return true
	case r < 0x200F:
		return false
	case r <= 0x2068:
		return r == 0x200F || r >= 0x202B && r <= 0x202E || r == 0x2067 || r == 0x2068
	case r < 0xFB1D:
		return false
	case r <= 0xFEFF:
		return true
	case r < 0x10800:
		return false
	case r <= 0x10FFF:
		return true
	case r < 0x1E800:
		return false
	default:
		return r <= 0x1EFFF
	}
}

// NeedsLayout reports whether a left-to-right or automatic paragraph of s
// needs arrangement. It is the hot-path test painters run before anything
// else, and it only inspects non-ASCII bytes.
func NeedsLayout(s string) bool {
	for i := 0; i < len(s); {
		if s[i] < utf8.RuneSelf {
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if Triggers(r) {
			return true
		}
		i += size
	}
	return false
}

// Logical undoes the glyph substitution of an arranged cell: a joined
// presentation form returns to its letter, and a character mirrored at an
// odd level returns to its own glyph.
func Logical(glyph rune, level int, joined bool) rune {
	if joined {
		if r, ok := arabic.Nominal(glyph); ok {
			glyph = r
		}
	}
	if level%2 == 1 {
		if m, ok := gt.Mirror(glyph); ok {
			glyph = m
		}
	}
	return glyph
}

// LogicalOrder inverts rule L2 for a display line given the levels of its
// cells in visual order: it writes into order the visual index of each cell
// in logical order. Each L2 step reverses the maximal runs at or above one
// level; those runs keep their place, so applying the steps from the lowest
// odd level up restores the logical order.
func LogicalOrder(levels []int8, order []int) []int {
	order = order[:0]
	for i := range levels {
		order = append(order, i)
	}
	if len(levels) == 0 {
		return order
	}
	work := append([]int8(nil), levels...)
	highest, lowestOdd := int8(0), int8(127)
	for _, lv := range work {
		highest = max(highest, lv)
		if lv%2 == 1 {
			lowestOdd = min(lowestOdd, lv)
		}
	}
	for lv := lowestOdd; lv <= highest && lv > 0; lv++ {
		for i := 0; i < len(work); {
			if work[i] < lv {
				i++
				continue
			}
			j := i
			for j < len(work) && work[j] >= lv {
				j++
			}
			for a, b := i, j-1; a < b; a, b = a+1, b-1 {
				work[a], work[b] = work[b], work[a]
				order[a], order[b] = order[b], order[a]
			}
			i = j
		}
	}
	return order
}

// NeedsLayoutRunes is NeedsLayout for rune slices.
func NeedsLayoutRunes(rs []rune) bool {
	for _, r := range rs {
		if r >= 0x0590 && Triggers(r) {
			return true
		}
	}
	return false
}

// ParagraphRTL reports whether a paragraph of s resolves right-to-left
// under rules P2 and P3: its first strong character outside isolates is
// right-to-left.
func ParagraphRTL(s string) bool {
	if !NeedsLayout(s) {
		return false
	}
	a := analyzers.Get().(*analyzer)
	defer analyzers.Put(a)
	a.para.SegmentString(s, gt.Neutral)
	return a.para.ParagraphLevel()%2 == 1
}

// StrongDirection applies rule P2 to s: it reports whether its first strong
// character outside isolates is right-to-left, and found=false when s has no
// strong character at all (an empty line, digits, punctuation).
func StrongDirection(s string) (rtl, found bool) {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		for i := 0; i < len(s); i++ {
			if c := s[i] | 0x20; c >= 'a' && c <= 'z' {
				return false, true
			}
		}
		return false, false
	}
	a := analyzers.Get().(*analyzer)
	defer analyzers.Put(a)
	a.runes = append(a.runes[:0], []rune(s)...)
	a.para.SegmentAuto(a.runes, false)
	ltrLevel := a.para.ParagraphLevel()
	a.para.SegmentAuto(a.runes, true)
	if a.para.ParagraphLevel() != ltrLevel {
		// The level followed the fallback: no strong character decided it.
		return false, false
	}
	return ltrLevel%2 == 1, true
}
