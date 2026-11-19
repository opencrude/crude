package bidi

import (
	"strings"
	"unicode/utf8"

	gt "github.com/go-text/typesetting/bidi"
)

// TerminalText returns the text a terminal receives for one cell of an
// arranged line. Crude has already applied the cell's joining controls
// (ZWNJ, ZWJ after a right-to-left letter) and the line's direction
// controls (LRM, RLM, ALM, embeddings, overrides, isolates): the cell shows
// its joined form in visual order. Sent along, these controls only do harm.
// A terminal that looks for one font with every code point of the cell draws
// a replacement glyph (Ghostty shows "ـه‌" as "?"), one that shapes the cell
// can draw the letter twice (WezTerm), and one that arranges text itself
// reads them again.
//
// The cell keeps its source text, so copying painted text still returns the
// half-space. Joiners in other clusters (emoji ZWJ sequences, Indic
// conjuncts) are kept. A cell that holds only controls becomes a space.
// The result is a substring of text unless a control sits inside the
// cluster, so drawing a frame does not allocate.
func TerminalText(text string) string {
	if !hasFormatControl(text) {
		return text
	}
	base, _ := utf8.DecodeRuneInString(text)
	joined := rightToLeftLetter(base)
	drop := func(r rune) bool {
		return directionControl(r) || joined && (r == zwnj || r == zwj)
	}
	keep := len(text)
	for keep > 0 {
		r, size := utf8.DecodeLastRuneInString(text[:keep])
		if !drop(r) {
			break
		}
		keep -= size
	}
	inner := false
	for _, r := range text[:keep] {
		if drop(r) {
			inner = true
			break
		}
	}
	if !inner {
		if keep == 0 {
			return " "
		}
		return text[:keep]
	}
	var b strings.Builder
	b.Grow(keep)
	for _, r := range text[:keep] {
		if !drop(r) {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return " "
	}
	return b.String()
}

const (
	zwnj = '\u200C'
	zwj  = '\u200D'
)

// hasFormatControl reports whether text holds a joiner or a direction
// control. Every candidate is U+061C or lies in U+200C..U+2069, so only the
// lead bytes 0xD8 and 0xE2 need a closer look.
func hasFormatControl(text string) bool {
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case 0xD8, 0xE2:
			r, _ := utf8.DecodeRuneInString(text[i:])
			if r == zwnj || r == zwj || directionControl(r) {
				return true
			}
		}
	}
	return false
}

// directionControl reports the explicit directional formatting characters of
// UAX #9: marks, embeddings, overrides and isolates.
func directionControl(r rune) bool {
	switch {
	case r == '\u200E', r == '\u200F', r == '\u061C':
		return true
	case r >= '\u202A' && r <= '\u202E':
		return true
	case r >= '\u2066' && r <= '\u2069':
		return true
	}
	return false
}

// rightToLeftLetter reports a strong right-to-left base character: Hebrew,
// Arabic-script letters and their presentation forms.
func rightToLeftLetter(r rune) bool {
	if !mayTrigger(r) {
		return false
	}
	switch gt.LookupClass(r) {
	case gt.R, gt.AL:
		return true
	}
	return false
}
