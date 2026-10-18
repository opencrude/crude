package ui

import (
	"crude/tui/rendering"
	"unicode/utf8"
)

// Pointer hits own complete source units; keyboard extensions own insertion
// boundaries. Keeping these separate avoids adding a second character to a
// Shift+Left selection while still including the initially pressed drag cell.
type textSelectionUnit uint8

const (
	textSelectionCharacter textSelectionUnit = iota
	textSelectionWord
	textSelectionLine
	textSelectionCaret
)

type textSelectionGesture struct {
	active     bool
	moved      bool
	unit       textSelectionUnit
	start, end int
}

func (g textSelectionGesture) project(start, end int) (anchor, head int) {
	if g.unit == textSelectionCaret {
		return g.start, start
	}
	if g.unit == textSelectionCharacter {
		if start < g.start {
			return g.end, start
		}
		return g.start, start
	}
	if start < g.start {
		return g.end, start
	}
	return g.start, end
}

func textWordRange(runes []rune, pos int) (int, int) {
	if len(runes) == 0 {
		return 0, 0
	}
	pos = min(max(0, pos), len(runes)-1)
	start, end, lastClass := 0, 0, -1
	for rest := string(runes); rest != ""; {
		cluster, _ := rendering.FirstGrapheme(rest)
		if cluster == "" {
			break
		}
		r, _ := utf8.DecodeRuneInString(cluster)
		class := runeClass(r)
		if class != lastClass {
			if pos < end {
				break
			}
			start, lastClass = end, class
		}
		end += utf8.RuneCountInString(cluster)
		rest = rest[len(cluster):]
	}
	return start, end
}
