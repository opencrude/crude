# Terminal Display Width — Full Process Document

This document is the canonical reference for how Crude measures and paints
Unicode (emoji, ZWJ sequences, CJK, symbols like ☰) so the TUI stays aligned
on modern terminals: **Kitty, Ghostty, WezTerm, Contour, foot, Alacritty,
Windows Terminal** (grapheme mode).

Related code:

| Path | Role |
|------|------|
| `tui/rendering/width.go` | Process-wide `WidthMethod`, `VisibleWidth`, `FirstGrapheme` |
| `tui/rendering/ansi.go` | `VisibleWidth` = strip ANSI then measure |
| `tui/rendering/ansi_parser.go` | ANSI → surface cells via `FirstGrapheme` |
| `tui/surface/buffer.go` | Cell packing (wide char + continuation) |
| `tui/compositor/output.go` | Cell grid → full-frame ANSI for Bubble Tea |
| `third_party/bubbletea/` | Force UV `cellbuf.Method = GraphemeWidth` |
| `third_party/xansi/` | Shared `FirstGraphemeCluster` + `correctClusterWidth` |
| `go.mod` replaces | `charm.land/bubbletea/v2`, `github.com/charmbracelet/x/ansi` |

---

## 1. The problem in one sentence

**Two independent systems measure the same string and must return the same
column count — and that count must match what the terminal advances.**

If any two of {layout, ultraviolet, terminal} disagree by even one column,
you get:

- Ragged columns (settings sidebar icons)
- Scrollbar “ghost” characters
- Clipped labels on dividers
- Cursor / hover hit-test drift

---

## 2. The render pipeline (where width is used)

```
 Widget builds ANSI string
        │  pads/truncates with VisibleWidth()
        ▼
 RenderToSurface / CellBuffer.WriteString
        │  FirstGrapheme() → Cell{Width: 1|2} + continuation
        ▼
 Compositor flattens surface tree
        │  GenerateANSI: emit rune once, skip Width==0
        ▼
 tea.View{Content: fullFrameANSI}
        ▼
 Bubble Tea cursed renderer
        │  uv.NewStyledString(content).Draw(cellbuf)
        │  cellbuf.Method MUST be GraphemeWidth
        ▼
 Ultraviolet TerminalRenderer
        │  putAttrCell: cur.X += cell.Width
        ▼
 Terminal paints glyphs and advances cursor
```

**Critical lossy step:** cell metadata is discarded when Crude emits ANSI.
Ultraviolet **re-parses** the string with `x/ansi.FirstGraphemeCluster`.
Therefore Crude and UV must call the **same** width function on the **same**
bytes. Fixing only Crude (or only probing the terminal) is not enough.

---

## 3. Dual-source failure mode (why probe alone fails)

```
Source A: Crude layout          Source B: UV cellbuf
VisibleWidth / FirstGrapheme    printString(buf.WidthMethod())
```

| Mistake | Result |
|---------|--------|
| Layout uses uniseg, UV uses ansi | Scrollbar artifacts (historical bug) |
| Layout uses GraphemeWidth, UV defaults WcWidth | Emoji/ZWJ pad drift |
| Probe terminal → only update layout | Layout matches terminal, UV still wrong → artifacts |
| Layout pads ☰ as 2, terminal advances 1 | Settings “Interface” row unaligned |

**Invariant:**

```
layout_width(s) == uv_cell_width(s)   // required every frame
// terminal_advance(s) should match both; when tables lie, correct both
```

---

## 4. What modern terminals actually do

Consensus across **Ghostty, foot, Windows Terminal (grapheme mode), Kitty
(current Unicode / text-sizing work)**:

| Rule | Detail |
|------|--------|
| **Grapheme clusters** | UAX #29 extended grapheme clusters, not raw code points |
| **Width ∈ {0, 1, 2}** | Cap at 2 — family ZWJ emoji is still **2**, not 4/6/8 |
| **Emoji / ZWJ / VS16 / flags** | Width **2** |
| **VS15 (text presentation)** | Width **1** |
| **CJK / fullwidth** | Width **2** |
| **Some symbols** | Unicode 16 tables say Wide; terminals often advance **1** (e.g. ☰ U+2630–2637) |

