# Terminal Display Width

This is the canonical reference for Unicode column measurement in Crude.
Crude targets modern grapheme-aware terminals, including Windows Terminal
1.22+, WezTerm, Ghostty, Contour, foot, and current Kitty.

## Invariant

Layout, the compositor, ultraviolet, and the terminal must agree on the
column width of every emitted string:

```
layout_width(s) == uv_cell_width(s) == terminal_advance(s)
```

If the first two differ, right-edge padding and scrollbars can leave stale
cells. If the terminal differs as well, cursor and hit-test positions drift.

## One Shared Method

Crude uses `ansi.GraphemeWidth` everywhere:

| Path | Role |
|------|------|
| `tui/rendering/width.go` | `WidthMethod`, `PlainWidth`, `FirstGrapheme` |
| `tui/rendering/ansi.go` | `VisibleWidth`: strip ANSI then measure |
| `tui/rendering/ansi_parser.go` | ANSI text to surface cells |
| `tui/surface/buffer.go` | Grapheme cell packing |
| `tui/compositor/output.go` | Surface cells back to ANSI |
| `third_party/bubbletea/` | UV screen buffer starts with `GraphemeWidth` |
| `third_party/xansi/` | Shared grapheme segmentation and corrections |

`go.mod` must retain both replacements:

```
replace charm.land/bubbletea/v2 => ./third_party/bubbletea
replace github.com/charmbracelet/x/ansi => ./third_party/xansi
```

Bubble Tea otherwise starts ultraviolet with `WcWidth`. That allows UV to
place a different number of cells from the layout code before terminal
capability negotiation completes.

## Width Rules

`x/ansi.FirstGraphemeCluster` segments UAX #29 extended grapheme clusters.
`correctClusterWidth` keeps a cluster within the terminal cell model:

| Input | Width |
|------|------:|
| ASCII, box drawing, `◆` | 1 |
| VS15 text presentation | 1 |
| CJK and fullwidth text | 2 |
| `☰` and U+2630-U+2637 trigrams | 2 |
| Emoji, VS16 emoji, flags, ZWJ emoji | 2 |
| A combining mark attached to a base | 0 additional cells |

Unicode 16 changed U+2630-U+2637 from neutral to wide. Current Windows
Terminal and the shared table advance `☰` by two cells. Do not add a local
one-cell exception for it.

Windows Terminal 1.22+ defaults to grapheme measurement. `WT_SESSION` only
identifies the host; it does not imply legacy Console or `wcswidth`
measurement. Those are user-selectable compatibility modes and are outside
Crude's target contract.

Unqualified emoji can intentionally remain narrow. Use a fully-qualified
emoji with U+FE0F when a two-cell emoji presentation is required, for example
`♻️` rather than `♻`.

## Rendering Pipeline

```
Widget ANSI string
    -> VisibleWidth / FirstGrapheme
    -> surface cells (wide cells have one continuation cell)
    -> compositor ANSI output
    -> Bubble Tea / ultraviolet with GraphemeWidth
    -> terminal
```

Cell metadata is lost when the compositor emits ANSI. Ultraviolet parses the
bytes again, so all layers must call the same `x/ansi` grapheme path.

For a width-2 cell, the compositor emits the grapheme once and skips its
continuation cell. Legacy ANSI renderers must do the same: writing a literal
space for the continuation makes that row one column too wide and shifts the
right-edge scrollbar left.

## Rules For Code

1. Use `rendering.VisibleWidth`, `PlainWidth`, and `FirstGrapheme` for layout,
   truncation, padding, and hit-testing.
2. Do not use `uniseg`, `runewidth`, or bare `lipgloss.Width` for layout.
3. Preserve the full grapheme text in a surface cell's `Content`; emitting only
   its first rune lets UV remeasure a different string.
4. If a glyph needs a correction, change vendored `x/ansi` so layout and UV
   receive it together. Do not add widget-specific padding.
5. Do not infer a width mode from `WT_SESSION`, `$TERM`, or a one-sided cursor
   probe.

## Verification

```bash
go test ./tui/rendering ./tui/compositor ./tui/editor -count=1
go test ./...
go build ./...
```

Key coverage:

| Test | Protects |
|------|----------|
| `tui/rendering/TestEmojiGraphemeWidth` | basic grapheme widths |
| `tui/rendering/TestTrigramsAreWide` | U+2630-U+2637 width |
| `tui/compositor/TestEndToEndWidthAgreement` | compositor and UV agreement |
| `tui/editor/TestViewWideGlyphsKeepScrollbarColumn` | wide glyphs cannot shift the editor scrollbar |
| `third_party/ultraviolet/TestCellbufMatchesXansiWidths` | UV and x/ansi agreement |
