# Right-to-Left and Bidirectional Text

> Status: Current reference — how Crude lays out Arabic, Persian, Hebrew and
> mixed-direction text on a character grid, and who owns each decision.

Crude arranges right-to-left text itself. A terminal draws cells left to
right; terminals that arrange bidirectional text on their own do it per screen
row, which mixes panes and breaks a full-screen layout, and most terminals do
not arrange it at all (Windows Terminal shows Persian reversed and unjoined).
So the terminal receives cells in visual order, and the text models, carets,
selections and copies stay in logical order.

## Pipeline and owners

| Stage | Owner | Contract |
|---|---|---|
| Unicode data and UAX #9 levels | `third_party/typesetting/bidi` (pinned go-text, Unicode 17) | Rules P2–I2 and paragraph L1. Passes the full `BidiTest.txt` and `BidiCharacterTest.txt` level suites. |
| Arabic joining | `third_party/typesetting/arabic` | HarfBuzz's joining state machine and presentation-form table. No ligatures: a ligature would merge two cells. |
| Paragraph and display line | `tui/bidi` | `Analyze` resolves one paragraph from units (grapheme clusters, inline objects); `Layout.Line` applies L1 and L2 to one display line, maps L4 mirrors and joined forms, and maps columns both ways. Its ordering passes the visual-order fields of both conformance files. |
| Policy | `tui/bidi` `SetPresentation`, `SetVisualCaret`; `tui/termview` `SetArrangement` | Published by `tui/viewmodel/text_direction.go` from settings and the host terminal report. Caches key on `bidi.Generation()`. |
| Cells | `tui/surface` | `WriteString` arranges one label. `WriteLogical` plus `ApplyLine` arrange a display line built from several runs. `ReadLogical` returns painted cells to logical text. |
| Host terminal | `third_party/bubbletea/host_bidi.go` | DECRQM for BDSM at startup; explicit mode (`CSI 8 l`, `CSI 1 SP k`) while Crude arranges; restore on exit and suspend. |

`ApplyLine` moves whole blocks of painted cells. Every cell keeps its colors,
attributes and link, so selection, find matches, diagnostics, occurrences and
a block caret painted at logical columns travel with their characters. A block
whose source character is still painted takes its arranged glyph; a painter
that drew something else in its place (a whitespace marker, an indent guide)
keeps it. A line that would split a wide grapheme is refused and stays logical.

Each arranged cell carries a `cell.BidiMark` (level, line start, glyph
substitution) in existing struct padding. It never changes terminal output;
the Host's painted-text selection uses it to copy logical text.

## Paragraphs and directions

