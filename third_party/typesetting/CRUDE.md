# go-text/typesetting subset

Pinned from `github.com/go-text/typesetting` v0.3.5 (Unicode 17.0.0;
`Unlicense OR BSD-3-Clause`, upstream `LICENSE` retained). Crude uses it for
the Unicode Bidirectional Algorithm (UAX #9) and for Arabic contextual
joining. See [right-to-left text](../../docs/reference/bidi.md).

Only these upstream packages are kept, byte for byte:

- `bidi/` — the UAX #9 port of the Unicode reference implementation. Its
  upstream tests run the complete `BidiTest.txt` and `BidiCharacterTest.txt`
  conformance files. They are stored gzip-compressed in `bidi/test/`; the
  test reads them through a small `gunzip` helper and is otherwise unchanged.
- `internal/unicodedata/`, `language/` — the generated Unicode tables the
  algorithm reads.
- `testutils/` — only the assertion helpers. The font fixture loader and its
  `typesetting-utils` dependency are not vendored.

Local delta:

- `bidi/bidi.go` records the resolved paragraph level on the two early
  returns (empty text, all-LTR short cut). Upstream left the previous value.
- `bidi/crude.go` exports read-only accessors: `LookupClass` (Bidi_Class),
  `Mirror` (Bidi_Mirroring_Glyph, rule L4), `Paragraph.ParagraphLevel` and
  `Runs.Levels` (the exact per-character levels; `Runs` merges adjacent levels
  of one direction, which rule L2 cannot use).
- UAX #9 HL1: `Paragraph.SegmentAuto` gives a paragraph without strong
  characters a right-to-left fallback level. `core.go` splits rule P2 out of
  `determineParagraphEmbeddingLevel` into `firstStrongType` so the paragraph
  level can use the fallback while FSI resolution (X5c) keeps P3's default.
- `arabic/` is new. `joining.go` and `arabic_table.go` are verbatim copies of
  the joining types, the joining state machine and the fallback
  presentation-form table from upstream `harfbuzz/ot_shape_arabic.go` and
  `harfbuzz/ot_shape_arabic_table.go` (HarfBuzz, MIT, `arabic/LICENSE.harfbuzz`).
  `arabic.go` runs that state machine over one run and maps forms to Unicode
  Arabic Presentation Forms. It forms no ligatures, because a ligature would
  merge two terminal cells.

No other algorithm is reimplemented here. Line reordering (rules L1–L2) and
cell placement live in `crude/tui/bidi`, which tests its ordering against the
visual-order field of the same pinned `BidiCharacterTest.txt`.

Verify with `cd third_party/typesetting && go test ./...`.
