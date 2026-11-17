// SPDX-License-Identifier: Unlicense OR BSD-3-Clause

package bidi

// Crude additions: read-only accessors for the pinned Unicode data and the
// resolved paragraph level. The algorithm itself is unchanged upstream code.

import (
	"math/bits"

	ucd "github.com/go-text/typesetting/internal/unicodedata"
)

// Class is the Bidi_Class property of a character (UAX #9, Table 4).
type Class uint8

// The Bidi_Class values. Unknown is returned for code points the pinned
// Unicode data does not classify.
const (
	Unknown Class = iota
	L
	R
	AL
	EN
	ES
	ET
	AN
	CS
	NSM
	BN
	B
	S
	WS
	ON
	LRE
	LRO
	RLE
	RLO
	PDF
	LRI
	RLI
	FSI
	PDI
)

// classOfBit maps the bit index of the internal one-hot class flag.
var classOfBit = func() (out [32]Class) {
	for flag, class := range map[ucd.BidiClass]Class{
		ucd.BD_L: L, ucd.BD_R: R, ucd.BD_AL: AL, ucd.BD_EN: EN, ucd.BD_ES: ES,
		ucd.BD_ET: ET, ucd.BD_AN: AN, ucd.BD_CS: CS, ucd.BD_NSM: NSM, ucd.BD_BN: BN,
		ucd.BD_B: B, ucd.BD_S: S, ucd.BD_WS: WS, ucd.BD_ON: ON, ucd.BD_LRE: LRE,
		ucd.BD_LRO: LRO, ucd.BD_RLE: RLE, ucd.BD_RLO: RLO, ucd.BD_PDF: PDF,
		ucd.BD_LRI: LRI, ucd.BD_RLI: RLI, ucd.BD_FSI: FSI, ucd.BD_PDI: PDI,
	} {
		out[bits.TrailingZeros32(uint32(flag))] = class
	}
	return out
}()

// LookupClass returns the Bidi_Class of r from the same data the algorithm
// uses.
func LookupClass(r rune) Class {
	flag, _ := ucd.LookupBidiClass(r)
	if flag == 0 {
		return Unknown
	}
	return classOfBit[bits.TrailingZeros32(uint32(flag))]
}

// Mirror returns the Bidi_Mirroring_Glyph of r (BidiMirroring.txt), used by
// rule L4 for characters at odd embedding levels.
func Mirror(r rune) (rune, bool) {
	m := ucd.LookupMirrorChar(r)
	return m, m != r
}

// ParagraphLevel reports the paragraph embedding level resolved by the last
// Segment call: the requested direction, or rules P2 and P3 for Neutral.
func (p *Paragraph) ParagraphLevel() Level { return p.embeddingLevel }

// SegmentAuto is Segment with a Neutral default direction (rules P2 and P3)
// whose fallback, for a paragraph without any strong character, is
// right-to-left when fallbackRTL is set. UAX #9 HL1 allows this, for example
// so that an empty line keeps the direction of the paragraph before it.
func (p *Paragraph) SegmentAuto(text []rune, fallbackRTL bool) Runs {
	p.text = append(p.text[:0], text...)
	p.fallbackLevel = 0
	if fallbackRTL {
		p.fallbackLevel = 1
	}
	return p.segment(Neutral)
}

// Levels returns the resolved embedding level of every character, valid
// until the next Segment call. Runs merges adjacent levels of the same
// direction (0 and 2, for example); rule L2 needs the exact levels.
func (r *Runs) Levels() []Level { return r.levels }