| Surface | Paragraph | Direction |
|---|---|---|
| Labels (`WriteString`, ANSI lines) | the label | automatic (first strong character), like `dir="auto"` |
| Composer and text areas | each hard line | automatic; a line without letters keeps the line above (UAX #9 HL1) |
| Text fields | the value | automatic; masked values are never arranged |
| Chat rows | the source paragraph of the row's first source span | that paragraph's direction for every wrapped row; code is left-to-right; rows without source are left-to-right |
| Editor | each source line | left-to-right (default) or **By line**: automatic, with HL1 inheritance from the nearest line above (64-line bound) |
| Embedded terminal | each screen row | left-to-right, like a terminal in implicit mode; see below |

A right-to-left paragraph is right-aligned in its display line: padding columns
after the text take the paragraph level, so L2 places them on the left. A
wrapped row and the horizontally scrolled window of an unwrapped line are both
display lines of the same paragraph: levels and joining come from the whole
paragraph, L1 and L2 apply to the visible part. Inlay hints, ghost text and
reference chips are inline objects (U+FFFC for the algorithm) and keep their
own order.

## Caret, pointer and selection

- The caret is a block on the character after the caret in text order, the
  model the terminal-wg BiDi recommendation uses. At the end of a line it sits
  on the first padding cell, which is left of a right-to-left paragraph.
- Pointer columns map through the row's `Line` before any existing hit test:
  editor (`positionOnRow`), text areas, text fields, chat selection and inline
  actions, comparison panes, terminal selection and forwarded mouse reports.
  Past the edge of a right-to-left row the nearest cell decides, so a press
  right of the text is at the line start, as left of left-to-right text.
- Up, Down, Page Up and Page Down keep the caret's screen column on its
  arranged row, with and without word wrap, folds and horizontal scrolling.
- Selections are logical ranges. A highlight can show as separate blocks on
  screen; copy returns the logical text.
- **Move on screen** (default): a plain Left or Right moves to the cell on that
  side. At a row edge, word movement (Ctrl+arrow) and selection collapse, the
  arrows follow text order and point the other way in a right-to-left row.
  **Follow text order**: Left is always backward. Up and Down keep the screen
  column. Secondary editor carets move in text order.

## Embedded terminals

Rows are arranged after they are painted, as a terminal in implicit BiDi mode
does: each row is a left-to-right paragraph and right-to-left runs read right
to left in place, so prompts, columns and box drawing stay where the program
put them. The default arranges only the main screen. A full-screen program
uses the alternate screen and may arrange its own text (another Crude,
editors); libghostty-vt does not track BDSM, so programs cannot opt out
themselves. The Terminal setting chooses Shell output, All output or As
written. Copy returns the program's text from the grid, never painted cells.

## Host terminal

| DECRPM for BDSM | Crude |
|---|---|
| set (implicit) | writes explicit mode and redraws once, so the terminal's Erase in Display gives every line the new mode |
| reset (explicit) | writes explicit mode; on exit restores only the default direction |
| permanently set | never writes; stops arranging and leaves cells logical for the terminal |
| no answer, not recognized | writes explicit mode (ignored by terminals without BiDi); restores implicit mode on exit |

Arranging stops entirely with **Terminal**, which also gives the host its own
mode back. Terminals that arrange text without reporting BDSM (for example
some Konsole and macOS Terminal configurations) need that choice.

## Fonts

The terminal draws the glyphs Crude sends; Crude cannot draw them itself. A
monospace font without Arabic or Hebrew makes the terminal borrow glyphs from a
proportional font: letters do not fill their cells, so joined words show gaps
and wide letters are squeezed. A font with ligatures on presentation forms
(Courier New, DejaVu Sans Mono) is wrong in a different way: it combines the
visual pair lam-initial, alef-final of "ال" into the ligature "لا".

**Crude RTL** (`assets/fonts/CrudeRTL-Regular.ttf`, `CrudeRTL-Bold.ttf`) is
the right-to-left fallback for Crude's window. It takes the Arabic glyphs of
DejaVu Sans Mono 2.37 unchanged (0.6 em cells, like JetBrains Mono, with joins
that reach the cell edges), adds the Hebrew letters and points and the rarer
Arabic letters from DejaVu Sans fitted to the same cell, and has no glyph
substitution. It covers 225 of the 242 presentation forms Crude emits; the
rest (some Urdu, Sindhi and Kirghiz letters) come from the terminal's own
fallback. `scripts/build_rtl_font.py` builds it from the pinned, checksummed
DejaVu archive; the license is `assets/fonts/LICENSE-DejaVu`.

| Window | How Crude RTL is used |
|---|---|
| Windows (`install.ps1 -Gui`) | Registered for the user from `%LOCALAPPDATA%\Crude\window\rtl-fonts`; the Crude profile names `JetBrainsMono NF, Crude RTL` (Windows Terminal font fallback) |
| Linux (`install.sh --gui`) | Copied to `~/.local/share/fonts/CrudeRTL`; the WezTerm configuration lists it after JetBrains Mono |
| Another terminal | Install the font from the release's `fonts/` folder and add it as the fallback after your font |

## Settings

| Setting | Values | Owner |
|---|---|---|
| Interface → Right-to-left text → Arrange | Join letters (default), Separate letters, Terminal | `ui.right_to_left` |
| Interface → Right-to-left text → Arrow keys | Move on screen (default), Follow text order | `ui.caret_movement` |
| Editor → Line direction | Left to right (default), By line | `editor.text_direction` |
| Terminal → Right-to-left text | Shell output (default), All output, As written | `terminal.right_to_left` |

Defaults are stored as absent fields and keep inheriting
([settings](settings.md)).

## Performance

`NeedsLayout` inspects only non-ASCII bytes and confirms candidates in a
range check proven against the whole pinned Unicode table
(`TestTriggerBlocksCoverUnicode`). Left-to-right text never builds a layout.
Paragraph layouts are cached per source line (editor), per content revision
(text areas) and per value (text fields). Chat rows cache the direction of
each source paragraph and arrange a visible row only when it holds
right-to-left text.

## Limits

- Arabic letters use presentation forms; letters outside the Unicode
  presentation-form blocks show in their nominal form, and Lam-Alef shows as
  two joined cells instead of one ligature.
- The terminal draws the glyphs. Without Crude RTL or another monospace font
  with presentation forms and no ligatures, joined words can show gaps or a
  wrong ligature ([Fonts](#fonts)).
- Embedded terminals show what the shell wrote; a shell or console host can
  break a long right-to-left line itself before Crude arranges its rows.
- Rows of one chat paragraph are analyzed row by row with the paragraph's
  direction; weak characters at a row edge can resolve differently from a
  whole-paragraph analysis.
- Terminal rows are separate paragraphs; a soft-wrapped right-to-left line
  is arranged per row.
- Bidi formatting characters in code are honored, not highlighted.

## Verification

```bash
cd third_party/typesetting && go test ./...
go test ./tui/bidi ./tui/surface ./tui/ui ./tui/editor ./tui/chatprojection ./tui/termview \
  ./tui/rendering ./tui/screens/shellchat ./tui/screens/diffview ./tui/screens/findinfiles
go test ./tui/viewmodel -run 'RightToLeft'
cd third_party/bubbletea && go test -run HostBidi .
uv run scripts/build_rtl_font.py   # rebuilds assets/fonts from the pinned sources
```

Native acceptance uses `scripts/tuishot.ps1 -WtProfile <profile>` with a
Windows Terminal profile that names `JetBrainsMono NF, Crude RTL`, and an
isolated `CRUDE_HOME` ([Windows capture](../guides/windows-visual-capture.md)).
