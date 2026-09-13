# Terminal Display Width

> Status: Current canonical reference.

This is the canonical reference for Unicode column measurement in Crude.
Crude supports modern grapheme-aware terminals, legacy width behavior, WSL,
SSH, and terminal multiplexers without deriving width from the process OS.

## Invariant

Layout, the compositor, ultraviolet, and the terminal must agree on the
column width of every emitted string:

```
layout_width(s) == uv_cell_width(s) == terminal_advance(s)
```

If the first two differ, right-edge padding and scrollbars can leave stale
cells. If the terminal differs as well, cursor and hit-test positions drift.

## One Shared Method And Profile

Crude uses `ansi.GraphemeWidth` everywhere. A process-wide immutable terminal
profile refines that method after asynchronous calibration:

| Path | Role |
|------|------|
| `tui/rendering/width.go` | `WidthMethod`, `PlainWidth`, `FirstGrapheme` |
| `tui/rendering/ansi.go` | `VisibleWidth`: strip ANSI then measure |
| `tui/rendering/ansi_parser.go` | ANSI text to surface cells |
| `tui/surface/buffer.go` | Grapheme cell packing |
| `tui/compositor/output.go` | Surface cells back to ANSI |
| `third_party/bubbletea/` | UV screen buffer starts with `GraphemeWidth` |
| `third_party/bubbletea/width_probe.go` | Invisible CPR calibration of the effective grid |
| `third_party/xansi/` | Shared grapheme segmentation and atomic runtime corrections |

`go.mod` must retain all three replacements:

```
replace charm.land/bubbletea/v2 => ./third_party/bubbletea
replace github.com/charmbracelet/x/ansi => ./third_party/xansi
replace github.com/charmbracelet/ultraviolet => ./third_party/ultraviolet
```

Bubble Tea otherwise starts ultraviolet with `WcWidth`. That allows UV to place
a different number of cells from layout before negotiation completes.

## Startup Guess And Async Calibration

Initial rendering never waits for terminal I/O. The starting guess is the
Unicode-16 grapheme model used by current Kitty, Alacritty, Windows Terminal,
WezTerm, Ghostty, Contour, and foot releases. It is deliberately independent of
`runtime.GOOS`: a Linux process may be running under WSL/Windows Terminal, SSH,
tmux, screen, or a nested combination.

About 50ms after initial paint, patched Bubble Tea sends one calibration burst:

1. Begin synchronized output, save the cursor, and enable Unicode Core mode
   2027 when supported.
2. At a scratch row, print each unique probe grapheme at column 1 and request a
   Cursor Position Report (CPR).
3. Erase the scratch row, restore the cursor, and end synchronized output.
4. Collect ordered zero-based CPR columns for up to 750ms without delaying
   keyboard input or initial rendering.
5. Publish an immutable `ansi.TerminalWidthProfile` and force one full redraw
   only if a measurement differs from the guess.

The burst contains Crude's product chrome plus representative disputed classes:
East Asian ambiguous text, Unicode-version changes such as `☰`, combining
marks, ZWJ emoji, flags, standalone modifiers, format characters, keycaps,
text/emoji variation pairs, BMP media/control and geometric symbols, persistent
Nerd Font rail glyphs, and complex language graphemes. Unqualified `⏺` is an
important exact probe: some terminal/font grids render it in one cell despite
Unicode's default two-cell emoji presentation; an unmeasured mismatch leaves a
one-cell stale tail on every changed editor or chat row containing it.
Supporting terminals never display the scratch writes because synchronized
output brackets them; save/erase/restore protects legacy terminals.

Inside tmux or screen, CPR normally reports the multiplexer's effective grid.
That is the grid the application must match. Exact corrections handle product
glyphs; an ambiguous-width measurement generalizes to that Unicode class; a
measured multi-cell ZWJ sequence enables legacy codepoint-sum fallback. The
calibration result is logged as `TERM width_calibrated` for diagnosis.

## Width Rules

`x/ansi.FirstGraphemeCluster` segments UAX #29 extended grapheme clusters.
`correctClusterWidth` applies the measured profile before the default model:

