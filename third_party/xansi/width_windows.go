package ansi

import "unicode/utf8"

// wtRuneWidth returns the column advance Windows Terminal (legacy console
// width model, UAX 2027 OFF) gives a single codepoint.
//
// Empirically measured against a real Windows Terminal session (WT_SESSION
// set, no ?2027h):
//
//   - Trigrams U+2630..U+2637 (☰ ☱ ☲ ☳ ☴ ☵ ☶ ☷) advance 2 cells, NOT 1
//     as the runewidth/UAX#11 Neutral classification would suggest. The
//     earlier "1" override was wrong; observed on WT 1.20+ these glyphs
//     paint wide (2 cells) and the cursor advances 2. Returning 1 here
//     caused a per-line −1 in the cellbuf and left a stray cell at the
//     right edge of every line below the trigram in the file (the "6"
//     ghost column on scrolling).
//   - Regional indicators U+1F1E6..U+1F1FF advance 2 each; WT does NOT
//     cluster flag pairs (🇯🇵 = 4, 🇺🇸 = 4).
//   - Zero-width codepoints (ZWJ U+200D, VS15 U+FE0E, VS16 U+FE0F,
//     combining marks) advance 1 column in WT's legacy width model
//     (the terminal never advances 0 cells).
//   - Everything else: runewidth's value.
//
// Verified samples (probe vs wtRuneWidth):
//
//	U+0061 a          1     U+200D ZWJ        1
//	U+FE0F VS16       1     U+0300 combining  1
//	U+2630 ☰          2     U+26A1 ⚡         2
//	U+267B ♻          1     U+2B50 ⭐         2
//	U+1F1EF 🇯         2     U+1F1F5 🇵         2
//	U+1F600 😀        2     U+4E2D 中         2
func wtRuneWidth(r rune) int {
	// Trigrams: WT advances 2 (wide glyph).
	if r >= 0x2630 && r <= 0x2637 {
		return 2
	}
	// Regional indicators: WT advances 2 each (no flag clustering).
	if r >= 0x1F1E6 && r <= 0x1F1FF {
		return 2
	}
	w := wcOptions.RuneWidth(r)
	if w < 0 {
		w = 0
	}
	if w == 0 {
		// Zero-width codepoints (ZWJ, VS15, VS16, combining marks, etc.)
		// still advance the cursor by 1 column in Windows Terminal's
		// legacy width model.
		return 1
	}
	return w
}

// decodeFirstWtCodepoint decodes the first UTF-8 codepoint of b and returns
// its (rune, byte-length, wtRuneWidth). It does NOT perform grapheme
// clustering — Windows Terminal without UAX 2027 measures per codepoint.
// Invalid UTF-8 yields (RuneError, 1, 1) so the caller advances and the
// stream resynchronizes.
func decodeFirstWtCodepoint(b string) (r rune, n int, w int) {
	if len(b) == 0 {
		return 0, 0, 0
	}
	r, size := utf8.DecodeRuneInString(b)
	if r == utf8.RuneError && size == 1 {
		return r, 1, 1
	}
	return r, size, wtRuneWidth(r)
}