### Windows Terminal specifically

- Default measurement mode: **Graphemes** (not legacy conhost, not pure wcswidth).
- Grapheme width is always **0–2** (`CodepointWidthDetector`).
- Mode `?2027` is **not** fully implemented as a runtime switch; WT picks mode
  at session start. Crude still enables `?2027` where supported (Kitty etc.).
- Unqualified emoji (`♻`) may paint narrow by UAX #11 design; use `♻️` (FE0F)
  when you need a guaranteed 2-cell emoji.

### Historical note (☰ / scrollbar)

Older Crude used `uniseg.StringWidth` (☰ → 1) while UV used `ansi.StringWidth`
(☰ → 2). Fixing layout to match UV removed scrollbar ghosts. Later, Unicode 16
tables made ☰ → 2 while Kitty still advances **1** for that symbol — same dual
mismatch class, different pair of sources.

---

## 5. Crude’s solution architecture

### 5.1 One width method process-wide

```go
// tui/rendering/width.go
var WidthMethod = ansi.GraphemeWidth

func FirstGrapheme(s string) (cluster string, width int)
func PlainWidth(s string) int
func VisibleWidth(s string) int  // StripAnsi then WidthMethod.StringWidth
```

**Never** call `uniseg.StringWidth`, `runewidth.StringWidth`, or bare
`lipgloss.Width` for layout columns. They will drift from UV.

### 5.2 Shared measurement in vendored `x/ansi`

`go.mod`:

```
replace github.com/charmbracelet/x/ansi => ./third_party/xansi
```

`FirstGraphemeCluster` always runs `correctClusterWidth` after the table
lookup so **every** caller (Crude + UV) gets the same corrections.

### 5.3 Force UV onto GraphemeWidth from frame 0

`go.mod`:

```
replace charm.land/bubbletea/v2 => ./third_party/bubbletea
```

Upstream Bubble Tea defaults UV `cellbuf.Method` to **WcWidth** and only
switches to GraphemeWidth if the terminal answers mode 2027. That is too late
and incomplete (Windows Terminal may never flip the mode).

Crude’s bubbletea patch:

1. `newCursedRenderer`: `cellbuf.Method = ansi.GraphemeWidth` immediately  
2. `start()`: emit `SetModeUnicodeCore` (`?2027h`) when using grapheme width  
3. `WithWidthMethod(m)` option; Crude calls it from `tui.Start`

```go
// tui/tui.go
program := tea.NewProgram(a, tea.WithWidthMethod(ansi.GraphemeWidth))
```

### 5.4 `correctClusterWidth` algorithm

Location: `third_party/xansi/parser_decode.go` (and documented in
`tui/rendering/width.go`).

```
input: grapheme cluster + raw table width w
  1. empty → 0
  2. w > 2 → w = 2                    // terminal cap
  3. w ≤ 1 → return w
  4. VS15 without VS16 → 1            // text presentation
  5. VS16 or ZWJ or regional indicators → 2
  6. emoji base ranges → 2
  7. East Asian wide / CJK → 2
  8. terminal-narrow symbols (☰ U+2630–2637) → 1
  9. else return w
```

| Example | Width |
|---------|-------|
| `a` `◆` `✎` `≡` | 1 |
| `☰` (burger / trigram) | **1** (corrected) |
| `中` | 2 |
| `😀` `⚡` `✨` `🔧` | 2 |
| `👨‍💻` `🏳️‍🌈` `🧑‍🌾` | 2 (one cluster) |

---

## 6. What each third_party patch is for

| Patch | Problem it solves | Safe to remove? |
|-------|-------------------|-----------------|
| **`third_party/bubbletea`** | UV defaulted to WcWidth while Crude used GraphemeWidth | **No** — reintroduces emoji dual-source drift |
| **`third_party/xansi`** | Table vs terminal for ☰ and cap/emoji rules in one place | **No** — reintroduces ☰ sidebar bug; loses shared corrections |

They are **complementary**, not alternatives.

---

## 7. UI rules for icons (settings and chrome)

You **may** use any glyph. Layout does not special-case icons in widgets.

