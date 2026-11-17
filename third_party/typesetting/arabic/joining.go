// SPDX-License-Identifier: Unlicense OR BSD-3-Clause

package arabic

// Crude: the joining types and state machine below are copied verbatim from
// harfbuzz/ot_shape_arabic.go (go-text/typesetting v0.3.5), itself ported from
// HarfBuzz hb-ot-shaper-arabic.cc. See LICENSE.harfbuzz.

import (
	ucd "github.com/go-text/typesetting/internal/unicodedata"
)

// arabicJoining is a property used to shape Arabic runes.
// See the table arabicJoinings.
type arabicJoining byte

const (
	ajU          arabicJoining = 'U' // Un-joining, e.g. Full Stop
	ajR          arabicJoining = 'R' // Right-joining, e.g. Arabic Letter Dal
	ajAlaph      arabicJoining = 'a' // Alaph group (included in kind R)
	ajDalathRish arabicJoining = 'd' // Dalat Rish group (included in kind R)
	ajD          arabicJoining = 'D' // Dual-joining, e.g. Arabic Letter Ain
	ajC          arabicJoining = 'C' // Join-Causing, e.g. Tatweel, ZWJ
	ajL          arabicJoining = 'L' // Left-joining, i.e. fictional
	ajT          arabicJoining = 'T' // Transparent, e.g. Arabic Fatha
	ajG          arabicJoining = 'G' // Ignored, e.g. LRE, RLE, ZWNBSP
)

// index into arabicStateTable
const (
	joiningTypeU = iota
	joiningTypeL
	joiningTypeR
	joiningTypeD
	joiningGroupAlaph
	joiningGroupDalathRish
	numStateMachineCols
	joiningTypeT
	joiningTypeC = joiningTypeD
)

func getJoiningType(u rune, genCat ucd.GeneralCategory) uint8 {
	if jType, ok := arabicJoinings[u]; ok {
		switch jType {
		case ajU:
			return joiningTypeU
		case ajL:
			return joiningTypeL
		case ajR:
			return joiningTypeR
		case ajD:
			return joiningTypeD
		case ajAlaph:
			return joiningGroupAlaph
		case ajDalathRish:
			return joiningGroupDalathRish
		case ajT:
			return joiningTypeT
		case ajC:
			return joiningTypeC
		}
	}

	const mask = 1<<ucd.Mn | 1<<ucd.Me | 1<<ucd.Cf
	if 1<<genCat&mask != 0 {
		return joiningTypeT
	}
	return joiningTypeU
}

/* Same order as the feature array */
const (
	arabIsol = iota
	arabFina
	arabFin2
	araFin3
	arabMedi
	arabMed2
	arabInit

	arabNone

	/* We abuse the same byte for other things... */
	arabStchFixed
	arabStchRepeating
)

var arabicStateTable = [...][numStateMachineCols]struct {
	prevAction uint8
	currAction uint8
	nextState  uint16
}{
	/*   jt_U,          jt_L,          jt_R,          jt_D,          jg_ALAPH,      jg_DALATH_RISH */

	/* State 0: prev was U, not willing to join. */
	{{arabNone, arabNone, 0}, {arabNone, arabIsol, 2}, {arabNone, arabIsol, 1}, {arabNone, arabIsol, 2}, {arabNone, arabIsol, 1}, {arabNone, arabIsol, 6}},

	/* State 1: prev was R or ISOL/ALAPH, not willing to join. */
	{{arabNone, arabNone, 0}, {arabNone, arabIsol, 2}, {arabNone, arabIsol, 1}, {arabNone, arabIsol, 2}, {arabNone, arabFin2, 5}, {arabNone, arabIsol, 6}},

	/* State 2: prev was D/L in ISOL form, willing to join. */
	{{arabNone, arabNone, 0}, {arabNone, arabIsol, 2}, {arabInit, arabFina, 1}, {arabInit, arabFina, 3}, {arabInit, arabFina, 4}, {arabInit, arabFina, 6}},

	/* State 3: prev was D in FINA form, willing to join. */
	{{arabNone, arabNone, 0}, {arabNone, arabIsol, 2}, {arabMedi, arabFina, 1}, {arabMedi, arabFina, 3}, {arabMedi, arabFina, 4}, {arabMedi, arabFina, 6}},

	/* State 4: prev was FINA ALAPH, not willing to join. */
	{{arabNone, arabNone, 0}, {arabNone, arabIsol, 2}, {arabMed2, arabIsol, 1}, {arabMed2, arabIsol, 2}, {arabMed2, arabFin2, 5}, {arabMed2, arabIsol, 6}},

	/* State 5: prev was FIN2/FIN3 ALAPH, not willing to join. */
	{{arabNone, arabNone, 0}, {arabNone, arabIsol, 2}, {arabIsol, arabIsol, 1}, {arabIsol, arabIsol, 2}, {arabIsol, arabFin2, 5}, {arabIsol, arabIsol, 6}},

	/* State 6: prev was DALATH/RISH, not willing to join. */
	{{arabNone, arabNone, 0}, {arabNone, arabIsol, 2}, {arabNone, arabIsol, 1}, {arabNone, arabIsol, 2}, {arabNone, araFin3, 5}, {arabNone, arabIsol, 6}},
}
