# Crude Architecture & Debugging Journal

## Project Overview

Crude is a TUI (Terminal User Interface) AI agent client built in Go using Bubble Tea v2 and the compositing engine architecture. It features floating windows, a compositing surface tree, and cell-buffer-based rendering via the `ultraviolet` parser.

---

## Rendering Architecture

### Surface Tree (Compositing Engine)

The compositor uses a tree of `Surface` nodes. Each `Surface` has a `CellBuffer` (flat `[]cell.Cell` array) and can have child surfaces with z-ordering.

**Composition pipeline:**

1. `BeginFrame()` — clears root children + back buffer
2. Background renders into root surface
3. Window manager renders floating windows (each window has its own surface with a child content surface)
4. `Composite()` — flattens surface tree via `FlattenZOrder()`, blits all surfaces bottom-to-top into the back buffer using `surface.Blit()`
5. Blit compares old vs new back buffer, sends ANSI diff to terminal

### Floating Window Rendering

Each `FloatingWindow` has:
- A main surface (`fw.surf`) — sized `w x h`, contains background fill, border decorations, title bar
- A child content surface (`fw.contentSurf`) — sized `contentW x contentH`, positioned at `(contentX, contentY)`

**Render flow (for LShape border):**
1. `fw.surf.Buffer().Clear()` — transparent blanks
2. `DrawBackground()` — fills entire buffer with `SubPaneBg`
3. `DrawLBorder()` — draws left vertical + bottom horizontal border chars
4. `DrawTitleBar()` — draws title text in top row
5. `fw.contentSurf.Buffer().Clear()` — transparent blanks
6. `fw.Content.Render(fw.contentSurf, ctx)` — widget renders ANSI string into content surface via `RenderToSurface()`
7. Content surface added as child of window surface
8. Compositor blits window surface then content surface onto back buffer

### Cell Types

- `Cell.Char` — the rune
- `Cell.Width` — display width (1 or 2 for wide chars, 0 for continuation cells)
- `Cell.FG` / `Cell.BG` — colors (`ColorDefault` = inherit from destination during Blit)
- `Cell.Transparent` — if true, Blit skips this cell

### Blit Semantics

`surface.Blit()` composites a source buffer onto a destination:
- Transparent source cells are **skipped** — destination shows through
- Cells with `BG.IsDefault()` inherit the destination cell's BG color
- Wide cells (Width=2) write their continuation cell automatically
- Continuation cells (Width=0) are skipped

---

## The Scrollbar Artifact Bug

### Symptoms

Stale characters appearing at the right edge (scrollbar column) of the command palette's view section when scrolling. The artifacts were small fragments/squares that persisted after the scrollbar moved.

### Root Cause

**Width calculation mismatch** between our `VisibleWidth` function and ultraviolet's parser.

#### Discovery Chain

**Phase 1: Terminal Probe**

The codebase had a `ProbeTerminal()` function in `tui/rendering/terminal_probe.go` that would send individual Unicode characters to the terminal, query cursor position via DSR (`\x1b[6n`), and probe whether the terminal rendered each character at width 1 or 2. This was called at startup in `main.go` and took ~200ms (12 probes × 80ms timeout each).

The probed widths fed `isWideCJK()` which used `uniseg.StringWidth` with a special case: if the probe said a character was wide, force width=2; otherwise use uniseg's default.

**Phase 2: Mismatch Discovery**

The artifact appeared as a stale scrollbar character. The scrollbar column is at position `renderW` (the rightmost content column). When scrolling, old scrollbar characters at previous positions should be overwritten by new content + padding.

Diagnosis steps:
1. Ruled out content surface cell issues (transparent blanks, AnsiAdapter fills)
2. Ruled out compositing pipeline (Blit semantics, surface tree order)
3. Ruled out Bubble Tea's diff system (ultraviolet's `putRange` zero-width skip at `terminal_renderer.go:681`)
4. Ruled out ultraviolet's `putAttrCell` placeholder skip
5. **Found**: `VisibleWidth` used `uniseg.StringWidth` which returned width=1 for ☰ (U+2630), but ultraviolet's parser uses `github.com/charmbracelet/x/ansi.StringWidth` which returns width=2 for the same character

This 1-column offset between our cell buffer layout and ultraviolet's cell buffer made ultraviolet's diff compare mismatched columns and see "no change" at the old scrollbar position, never overwriting it.

**Phase 3: The Fix**

Replaced `VisibleWidth` with `ansi.StringWidth(StripAnsi(s))` — matching ultraviolet's calculation exactly.

```go
// Before
func VisibleWidth(s string) int {
    return uniseg.StringWidth(s)
}

// After
func VisibleWidth(s string) int {
    return ansi.StringWidth(StripAnsi(s))
}
```

**Phase 4: Residual Artifact**

After the width fix, a "hole before scrollbar" remained — a visual gap where the old Width=1 assumption in `renderItem`'s layout formula left extra space. This was a layout calculation issue, not a stale character issue.

