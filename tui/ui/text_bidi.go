package ui

import (
	"unicode/utf8"

	"crude/tui/bidi"
	"crude/tui/rendering"
)

// textLineBidi is the bidirectional layout of one logical line of an
// editable text control. Each line is a paragraph with an automatic
// direction, like a textarea with dir="auto": a Persian paragraph is
// right-aligned and reads right to left, an English one does not.
type textLineBidi struct {
	layout *bidi.Layout
	// runeCol is the logical display column of every rune offset of the
	// line, plus its end. Only lines with a layout keep it.
	runeCol []int32
}

// analyzeTextLine resolves one logical line. flatStart is the line's first
// flat rune offset; an atomic range (a reference chip) that lies inside the
// line moves as one inline object. fallbackRTL gives a line without strong
// characters the direction of the paragraph before it.
func analyzeTextLine(runes []rune, flatStart int, atoms []AtomicRange, fallbackRTL bool) textLineBidi {
	if !bidi.Current().Arrange || !fallbackRTL && !bidi.NeedsLayoutRunes(runes) {
		return textLineBidi{}
	}
	text := string(runes)
	units := make([]bidi.Unit, 0, len(runes))
	runeCol := make([]int32, len(runes)+1)
	col, pos, off := 0, 0, 0
	for pos < len(runes) {
		if n, width, ok := atomAt(atoms, flatStart+pos, flatStart+len(runes), runes[pos:]); ok {
			units = append(units, bidi.Unit{Text: string(runes[pos : pos+n]), Width: width, Object: true})
			for k := 0; k < n; k++ {
				runeCol[pos+k] = int32(col)
				off += utf8.RuneLen(runes[pos+k])
			}
			col += width
			pos += n
			continue
		}
		cluster, width := rendering.FirstGrapheme(text[off:])
		if cluster == "" {
			break
		}
		n := utf8.RuneCountInString(cluster)
		for k := 0; k < n && pos+k < len(runes); k++ {
			runeCol[pos+k] = int32(col)
		}
		units = append(units, bidi.Unit{Text: cluster, Width: max(0, width)})
		col += max(0, width)
		pos += n
		off += len(cluster)
	}
	runeCol[len(runes)] = int32(col)
	layout := bidi.Analyze(units, bidi.Auto, fallbackRTL)
	if layout == nil {
		return textLineBidi{}
	}
	return textLineBidi{layout: layout, runeCol: runeCol}
}

// atomAt reports the atomic range that starts at flat offset pos and ends by
// lineEnd, with its rune length and painted width.
func atomAt(atoms []AtomicRange, pos, lineEnd int, rest []rune) (int, int, bool) {
	for _, ar := range atoms {
		if ar.Start == pos && ar.End > ar.Start && ar.End <= lineEnd {
			n := ar.End - ar.Start
			if n > len(rest) {
				return 0, 0, false
			}
			return n, rendering.VisibleWidth(string(rest[:n])), true
		}
	}
	return 0, 0, false
}

// line arranges the runes [start, start+n) of the line on a display line
// `span` columns wide. It is nil when the line paints as written.
func (lb textLineBidi) line(start, n, span int) *bidi.Line {
	if lb.layout == nil {
		return nil
	}
	start = min(max(0, start), len(lb.runeCol)-1)
	end := min(max(start, start+n), len(lb.runeCol)-1)
	// An oversized inline object occupies its own row. Arrange only the visible
	// part, so bidi cannot move cells in the neighboring control.
	col := int(lb.runeCol[start])
	return lb.layout.Line(col, min(int(lb.runeCol[end]), col+span), span)
}

// rtl reports the line's resolved direction.
func (lb textLineBidi) rtl() bool { return lb.layout.RTL() }