| Input | Width |
|------|------:|
| ASCII | 1 |
| Box drawing, symbols, ambiguous text | measured, otherwise Unicode-16 default |
| VS15 text presentation | terminal/profile dependent |
| CJK and fullwidth text | 2 |
| `☰` and U+2630-U+2637 trigrams | measured, initial guess 2 |
| Emoji, VS16 emoji, flags, ZWJ emoji | normally 2; measured legacy grids may be wider |
| A combining mark attached to a base | 0 additional cells |

Unicode 16 changed U+2630-U+2637 from neutral to wide. This is exactly why OS,
`TERM`, `TERM_PROGRAM`, and `WT_SESSION` are hints rather than width contracts.
The terminal or multiplexer measurement wins.

Unqualified emoji can intentionally remain narrow. Use a fully-qualified
emoji with U+FE0F when a two-cell emoji presentation is required, for example
`♻️` rather than `♻`.

## Rendering Pipeline

```
Widget ANSI string
    -> VisibleWidth / FirstGrapheme
    -> surface cells (multi-column cells have Width-1 continuations)
    -> compositor ANSI output
    -> Bubble Tea / ultraviolet with GraphemeWidth
    -> terminal
```

Cell metadata is lost when the compositor emits ANSI. Ultraviolet parses the
bytes again, so all layers must call the same `x/ansi` grapheme path.

For every multi-column cell, the compositor emits the grapheme once and skips
all continuation cells. Surface composition and ultraviolet accept measured
widths beyond two for legacy effective grids; writing literal spaces for
continuations makes the row too wide and causes stale-cell artifacts.

Multi-column graphemes are also atomic under occlusion. If a floating border,
dropdown, or later component touches either a primary or any continuation
column, the complete old grapheme span is blanked before the new cell is
written. `CellBuffer.Set`, `FillRect`, `Blit`, opaque blit, alpha blend, and
merge all enforce this rule. Half-graphemes and orphan continuations must never
reach compositor ANSI or UV; this is the floating-window-over-emoji artifact.

## Rules For Code

1. Use `rendering.VisibleWidth`, `PlainWidth`, and `FirstGrapheme` for layout,
   truncation, padding, and hit-testing.
2. Do not use `uniseg`, `runewidth`, or bare `lipgloss.Width` for layout.
3. Preserve the full grapheme text in a surface cell's `Content`; emitting only
   its first rune lets UV remeasure a different string.
4. Never add platform or widget-specific width exceptions. Add a calibration
   representative or a shared x/ansi class rule so layout and UV change
   together.
5. Never infer width from `runtime.GOOS`, `WT_SESSION`, `$TERM`, or
   `TERM_PROGRAM`; they cannot identify the effective grid through WSL, SSH,
   or multiplexers.
6. A cursor probe is valid only when its correction is installed in the shared
   x/ansi profile consumed by layout, surfaces, Bubble Tea, and UV together.

## Verification

```bash
go test ./tui/rendering ./tui/compositor ./tui/editor -count=1
go test ./... # in third_party/xansi, bubbletea, and ultraviolet
go test ./...
go build ./...
```

Key coverage:

| Test | Protects |
|------|----------|
| `tui/rendering/TestEmojiGraphemeWidth` | basic grapheme widths |
| `third_party/bubbletea/TestTerminalWidthProbeBuildsMeasuredProfile` | CPR measurements become one shared profile |
| `third_party/xansi/TestTrigramWidthUsesModernGuessThenTerminalMeasurement` | runtime correction replaces OS policy |
| `tui/compositor/TestEndToEndWidthAgreement` | compositor and UV agreement |
| `tui/compositor/TestFloatingBorderOccludesEmojiWithoutHalfGrapheme` | floating borders cannot expose half an underlying emoji |
| `tui/surface/TestBlitBorderAtomicallyOccludesUnderlyingWideGrapheme` | all layer overlap is grapheme-atomic |
| `tui/editor/TestViewWideGlyphsKeepScrollbarColumn` | wide glyphs cannot shift the editor scrollbar |
| `tui/editor/TestRenderWideGlyphsKeepScrollbarColumn` | Markdown/`✅` survives compositor→UV with the scrollbar at the right edge |
| `third_party/ultraviolet/TestCellbufMatchesXansiWidths` | UV and x/ansi agreement |
| `third_party/ultraviolet/TestStyledStringAcceptsMeasuredWidthBeyondFour` | legacy measured clusters remain representable |
