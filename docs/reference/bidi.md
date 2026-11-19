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
| Source code order | `tui/editor/bidi_syntax.go` | Isolates the code, string and comment tokens of a line from its syntax highlighting (UTS #55); `bidi.Unit.Before`/`After` carry the isolate controls. |
| Host terminal | `third_party/bubbletea/host_bidi.go` | DECRQM for BDSM at startup; explicit mode (`CSI 8 l`, `CSI 1 SP k`) while Crude arranges; restore on exit and suspend. `tui/viewmodel/text_direction.go` sends the wanted mode when the program starts (`Init`) and on every later change. |
| Terminal output | `bidi.TerminalText` (`tui/compositor`) | An arranged cell reaches the terminal without the joining and direction controls Crude already applied. |

`ApplyLine` moves whole blocks of painted cells. Every cell keeps its colors,
attributes and link, so selection, find matches, diagnostics, occurrences and
a block caret painted at logical columns travel with their characters. A block
whose source character is still painted takes its arranged glyph; a painter
that drew something else in its place (a whitespace marker, an indent guide)
keeps it. A line that would split a wide grapheme is refused and stays logical.

Each arranged cell carries a `cell.BidiMark` (level, line start, glyph
substitution) in existing struct padding. The Host's painted-text selection
uses it to copy logical text. Isolates are not recorded in cells: only the
editor and chat rows use them, and both copy from their source text.

An arranged cell keeps its source cluster, including a Persian half-space
(ZWNJ, U+200C), a ZWJ after a right-to-left letter and direction marks. Crude
has already applied them: the cell shows the joined form in visual order. The
compositor sends the cell without them (`bidi.TerminalText`). A terminal that
looks for one font with every code point of a cell drew "ـه‌" as a replacement
character (Ghostty), and one that shapes the cell drew the letter twice.
Emoji ZWJ sequences and Indic conjuncts keep their joiners; a cell of only
controls becomes a space, so widths never change.

## Paragraphs and directions

| Surface | Paragraph | Direction |
|---|---|---|
| Labels (`WriteString`, ANSI lines) | the label | automatic (first strong character), like `dir="auto"` |
| Composer and text areas | each hard line | automatic; a line without letters keeps the line above (UAX #9 HL1) |
| Text fields | the value | automatic; masked values are never arranged |
| Chat rows | the source paragraph of the row's first source span | that paragraph's direction for every wrapped row; code is left-to-right; inline code is a left-to-right isolate; rows without source are left-to-right |
| Editor | each source line | left-aligned code grid. **By line** (default) isolates prose in its reading direction. **Left to right** uses LTR paragraph order for prose |
| Embedded terminal | each screen row | left-to-right, like a terminal in implicit mode; see below |

Prose surfaces can align a paragraph to its reading direction. Editor rows
never include unused viewport space in reordering. Inlay hints and reference
chips are inline objects and keep their own internal order.

## Source code order in the editor

The editor follows UTS #55 (Unicode Source Code Handling, section 4.1), which
applies protocol HL4 of UAX #9 to source code. It takes the tokens from the
syntax highlighter that colors the line (`syntaxLine.roles`, one role per
source column, mapped through edits like the colors). Semantic tokens change
color only, not the lexical boundaries used for direction:

- Each code token (keyword, name, operator, punctuation, escape), each number
  and each string or comment delimiter is an atom: a left-to-right isolate.
- The content of a string or a comment is a first-strong isolate, so a
  Persian comment reads in its own direction and two Hebrew strings never
  swap places. (UTS #55 gives string contents the atom direction by default
  and allows this choice.)
- Atoms separated only by spaces form one syntactic element, isolated left to
  right as a whole, so `#cite(<key>)` keeps its order inside a Persian
  sentence instead of losing its `#` or mirroring its brackets.
- Literal text (`Text` and `Generic` tokens: the prose of Markdown, Typst and
  similar formats) stays in the line's paragraph (UTS #55 section 4.1.4).

Chat applies the same rule to Markdown: the renderer records each inline
code span (`SourceTrace.MarkCode`, `CopyText.InlineCode`), and
`chatprojection.ArrangedLine` isolates it left to right, so `./run.sh --all`
keeps its `./` inside a Persian or Hebrew sentence.

The editor separates alignment, reading order and clipping. The outer row
is left to right. In **By line**, a prose region has a first-strong isolate;
its indentation is outside the isolate. Each tab separates fields that keep
their source columns, even when whitespace markers are off. Openers leading into prose and
closers returning to code stay with the code. Inline code inside a sentence
retains its own nested isolate. A line without syntax uses its nonblank text
as one region. Empty rows start at the left margin. The existing go-text
resolver supplies levels and Arabic joining; there is no second bidi engine.

The design follows the separation in IntelliJ Platform's
[BidiRunUtil](https://github.com/JetBrains/intellij-community/blob/master/platform/platform-impl/src/com/intellij/openapi/editor/impl/view/BidiRunUtil.java)
(indent and lexical regions),
[LineLayout](https://github.com/JetBrains/intellij-community/blob/master/platform/platform-impl/src/com/intellij/openapi/editor/impl/view/LineLayout.java)
(logical lines and visual runs), and
[EditorCoordinateMapper](https://github.com/JetBrains/intellij-community/blob/master/platform/platform-impl/src/com/intellij/openapi/editor/impl/view/EditorCoordinateMapper.java)
(forward and inverse mapping over the same visual fragments).
Crude retains the visual map of the whole unwrapped line or actual soft-wrap
row. Horizontal scroll is a translation in visual columns. Paint seeks into
that map and clips at the pane edges. Clicks, drags, selection ink, caret
motion, reveal and popup anchors use that map. A resize with wrap off cannot
reorder the surviving cells. Source ranges and saved bytes remain logical.
For rectangular selection
`Line.LogicalRanges` maps every intersected visual block into logical ranges,
not just the two endpoints. This is the distinction made by OpenJDK's
[TextLayout.getLogicalRangesForVisualSelection](https://docs.oracle.com/en/java/javase/21/docs/api/java.desktop/java/awt/font/TextLayout.html#getLogicalRangesForVisualSelection(java.awt.font.TextHitInfo,java.awt.font.TextHitInfo)).

## Caret, pointer and selection

- The caret is a block on the character after the caret in text order, the
  model the terminal-wg BiDi recommendation uses. At the end of a line it sits
  on the first padding cell. In the editor this is after the left-aligned row.
- Pointer columns map through the row's `Line` before any existing hit test:
  editor (`positionOnRow`), text areas, text fields, chat selection and inline
  actions, comparison panes, terminal selection and forwarded mouse reports.
  In the editor blank space after text hits end of row. On direction-aligned
  prose surfaces the nearest edge decides.
- Up, Down, Page Up and Page Down keep the caret's screen column on its
  arranged row, with and without word wrap, folds and horizontal scrolling.
- Selections are logical ranges. A highlight can show as separate blocks on
  screen; copy returns the logical text.
- **Move on screen** (default): a plain Left or Right moves to the cell on that
  side. In the editor, word motion uses the run at the caret. Selection
  collapse uses visual endpoints. At the edge, plain motion crosses to the
  adjacent display row.
  **Follow text order**: Left is always backward. Up and Down keep the screen
  column. Secondary editor carets use the same motion as the primary caret.

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

The App publishes the saved choice when it is built, but a message sent then
has no program to reach. `hostBidiStartCmd` sends the wanted mode with the
program's start-up commands, and every later change goes to the running
program. (Before 2026-11-19 the start-up request was dropped and never sent
again, so VTE terminals kept implicit mode and reversed Crude's text a second
time.)

Arranging stops entirely with **Terminal**, which also gives the host its own
mode back. **Automatic** (the default) is Join letters, except in a terminal
that always reorders text and cannot be told to stop. Crude asks for the
terminal's name (XTVERSION) at start-up; Konsole answers `Konsole <version>`,
reports no BDSM and has no sequence to turn its BiDi rendering off, so Crude
leaves cells logical there. Konsole then arranges each row as one
left-to-right paragraph: text reads correctly, but the editor has no Crude-managed
code isolation, and pointer columns in right-to-left runs are
logical. A user who turns off "Bi-Directional text rendering" in the Konsole
profile chooses **Join letters** to get Crude's arrangement. Behind tmux or
screen Crude sees the multiplexer's name, not Konsole's; choose **Terminal**
there.

The Linux probes below show the BDSM report, not the terminal's font or
width behavior. In `CSI 8 ; Ps $ y`, `0` means not recognized, `1` set,
`2` reset, `3` permanently set, and `4` permanently reset. DA1 is not a
mode-2027 report. Width calibration remains independent
([terminal width](terminal-width.md)).

| Terminal tested | BDSM reply | Automatic arrangement in Crude |
|---|---|---|
| Alacritty 0.13.2 | not recognized | Crude |
| Kitty 0.50.0 | not recognized | Crude; use `force_ltr yes` to avoid per-run host reversal |
| Ghostty 1.3.1 | no reply | Crude |
| Kwi (libghostty 1.0.1, KDE nightly) | not recognized | Crude |
| Tilix 1.9.6 / VTE 0.76 | set | Crude requests explicit mode |
| xterm 390 | not recognized | Crude |
| Konsole 23.08.5 | no reply | Terminal, identified by XTVERSION |
| WezTerm 20240203 | reset | Crude requests explicit mode |

The WezTerm capture did not establish correct mixed-source selection there.
A reset reply is not evidence of permanently implicit mode or of an inability
to disable host reordering. The native editor scroll and selection checks
below concern Alacritty and Ghostty.

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
| Windows (`install.ps1`) | Copied into `%LOCALAPPDATA%\Microsoft\Windows\Fonts` and registered there for the user, with or without the GUI window; the Crude profile names `JetBrainsMono NF, Crude RTL` once Windows reports the family (Windows Terminal font fallback) |
| Linux (`install.sh --gui`) | Copied to `~/.local/share/fonts/CrudeRTL`; the WezTerm configuration lists it after JetBrains Mono |
| Another terminal | Install the font from the release's `fonts/` folder and add it as the fallback after your font |

On Linux, Alacritty and Tilix/Pango use Fontconfig for fallback. Install both
Crude RTL faces in the user's font directory and append the family after the
terminal's primary monospace family in a user Fontconfig rule. Keep the primary
family first; Crude RTL intentionally has no Latin glyphs. Check regular and
bold Arabic presentation forms and Hebrew with `fc-match <family>:charset=fee1`
and `fc-match <family>:charset=05e9`. Kitty uses `symbol_map` for the character
ranges actually present in the font; leave its primary and styled Latin fonts
unchanged. Kitty's `force_ltr yes` also stops its per-run reversal from arranging
Crude's already-arranged text a second time. Open a new terminal after changing
fallback or text-direction options.

### Native Linux review (2026-11-17)

Unicode bidi conformance, editor/composer selection and cursor mapping, chat
links and terminal lifecycle regressions pass on Linux. Native captures verify
file-link navigation and Persian/Hebrew display in Alacritty and Tilix at
160×40 and 80×24, with the user's installed font fallbacks. Kitty 0.49.2's
automated click check remains blocked: it reports an earlier pointer position
after XTEST moves. A raw terminal input probe reproduces this without Crude,
including with paced moves and clicks. Font selection and native display were
checked, but this is not a passing Kitty click journey. Do not compensate for
those coordinates in Crude's hit testing.

### Native editor scroll and selection check

Use a disposable workspace and a copy of a Persian Typst file. In Alacritty,
open the file, scroll horizontally, drag across a Latin word inside an RTL
sentence, replace it and save. Compare the file bytes, not just the highlight.
In Ghostty, pan by dragging the horizontal scrollbar and drag it back. On
Ghostty 1.3.1 the GTK frontend can consume a horizontal wheel event. Check
that the scrollbar actually moved before comparing captures. Double-click a
Persian word, replace it and verify that only its logical bytes changed.
For scroll acceptance compare the overlapping text rectangle after applying
the same horizontal translation as on an ASCII code line. Gutter, chrome,
pointer, cursor blink and clipped glyphs at the boundary are not text pixels.

## Settings

| Setting | Values | Owner |
|---|---|---|
| Interface → Right-to-left text → Arrange | Automatic (default), Join letters, Separate letters, Terminal | `ui.right_to_left` (`auto`, `arrange`, `unjoined`, `terminal`) |
| Interface → Right-to-left text → Arrow keys | Move on screen (default), Follow text order | `ui.caret_movement` |
| Editor → Line direction | By line (default), Left to right | `editor.text_direction` (`by_line`, `ltr`) |
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
- Code isolation needs the syntax highlighter's tokens: a file without a
  grammar lays out each line as plain text.
- Kitty and Ghostty use the font's fallback for a code point the primary font
  lacks; without Crude RTL (or `symbol_map` in Kitty) joined words can show
  gaps ([Fonts](#fonts)).

## Verification

```bash
cd third_party/typesetting && go test ./...
go test ./tui/bidi ./tui/surface ./tui/ui ./tui/editor ./tui/chatprojection ./tui/termview \
  ./tui/rendering ./tui/screens/shellchat ./tui/screens/diffview ./tui/screens/findinfiles
go test ./tui/compositor
go test ./tui/viewmodel -run 'RightToLeft|HostBidi|Konsole'
cd third_party/bubbletea && go test -run HostBidi .
uv run scripts/build_rtl_font.py   # rebuilds assets/fonts from the pinned sources
```

Native acceptance uses `scripts/tuishot.ps1 -WtProfile <profile>` with a
Windows Terminal profile that names `JetBrainsMono NF, Crude RTL`, and an
isolated `CRUDE_HOME` ([Windows capture](../guides/windows-visual-capture.md)).
On Linux, `scripts/tuishot.sh` with `TERMINAL=ghostty`, `tilix`, `konsole`,
`kwi`, `kitty`, `alacritty` and `wezterm` opens a Persian Typst file with
half-spaces, citations and labels; compare the order and joining of each
terminal against the table in [Host terminal](#host-terminal).