### Ultimate Impasse

The real problem: `displaywidth` (used by `ansi.StringWidth` via `GraphemeWidth` mode) classifies ☰ (U+2630) as `_Wide` in its trie data. This is **correct according to Unicode** — the Unicode Character Database classifies ☰ (U+2630 TRIGRAM FOR HEAVEN) as `East Asian Width: Wide (W)`. The `displaywidth` package follows this classification.

**However, terminals are split on how to handle this:**

| Terminal | ☰ width | Approach |
|----------|---------|----------|
| Kitty | 1 | Overrides Unicode, treats as narrow |
| Alacritty | 1 | Overrides Unicode, treats as narrow |
| Ghostty | 1 | Overrides Unicode, treats as narrow |
| WezTerm | 1 | Overrides Unicode, treats as narrow |
| **Windows Terminal** | **2** | **Follows Unicode standard** |
| iTerm2 | varies | Font-dependent |

The charmbracelet team is aware of this general class of issues (see [lipgloss#562](https://github.com/charmbracelet/lipgloss/issues/562)) and their position is: "this is a known terminal emulators issue and terminals calculate widths differently" — pointing to [jeffquast.com](https://www.jeffquast.com/post/ucs-detect-test-results/) and [mitchellh.com](https://mitchellh.com/writing/grapheme-clusters-in-terminals) research.

**This is actually correct**: terminals DO calculate widths differently. The major terminals are split — Kitty/Alacritty/Ghostty/WezTerm override Unicode and render ☰ as width=1, while Windows Terminal follows Unicode and renders ☰ as width=2. The `displaywidth` package is correct according to Unicode; the terminals that render width=1 are the ones deviating from the standard.

The `EastAsianWidth` option in `displaywidth`/`go-runewidth` controls EAW=A characters (ambiguous width, like box-drawing and geometric symbols). ☰ is NOT in this category — it's classified as `_Wide` (unconditional), which is a different classification. ▶ (U+25B6) returns width=1 in both modes and was never problematic.

Verified empirically:
- `ansi.StringWidth("☰")` → 2 (via displaywidth, `_Wide` classification — matches Unicode)
- `ansi.WcWidth.StringWidth("☰")` → 2 (via go-runewidth, same result)
- Both `GraphemeWidth` and `WcWidth` modes agree on width=2 for ☰
- Windows Terminal renders ☰ as width=2 (follows Unicode)
- Kitty/Alacritty/Ghostty/WezTerm render ☰ as width=1 (override Unicode)

The width calculation doesn't need to match every terminal — it needs to match ultraviolet's diff system, which it does. Both `displaywidth` and `go-runewidth` return width=2 for ☰, and ultraviolet uses the same library, so our layout matches UV's cell buffer.

This creates a **terminal-dependent visual mismatch**:
- On Windows Terminal: layout is correct (width=2 matches terminal rendering)
- On Kitty/Alacritty/Ghostty/WezTerm: layout is 1 column off (width=2 but terminal renders width=1)

**Resolution**: Removed the `catView` items from the command palette that used ☰ (U+2630). This eliminates the visual mismatch on all terminals. The alternative — changing our width to match Kitty/Alacritty — would break ultraviolet's diff system and cause stale character artifacts on all terminals including Windows Terminal.

---

## Width Calculation Migration

### Before

Three different width calculation sources:
1. `github.com/rivo/uniseg` — grapheme cluster iteration + StringWidth
2. `github.com/mattn/go-runewidth` — used in `fileio/exec.go`
3. `github.com/clipperhouse/displaywidth` — used in `tui/surface/buffer.go`

Plus a custom `isWideCJK()` function with terminal probing.

### After

Single source of truth: `github.com/charmbracelet/x/ansi`
- `ansi.StringWidth()` — replaces `uniseg.StringWidth`, `runewidth.StringWidth`, and `displaywidth.String`
- `ansi.DecodeSequence()` — replaces `uniseg.StepString` for grapheme cluster iteration (also returns display width directly, eliminating redundant `VisibleWidth` calls in loops)
- `ansi.FirstGraphemeCluster()` — replaces `uniseg.NewGraphemes().Next()` pattern
- `ansi.Truncate()` — replaces `runewidth.Truncate`

### Files Changed

| File | Change |
|------|--------|
| `tui/rendering/ansi.go` | Removed `uniseg` import, replaced all `StepString` with `DecodeSequence`, removed `isWideCJK` |
| `tui/rendering/wrap.go` | Removed `uniseg` import, replaced all `StepString` with `DecodeSequence` |
| `tui/rendering/ansi_parser.go` | Removed `uniseg` import, replaced `NewGraphemes` with `FirstGraphemeCluster` |
| `tui/surface/buffer.go` | Removed `uniseg` + `displaywidth` imports, replaced with `FirstGraphemeCluster` |
| `fileio/exec.go` | Removed `runewidth` import, replaced with `ansi.StringWidth` + `ansi.Truncate` |
| `tui/rendering/terminal_probe.go` | **Deleted** — entire file no longer needed |
| `main.go` | Removed `ProbeTerminal()` call and `rendering` import |
| `go.mod` / `go.sum` | `go mod tidy` removed unused dependencies |

### Deleted Functions

- `isWideCJK()` — custom CJK width logic with terminal probing
- `ProbeTerminal()` — slow terminal emoji width probe (12 DSR queries, ~200ms)
- `probedWide()` — lookup into probed ranges
- `makeNoColorBgTransparent()` — made blank cells transparent, causing stale characters to leak through

---

## Transparency & Blit Bug

### Problem

`makeNoColorBgTransparent()` in `tui/wm/window.go` made blank cells with `NoColor` background transparent. When the content surface had transparent cells at the scrollbar column (from padding spaces), `Blit()` skipped them. The window surface's old scrollbar characters from previous frames persisted.

Even though the window surface was cleared when `contentDirty=true`, on subsequent frames with `contentDirty=false` the window surface wasn't cleared and retained old characters. The transparent content cells didn't overwrite them.

### Fix

Removed `makeNoColorBgTransparent()` entirely. The content surface is now fully opaque. Cells with `NoColor` BG inherit the destination's background color during Blit (via the `BG.IsDefault()` → inherit logic), which produces the same visual result without creating transparent holes.

Additionally, all padding in the command palette's `View()` method now uses `lipgloss.NewStyle().Background(SubPaneBg).Render(strings.Repeat(" ", n))` instead of raw `strings.Repeat(" ", n)` — giving padding cells an explicit background color so they're always opaque.

---

## Command Palette Sections

The command palette groups items by `cmdCategory`:

```
const (
    catFile    cmdCategory = "file"
    catTools   cmdCategory = "tools"
    catSystem  cmdCategory = "system"
    catHelp    cmdCategory = "help"
    catAgent   cmdCategory = "agent"
)
```

Previously had `catView` (removed due to ☰ being classified as `_Wide` by displaywidth, causing width mismatch on some terminals).

### Layout Formula (renderItem)

```
availForLabel := width - 2 - iconW - 1 - gap - detailW
```

Where:
- `width` = `renderW` (content width minus scrollbar)
- `2` = selection marker prefix (`"> "` or `"  "`)
- `iconW` = `VisibleWidth(icon)` — width of icon character
- `1` = space between icon and label
- `gap` = space between label and detail (3)
- `detailW` = width of detail string

### Padding in View

```go
for li, line := range visible {
    lw := rendering.VisibleWidth(line)
    if lw < renderW {
        line += bgPad.Render(strings.Repeat(" ", renderW-lw))
    }
    if sbW > 0 && li < len(sbLines) && sbLines[li] != "" {
        line += sbLines[li]
    }
    out = append(out, padLine(line))
}
```

Order matters:
1. **Pad content to `renderW`** — ensures the scrollbar starts at the correct column
2. **Append scrollbar character** — goes at column `renderW`
3. **Pad total to `width`** — handles any remaining gap (should be no-op since content + scrollbar = `renderW + sbW = width`)

---

## Key Debugging Techniques Used

1. **Grep for width functions** — found all `uniseg.StepString`, `StringWidth`, `RuneWidth` calls across the codebase
2. **Trace rendering pipeline** — followed the path from command palette `View()` → `RenderToSurface()` → `Blit()` → terminal output
3. **Test width calculations** — ran `ansi.StringWidth` on individual characters to compare with `uniseg.StringWidth`
4. **Vendor inspection** — read ultraviolet's `terminal_renderer.go` to understand `putAttrCell` and `putRange`
5. **Eliminate variables** — tested `cw--` (force width 0) and `cw++` (force width 2) to confirm width mismatch was the mechanism

## Lessons Learned

1. **Consistency > correctness**: Our width calculation doesn't need to match what the terminal does — it needs to match what ultraviolet does. Even if both are "wrong" relative to the terminal, as long as they agree, the diff system works correctly.
2. **Transparency is dangerous**: Making cells transparent in a composited surface tree creates invisible state that can leak stale characters. Opaque cells are safer.
3. **`_Wide` classification is unreliable for cross-terminal compatibility**: Characters that `displaywidth` classifies as `_Wide` (like ☰ U+2630) follow Unicode's East Asian Width classification, but terminals are split — some follow Unicode (Windows Terminal renders as width=2), others override it (Kitty/Alacritty render as width=1). Avoid these characters in TUI layouts where positional accuracy matters across terminals. Use characters with unambiguous widths (ASCII, or explicitly wide CJK). Note: This is different from EAW=A characters (controlled by `EastAsianWidth` option) — `_Wide` classification is unconditional.
4. **Server-side width probing is fragile**: The DSR-based probe was slow, unreliable (some terminals don't respond), and produced different results on different terminals. Removing it eliminated a source of inconsistency.
