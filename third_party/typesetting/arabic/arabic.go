// SPDX-License-Identifier: Unlicense OR BSD-3-Clause

// Package arabic resolves Arabic contextual joining and maps joined letters
// to the Unicode Arabic Presentation Forms.
//
// Crude addition. A character grid draws each cell on its own, so a text
// terminal cannot join letters after an application has arranged them in
// visual order. This package exposes HarfBuzz's Arabic joining state machine
// and its fallback presentation-form table (both copied verbatim into
// joining.go and arabic_table.go) for that case. It forms no ligatures: a
// ligature would join two grid cells into one glyph.
package arabic

import (
	"sync"

	ucd "github.com/go-text/typesetting/internal/unicodedata"
)

// Form is the contextual form a joining character takes.
type Form uint8

const (
	// None marks a character that does not change shape: a transparent
	// mark, a non-joining character, or text outside the joining scripts.
	None Form = iota
	Isolated
	Final
	Medial
	Initial
)

func formOf(action uint8) Form {
	switch action {
	case arabIsol:
		return Isolated
	case arabFina, arabFin2, araFin3:
		return Final
	case arabMedi, arabMed2:
		return Medial
	case arabInit:
		return Initial
	default:
		return None
	}
}

// Join resolves the contextual form of every rune of one run of text with
// HarfBuzz's Arabic joining state machine. Transparent characters (marks,
// format characters) are skipped and keep None. The caller passes one run:
// text that must not join across a boundary (for example a change of
// embedding level) belongs to separate calls. forms must be at least as long
// as text.
func Join(text []rune, forms []Form) {
	prev, state := -1, uint16(0)
	for i, u := range text {
		thisType := getJoiningType(u, ucd.LookupGeneralCategory(u))
		if thisType == joiningTypeT {
			forms[i] = None
			continue
		}
		entry := &arabicStateTable[state][thisType]
		if entry.prevAction != arabNone && prev != -1 {
			forms[prev] = formOf(entry.prevAction)
		}
		forms[i] = formOf(entry.currAction)
		prev = i
		state = entry.nextState
	}
}

// Joins reports whether r takes part in Arabic joining at all (joining type
// other than U and T).
func Joins(r rune) bool {
	switch getJoiningType(r, ucd.LookupGeneralCategory(r)) {
	case joiningTypeU, joiningTypeT:
		return false
	default:
		return true
	}
}

func shapeIndex(f Form) int {
	switch f {
	case Initial:
		return 0
	case Medial:
		return 1
	case Final:
		return 2
	case Isolated:
		return 3
	default:
		return -1
	}
}

// PresentationForm returns the Arabic Presentation Forms character that
// draws r in form f, and false when Unicode encodes no such character.
func PresentationForm(r rune, f Form) (rune, bool) {
	index := shapeIndex(f)
	if index < 0 || r < firstArabicShape || r > lastArabicShape {
		return r, false
	}
	if v := arabicShaping[r-firstArabicShape][index]; v != 0 {
		return rune(v), true
	}
	return r, false
}

var nominal struct {
	once sync.Once
	of   map[rune]rune
}

// Nominal returns the letter that PresentationForm mapped to the
// presentation character p, and false for any other character.
func Nominal(p rune) (rune, bool) {
	nominal.once.Do(func() {
		nominal.of = make(map[rune]rune, 4*len(arabicShaping))
		for i, forms := range arabicShaping {
			for _, v := range forms {
				if v != 0 {
					nominal.of[rune(v)] = firstArabicShape + rune(i)
				}
			}
		}
	})
	r, ok := nominal.of[p]
	return r, ok
}