1. Prefer measuring via `rendering.VisibleWidth` / `PlainWidth` only.  
2. For column alignment with mixed icon widths, pad to a **fixed name column**
   using measured width (do not assume icon width == 1).  
3. Long labels: size the rail from content (settings left column grows for
   “Language Servers”) so text is not clipped into the divider.  
4. For guaranteed 2-cell emoji on Windows Terminal, use fully-qualified forms
   with **U+FE0F** when the base is text-default (e.g. `♻️` not `♻`).

Settings category labels:

```go
// formatCategoryLabel — icon + " " + name
// Width comes entirely from the shared ansi path.
```

---

## 8. Why we do **not** probe the terminal

Cursor-position probes (print glyph → CPR) were tried historically and deleted
(`terminal_probe.go`). They fail the dual-source rule unless **layout and UV
are updated atomically from the same probe result**. Probing only Crude leaves
UV wrong → artifacts. Probing is also flaky under alt-screen, racey first
frames, and multiplexers.

Instead we encode the **terminal consensus algorithm** once in `x/ansi` and
force both consumers onto it.

---

## 9. End-to-end checklist for new width work

When adding UI that pads, truncates, or hit-tests by columns:

1. Use `rendering.VisibleWidth` / `FirstGrapheme` / `PlainWidth` only.  
2. Never import `uniseg` or `runewidth` for display columns.  
3. If a glyph misaligns on Kitty **and** WT, fix `correctClusterWidth` in
   `third_party/xansi` — not a one-off pad in the widget.  
4. Add a test under `tui/rendering/width_test.go` that:
   - asserts the glyph width
   - asserts `PlainWidth(s) == ansi.GraphemeWidth.StringWidth(s)`  
5. Rebuild with replaces active (`go build ./...`).  
6. Manually check settings sidebar + any chrome using the glyph.

---

## 10. Tests

```bash
go test ./tui/rendering/ -run 'Width|Emoji|Trigram|Grapheme' -count=1
go build ./...
```

Key tests in `tui/rendering/width_test.go`:

- `TestEmojiGraphemeWidth` — emoji/ZWJ/CJK  
- `TestTrigramBurgerIsSingleCell` — ☰ → 1  
- `TestWidthNeverExceedsTwo` — cap  
- `TestLayoutMatchesANSIMethod` — layout ≡ UV path  

---

## 11. Known limits (honest)

1. **Unqualified emoji** may paint narrow on WT by Unicode design; qualify with FE0F.  
2. **Legacy conhost / pure wcwidth shells** are outside the target set.  
3. **New Unicode releases** may reclassify more symbols; extend
   `isTerminalNarrowSymbol` when real terminals disagree with tables.  
4. **Font metrics ≠ cell advance** — a glyph may *look* narrow inside two
   cells; we measure **cursor advance**, not ink bounds.  
5. Perfect parity with every font on every OS is impossible; the goal is
   **layout ≡ UV ≡ modern terminal consensus**.

---

## 12. Timeline of decisions (process log)

| Step | Decision |
|------|----------|
| Historical | Match UV: drop uniseg for `VisibleWidth` (scrollbar fix) |
| Problem | Layout GraphemeWidth vs UV default WcWidth |
| Fix A | Vendor bubbletea: force GraphemeWidth + optional `WithWidthMethod` |
| Problem | ☰ measured 2, Kitty advances 1 → settings rail broken |
| Wrong fix | Replace ☰ with ≡ / only pad in settings |
| Right fix | Vendor x/ansi: `correctClusterWidth` shared by layout + UV |
| Expansion | Cap 2, VS15/VS16/ZWJ/flags/emoji/CJK rules for WT + Kitty |
| Rejected | Terminal probe unless both sources update from one value |

---

## 13. Quick reference for agents

```
Want column count of a styled string?  → rendering.VisibleWidth(s)
Want first cluster + width?            → rendering.FirstGrapheme(s)
Emoji looks wrong in one widget only?  → fix xansi correctClusterWidth, not the widget
UV and Crude disagree?                 → check go.mod replaces + WidthMethod
☰ / trigrams                           → width 1 (corrected)
😀 / ZWJ / ⚡ / CJK                      → width 2
Never                                  → uniseg / runewidth for layout
Never                                  → probe only one side of the pipeline
```
