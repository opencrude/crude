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
5. **State lifecycle — clear before early return**: When a function is the single source of truth for state variables (like `handleTabSwitch` for `activeEditorView`/`activeAgentView`), always reset those variables at the top of the function, BEFORE any early return paths. The bug was: `handleTabSwitch` set `activeEditorView = ""` AFTER the `if activeTab == nil { return }` guard, so closing the last tab left the view variable stale. This caused the renderer to fall through to the wrong branch (input widget instead of launcher), and Ctrl+Q to attempt a no-op close instead of quitting. Fix: `activeEditorView = ""; activeAgentView = ""` goes first, THEN check for active tab.
6. **Popup chrome is cleaner when self-drawn**: Popups that draw their own border+title (like the diagnostic hover popup) look more cohesive than those relying on FloatingWindow's generic title-bar decoration. The self-drawn approach: `BorderNone` + `decorated=false` on the FloatingWindow, then the widget renders `╭─ Title ─╮ ... │ body │ ... ╰──────╯` internally via `PopupChromeLines`. All popups (context menu, find/replace, save dialog, text prompt, file picker, diagnostic) now use this unified chrome.
7. **Context menu design rules**: (a) No title — the top border is just `╭───╮` with continuous dashes when there's no title/icon. (b) Disabled items must have the SAME leading indent as enabled items — no extra spaces. (c) Disabled items should be dim (Muted), NOT italic — italic looks like placeholder text, not disabled. (d) Height must be exact content + chrome — no extra bottom padding. (e) Click-outside-to-dismiss on left-click. (f) Left-click on a menu item activates it immediately. (g) Brief slide-in animation (80ms reveal top-to-bottom) makes it feel responsive.
8. **`buildTopBorder` width formulas must enumerate every title/icon combination independently**: The formula has different cases for (no title, no icon), (icon only), (title only), and (title + icon). Each case has a different fixed width due to the presence or absence of spaces between segments. Trying to use a single unified formula with conditionals for the spacer is fragile — recompute the fixed width from scratch per case.
9. **Testing ANSI-rendered strings — strip first, then search**: When asserting the content of an ANSI-styled string, NEVER use `strings.Index(rawLine, label)` — the label text can false-match inside ANSI escape codes (e.g., `"Off"` false-matching in an ANSI code that happens to contain the byte sequence). Always `stripAnsi` first, then search the plain text. Also, box-drawing characters like `│` are 3 UTF-8 bytes — byte-position assertions must account for this (e.g., label at byte-position 6 after `│` + 2 pad + 1 indent, not position 4).

---

## Unified Popup Design System

### Architecture

All popups in the TUI go through `tui/widgets/popupchrome.go`. The chrome is drawn by the widget itself (not by FloatingWindow):

```
╭─ Title ────────────────────────────────╮
│  body content line 1                   │
│  body content line 2                   │
╰────────────────────────────────────────╯
```

**Visual rules:**
- Border: rounded (`╭ ╮ ╰ ╯`), 1 cell wide
- Padding: 2 cells horizontal, 0 vertical
- Title: integrated into top border, bold. No title for context menus.
- Background: `theme.Colors.PopupBg` (`#161b22`, same as Code.Bg)
- Focused: border becomes PopupBorderFocused, title becomes PopupTitleFocused
- No close X button on most popups (Esc to close)
- FloatingWindow is always `WithDecoration(false)` + no title — it's just a positioned box

**Shared components** (all produce inner content lines, padded to `innerW`):
- `PopupInputField` — `<label>: <value>` with blinking cursor. No more `│` box-drawing field borders.
- `PopupToggle` / `PopupToggleRow` — filled pill when on (` Aa `), dim outline when off. No more `[Aa]`.
- `PopupButton` / `PopupButtonRow` — filled bg when focused (` Yes `), dim when not. No more `[ Yes ]`.
- `PopupSectionLabel` — italic Muted, no background color (was: Selection-bg-heavy).
- `PopupDivider` — thin `─` in PopupDivider color.
- `PopupHintLine` — `<key> <desc> · <key> <desc>` with bold keys.
- `PopupEmptyLine` — blank with PopupBg background.

**Sizing:**
- `PopupContentWidth(innerW)` → total width: `innerW + 6` (2 border + 4 pad)
- `PopupContentHeight(contentRows)` → total height: `contentRows + 2`

### Theme colors

```go
PopupBg, PopupBorder, PopupBorderFocused, PopupTitle, PopupTitleFocused
PopupDivider, PopupSubtleBg, PopupButtonBg, PopupButtonFg
```

### Popups migrated

| Popup | File | Title | Unique features |
|-------|------|-------|-----------------|
| Find/Replace | `findreplace.go` | Find (with ⌕ icon) | Focused input field, pill toggles |
| Save dialog | `confirmprompt.go` | Unsaved changes (severity-tinted) | Severity-colored title/border, Yes/No buttons |
| Context menu | `contextmenu.go` | None | Section labels, dividers, key hint detail, slide-in animation |
| Text prompt | `textprompt.go` | Dynamic (with Kind icon) | Input field, hint row |
| File picker | `filepicker.go` | File Picker (with ▤ icon) | Scrollbar, directory listing |
| Diagnostic hover | `mouse.go:updateDiagHoverPopup` | Severity label (border tinted) | Severity-colored border, code source line |
| Diagnostic context | `app.go:showDiagnosticAt` | Diagnostic (with ℹ icon) | Click-triggered, centered |

---

## Popup Mouse Support (click + hover + focus restoration)

Every popup responds to mouse clicks, mouse hover, and click-outside. Keyboard focus flows naturally: when a popup closes (via click-outside or Esc), the underlying widget regains keyboard focus because focus booleans persist.

### ClickOutsideBehavior

Each `FloatingWindow` has a `ClickOutsideBehavior` that determines what happens when the user clicks outside it:

```go
const (
    ClickOutsideCloseRoute ClickOutsideBehavior = iota  // close + route click
    ClickOutsideKeepRoute                               // keep open, route click
    ClickOutsideIgnore                                  // modal — suppress click
)
```

| Popup ID | Behavior | Rationale |
|----------|----------|-----------|
| `context-menu`, `references-popup`, `completion-popup`, `outline-popup`, `diag-context` | CloseRoute | Right-click / LSP popups close on outside click |
| `find-replace`, `filepicker`, `composer`, `command-palette`, `textoverlay` | KeepRoute | Tool popups stay open; click routes to editor |
| `dirty-close-prompt`, `tree-prompt`, `tree-delete-confirm` | Ignore | True modal — Esc or buttons required |

Set at creation time via `wm.WithClickOutside(wm.ClickOutsideIgnore)`.

### Architecture

- `tui/wm/window.go` — `FloatingWindow` has `clickOutside` field, `ClickOutsideBehavior()` getter, `SetClickOutsideBehavior()` setter.
- `tui/viewmodel/popupmouse.go` — All popup mouse dispatch lives here. Functions:
  - `handlePopupClickOutside(msg)` — top of `dispatchMouseClick`, before WM hit-test
  - `handlePopupContentClick(fw, msg)` — routes click on popup content to per-popup handler
  - `handlePopupContentHover(fw, screenX, screenY)` — routes motion events to per-popup hover handler
  - `closePopupWindowByID(id)` — close dispatch by ID
- `tui/viewmodel/mouse.go` — `dispatchMouseClick` calls the above. `dispatchMouseMotion` calls `handlePopupContentHover`.

### Pre-selection (hover highlight)

For each popup with selectable items (context-menu, find-replace toggles, confirm buttons, references, completion, outline), the cursor/highlight follows the mouse cursor. The mouse leaving the popup does NOT reset the cursor to 0 (matches real-system behavior).

Per-popup state for pre-selection:
- `ContextMenuState.Cursor` — item index
- `FindReplaceState.HoveredToggle` — 0=Aa, 1=\b, 2=.*, -1=none
- `dirtyPromptFocusYes` — true if mouse over Yes, false if over No
- `refCursor`, `completionCursor`, `outlineCursor` — same fields used for keyboard nav

### Focus restoration

The existing focus system uses orthogonal booleans (`treeFocused`, `tabBarFocused`, `activeEditorView`, `activeAgentView`). These persist through popup open/close — the popup just sits on top in `wm.focused`. When the popup closes, `focusTarget()` re-derives from the booleans, which still match the user's pre-popup state.

**Click-outside flow:**
1. User clicks at (X, Y) outside popup P
2. `handlePopupClickOutside` calls `closePopupWindowByID("P")` (for CloseRoute) or returns true (for Ignore)
3. The click continues to the region handler in `dispatchMouseClick`
4. Region handler sets booleans: `treeFocused = true` for sidebar click, `m.unfocus()` + editor click, etc.
5. `focusTarget()` returns the new focus

**Find-replace + click-editor flow:**
1. User clicks the editor while find-replace is open
2. `handlePopupClickOutside` sees click is outside find-replace, but find-replace has KeepRoute → falls through
3. WM.HandleMouse returns nil (click missed the popup)
4. classifyMouse → regionEditor
5. Editor handles click. WM's HandleMouse also unfocuses the popup (line 306-311 of manager.go)
6. focusTarget() returns FocusEditor
7. Subsequent keyboard goes to the editor, not find-replace

### Tests

`tui/viewmodel/popupmouse_test.go` — 19 tests:
- `TestClickOutside_*` (5) — context menu, confirm prompt, find/replace, right-click, inside-click
- `TestClickContextMenu_*` (2) — activates item, disabled item no-op
- `TestHoverContextMenu_MovesCursor`
- `TestClickDirtyPrompt_*` (2) — Yes/No button clicks
- `TestFocusRestoration_*` (5) — editor focus, tree focus, chained popups, find-replace stays open, mouse click routes to region
- `TestHoverFindReplace_ToggleHover`
- `TestClickFindReplace_*` (2) — focus field, toggle Aa

### Pitfalls

- **The `Modal()` flag in the WM only blocks clicks to OTHER floating windows**, not to root content (editor, tree). The `ClickOutsideBehavior == Ignore` mechanism is the additional layer that blocks root clicks for true modals.
- **Button position detection is approximate**: I use rough X ranges (e.g., "X<8 = Yes, X>=8 = No") because exact button positions vary with content. For pixel-precise detection, the chrome components would need to expose their hit-test rectangles.
- **Keyboard nav and mouse nav share state**: the `Cursor` field is set by both. When the user presses an arrow key, the cursor moves; when the mouse hovers, the cursor follows. The `st.Cursor` is the source of truth.
- **The `lastContextMenu` field is cleared on close**: previously leaked. The animation completed before close, so no flicker.
- **Disabled items are clickable for cursor movement but not for activation**: hover can land on disabled items (just shows the label), but click is a no-op.

---

## User Preferences (Active Session)

### Quality bar
- **"Production grade, eye-catchy, on par with giants" (VSCode/JetBrains)**. Visual polish matters as much as correctness. Beautiful, clean, minimal but with subtle sophistication. No regressions in look-and-feel when adding features.
- **No flashy hand-wavy "AI" aesthetics** (no rainbow gradients, no excessive emojis, no chatty mascots). Restrained, sharp, technical look.
- **Always: same-glyph animations** to avoid vertical jumping. Bold/faint pulse > rotating quadrant circles.
- **Animations only when something is actually happening**. Grace periods are OK to prevent blink-too-fast, but idle = static.
- **Per-tab state is sacred**. Never reload a file just because the user switched tabs. Never lose cursor, scroll, undo, or unsaved edits.

### Working style
- **One task at a time, completely**. Don't half-finish a feature and move on. If a refactor breaks the build, finish fixing the build before adding the next feature.
- **Don't do many edits in a row without building**. Run `go build ./...` and `go test ./...` after each significant change. Errors compound — fix them immediately.
- **Prefer delegation over in-process agents** for tasks that can be cleanly isolated. Use `opencode run --title "..."` with a focused prompt describing context, goal, and constraints. Reserve in-process `task` tool for exploration/research.
- **Smaller diffs over clever code**. The codebase already has utility widgets (PopupContent, FloatingWindow, WindowManager) — reuse them. Don't introduce new patterns when the existing one works.
- **Tests are not optional** for refactors that change behavior. Phase 1 added 3 tests for tab preservation; Phase 2 added 8 tests for search. Keep this pace.

### What to avoid
- Don't use the `task` (subagent) tool for implementation work — user prefers external opencode sessions via the CLI.
- Don't add `*_test.go` files that just re-test existing infrastructure; add tests that prove NEW behavior.
- Don't pile new features on a broken build. Fix the build, then add features.
- Don't introduce emojis to files unless the user asks. (Emojis in UI are OK — they're a polish detail. Emojis in code, comments, or commit messages are not.)

---

## Tab/Editor/Finder Overhaul (Phases 1–3, in progress)

### Goal
Make the TUI feel like a real IDE. The user is unhappy with three things:
1. **Tabs lose state when switching** — currently there's literally ONE `*editor.Editor` shared across all editor tabs; switching tabs calls `editor.Open(path)` which reloads from disk, destroying cursor, scroll, undo, and any unsaved edits. User called this "a complete joke."
2. **No find/replace** — no search code exists anywhere; buffer has no `FindAll` method, no UI.
3. **No right-click** — only `tea.MouseLeft` is handled anywhere.

User wants all three solved to "production grade, on par with VSCode/JetBrains":
- Per-tab state that survives switching
- Find/Replace floating popup above the active editor (Ctrl+F, Ctrl+H, F3, Shift+F3)
- Right-click context menu everywhere: editor (symbol/diagnostic aware), file tree, tabs, input, agent messages, status bar
- Keyboard shortcut for context menu: Ctrl+. (opens menu at cursor)

### Implementation Order
1. **Phase 1 (DONE):** Tab state preservation. Per-tab editor instances.
2. **Phase 2 (DONE):** Find/Replace popup + search infrastructure.
3. **Phase 3 (in progress):** Right-click context menu + Ctrl+. shortcut.

### Phase 1 — Tab State Preservation (DONE)

**Problem:** `App.editor *editor.Editor` was a single instance. `openEditorTab` called `editor.Open(path)` which reset cursor, scroll, undo, modified flag, selection, hover, chroma cache, watcher. `handleTabSwitch` also called `Open()`. Switching tabs = file reload = data loss.

**Solution:** Map of per-tab editors.

- New `tui/viewmodel/editor_tab.go`:
  ```go
  type EditorTab struct {
      Path   string
      Editor *editor.Editor
      closingWithDirty bool
  }
  func newEditorTab(path string, ed *editor.Editor) *EditorTab
  ```
- `tui/viewmodel/app.go`:
  - `App.editor` is now a pointer to the active tab's editor (still nil-safe via `IsOpen()` checks already in place).
  - `App.editorTabs map[string]*EditorTab` keyed by absolute path.
  - `openEditorTab`: if `path` already in `m.editorTabs`, just `m.win.SwitchTo` to that tab and `m.handleTabSwitch` — do NOT reload. Otherwise create new `editor.New(th)`, call `Open(path)`, register in map, point `m.editor` at it.
  - `closeEditorTab` + new `closeEditorTabForce(path string)`: if dirty, show `showDirtyClosePrompt` first; otherwise just call `closeEditorTabForce` which removes from `m.editorTabs`, closes the LSP file, closes the editor.
   - `handleTabSwitch` (in `update.go`): clears `activeEditorView`/`activeAgentView` BEFORE the early return, then re-derives from the active tab. NEVER calls `Open()`.
- `tui/editor/editor.go`:
  - Added `search *SearchState` field on Editor, initialized in `New()`.
  - Added `DiagnosticsAt(line, col) []LSPDiagnostic` (public wrapper around `diagnosticsAt`).
  - Added `DeleteSelection() bool`.

**Dirty prompt:**
- New `tui/widgets/confirmprompt.go` `ConfirmPrompt(th, question, detail, focusedYes) []string` — pre-rendered ANSI lines for a yes/no dialog, following the PopupContent pattern.
- `tui/viewmodel/app.go showDirtyClosePrompt(path)` creates a small `FloatingWindow` with id `"dirty-close-prompt"`. Uses `widgets.ConfirmPrompt` for content.
- `handleDirtyPromptKey(msg)` routes keys: ←/↑ = Yes, →/↓ = No, Tab (with Shift) toggles, Enter activates, Esc cancels. Updates `dirtyPromptFocusYes` and rebuilds content via `fw.SetContent()` + `fw.MarkContentDirty()`.
- Added `(*wm.FloatingWindow).SetContent(c widget.Widget)` so the popup can refresh content without closing/reopening.

**Tests:** `tui/viewmodel/editor_tab_test.go` — 3 tests:
- `TestEditorTab_StatePreservedAcrossSwitch` — type into A, create B, verify A is unchanged.
- `TestEditorTab_MapIndependentTabs` — edits in A don't affect B.
- `TestEditorTab_CloseRemovesOnlyThatTab` — closing A doesn't kill B.

### Phase 2 — Find/Replace Popup (DONE)

**Search infrastructure** in `tui/editor/search.go`:
- `SearchOptions{CaseSensitive, WholeWord, Regex bool}`.
- `SearchMatch{Start, End Position; Matched string}` — half-open range.
- `SearchState{Query, Replace, Options, Matches, CurrentIdx, LastCompute, Open bool}`.
- `NewSearchState() *SearchState` (CurrentIdx = -1 initially).
- `(*SearchState) computeMatches(bufText string)` — populates `Matches`. Skips empty queries and invalid regex. Supports literal substring (case-insensitive via lowercase compare, whole-word via byte-level `\b` detection), and regex (via `regexp.Regexp.FindAllStringIndex`).
- Helpers: `offsetToLineCol(text, startOff, endOff) -> (Position, Position)`, `byteOffsetToLineCol`, `isWordBoundary`, `isSearchWordChar(byte)`.

**Editor search methods** (in `search.go`):
- `Search() *SearchState` — getter.
- `SetQuery(q string) int` — updates query, sets `Open=true`, recomputes, returns match count.
- `SetOptions(opts SearchOptions)` — updates options, recomputes.
- `SetReplace(r string)` — sets replace text, no recompute.
- `SetSearchOpen(open bool)` — opens/closes, resets matches on close.
- `NextMatch() (int, bool)` / `PrevMatch() (int, bool)` — move cursor, scroll to it.
- `ReplaceCurrent() int` — replaces the match under the cursor.
- `ReplaceAll() int` — replaces from end backwards to keep offsets valid.

**Rendering** in `tui/editor/render.go`:
- New helpers: `searchHighlightAt(line, col int) (int, bool)` returns match index or -1, plus "is current" bool. Handles single-line, multi-line start, multi-line end, multi-line interior.
- New helper: `searchHighlightBg(isCurrent bool) cell.Color` — current uses `Accent2`, others use `Selection`.
- Render path: between diagnostic fg and selection bg, added a `case searchMatchIdx >= 0` branch. Current match uses `AttrReverse` for stronger emphasis.

**Find/Replace widget** in `tui/widgets/findreplace.go`:
- `FindReplaceState{ReplaceMode, FocusedField int, Query, Replace string, Cursor int, CaseSensitive, WholeWord, Regex bool, MatchCount, CurrentIdx int, OnChange, OnNext, OnPrev, OnReplace, OnReplaceAll, OnClose func(), width int}`.
- `FindReplaceLines(th, st) []string` — pre-styled ANSI lines for popup. Box-drawing input field `│ ... │`, blinking caret `▏` (single cell, no jumping), toggle row `[Aa] [\b] [.*]` with filled vs dim style, hint row at the bottom.
- `FindReplaceFromEditor(st, e)` / `FindReplaceToEditor(st, e)` — round-trip state between popup and editor.
- `VisibleWidth(s)` (wrapper around `rendering.VisibleWidth`).

**App integration** in `tui/viewmodel/app.go`:
- `App.findReplace *widgets.FindReplaceState` field.
- `openFindReplace(replaceMode bool)` — opens popup above active editor (top-right). Wires callbacks: OnChange runs `FindReplaceToEditor` + recompute; OnNext/Prev call `m.editor.NextMatch/PrevMatch`; OnReplace calls `m.editor.ReplaceCurrent`; OnReplaceAll calls `m.editor.ReplaceAll` and shows a status bar message; OnClose calls `closeFindReplace`.
- `closeFindReplace()` — sets `m.editor.SetSearchOpen(false)`, removes window, clears state, sets `m.dirty = true`.
- `refreshFindReplacePopup()` — re-renders the popup (used after every keypress). Creates the window on first call, sets content via `fw.SetContent` on subsequent calls.
- `handleFindReplaceKey(msg tea.KeyPressMsg) tea.Cmd` — full keyboard input for the popup:
  - Esc → close
  - Tab / Shift+Tab → switch between find/replace fields
  - Enter on replace field → `OnReplace`; on find field → `OnNext`
  - Shift+Enter → `OnPrev`
  - Alt+C / Alt+W / Alt+R → toggle case/whole-word/regex
  - Ctrl+Alt+Enter → `OnReplaceAll`
  - Backspace/Delete → delete char at cursor (or last char of replace)
  - Left/Right → move cursor in find field
  - Anything else with `msg.Text` → insert at cursor (find field) or append (replace field)

**Keybinds** in `tui/viewmodel/keyboard.go dispatchEditorKey` (after the LSP block):
- `Ctrl+F` → `m.openFindReplace(false)`
- `Ctrl+H` → `m.openFindReplace(true)`
- `F3` → `m.findReplace.OnNext()` if popup open, else `m.editor.NextMatch()`
- `Shift+F3` → same for previous
- `Esc` (when findReplace popup open) → `closeFindReplace`

**Routing** in `dispatchFloatingWindowKey`: when `fw.ID == "find-replace"`, route to `handleFindReplaceKey`.

**Layout** added `LayoutInfo.EditorW` field.

**Tests:** `tui/editor/search_test.go` — 8 tests (literal, case, whole-word, regex, next/prev wrap, replace current, replace all, multi-line). `tui/widgets/findreplace_test.go` — 6 tests (basic, replace, toggles, no-results, empty-query, round-trip).

### Phase 3 — Right-Click Context Menu (DONE)

**Status:** All tasks complete. 11 new tests in `contextmenu_test.go`, 9 new tests in `contextmenu_v2_test.go`, all passing.

**Context menu widget changes (contextmenu.go):**
- `ContextMenuItem{Label, Detail, Disabled, Action}`.
- `ContextMenuSection{SectionLabel, Items}`.
- `ContextMenuState{Title, Sections, Cursor, FlatItems}` with `Build()`, `Activate()`, `Next()`, `Prev()`.
- `ContextMenuLines(th, st, openDurationMs) []string` — uses unified chrome, no title, section dividers, full-row SubtleBg highlight. Supports slide-in animation via `ContextMenuLinesReveal`.
- `FlatItemAt(i)` accessor for external navigation.
- `ContextMenuPopupWidth/Height` for sizing (tight: height = content + 2 chrome rows only).
- `QuickMenu(title, items...)` helper for non-sectioned menus.

**Design rules enforced:**
- No title — top border is continuous dashes `╭───╮`.
- Disabled items: same indent as enabled, dim foreground, NO italic.
- Height: exact content + chrome — no extra bottom padding.
- Click-outside-to-dismiss: dispatchMouseClick checks `"context-menu"` containment.
- Click-on-item activation: `activateContextMenuAt()` resolves screen Y to item row.
- Slide-in animation: 80ms top-to-bottom reveal via `contextMenuOpenedAt` timestamp.

**WM/mouse/app integration:**
- `mouse.go:dispatchMouseClick`: right-click→`dispatchMouseRightClick`, left-click-outside→close menu, left-click-on-item→activate.
- `mouse.go:dispatchMouseRightClick`: right-click toggles close; otherwise builds region-specific menu.
- `mouse.go:clampPopupPosition()`: keeps popup on-screen.
- `app.go:lastContextMenu`, `contextMenuOpenedAt` fields for animation.
- `app.go:activateContextMenuAt(sx, sy)`: resolves screen Y to menu item, activates it.
- `app.go:contextMenuLinesForFrame()`: returns animated lines for first 80ms, full menu after.
- `keyboard.go`: Ctrl+. wired via `showContextMenuAt` (Phase 3b).

### Key Files for Resuming
- `tui/viewmodel/editor_tab.go` — EditorTab struct (new, 22 lines)
- `tui/viewmodel/editor_tab_test.go` — 3 tests (new)
- `tui/viewmodel/app.go` — `App` struct with new `editorTabs`, `findReplace`, `lastContextMenu` fields; `openEditorTab`, `closeEditorTab`, `closeEditorTabForce`, `showDirtyClosePrompt`, `handleDirtyPromptKey`, `openFindReplace`, `closeFindReplace`, `refreshFindReplacePopup`, `handleFindReplaceKey`, `buildEditorContextMenu`, `buildSidebarContextMenu`, `buildTabBarContextMenu`, `buildInputContextMenu`, `buildChatContextMenu`, `closeContextMenu`, `handleContextMenuKey`, `refreshContextMenu`, `showContextMenuAt`, `pathUnderMouse`, `copyEditorSelection`, `cutEditorSelection`, `copyToClipboard`, etc.
- `tui/viewmodel/update.go` — `handleTabSwitch` now clears `activeEditorView`/`activeAgentView` BEFORE the early return. **Always resets state, then derives from the active tab.**
- `tui/viewmodel/mouse.go` — `dispatchMouseClick` routes right-click to `dispatchMouseRightClick`. `clampPopupPosition` for popup placement. `activateContextMenuAt` for click-on-item.
- `tui/viewmodel/keyboard.go` — `dispatchEditorKey` has find/replace/Ctrl+. slots (Ctrl+. now wired via `showContextMenuAt`). `dispatchFloatingWindowKey` routes `"find-replace"`, `"dirty-close-prompt"`, `"context-menu"`, `"tree-prompt"`, `"tree-delete-confirm"`.
- `tui/editor/search.go` — SearchState, SearchOptions, SearchMatch, computeMatches, all editor search methods.
- `tui/editor/render.go` — `searchHighlightAt`, `searchHighlightBg` integrated into the cell-by-cell render.
- `tui/widgets/findreplace.go` — `FindReplaceState`, `FindReplaceLines`, `FindReplaceFromEditor`, `FindReplaceToEditor`.
- `tui/widgets/contextmenu.go` — `ContextMenuItem`, `ContextMenuSection`, `ContextMenuState`, `ContextMenuLines`.
- `tui/widgets/confirmprompt.go` — `ConfirmPrompt` for the dirty-file prompt.
- `tui/wm/window.go` — added `SetContent` method.
- `tui/filetree/tree.go` — added `VisibleRows`, `IsDir`, `ParentDir`, `ExpandToPath`.

### Pitfalls / Gotchas When Resuming
- **Two duplicate function blocks** were accidentally added to `app.go` during a botched edit. If the build fails with "redeclared in this block", run `grep -n "func (m \*App) closeContextMenu" tui/viewmodel/app.go | sort -k2` to find duplicates.
- `m.editor.PadL` is used in `routeEditorMouse` to translate mouse X — already wired, don't break it.
- `m.lastSettings.Editor.FormatOnSave` is applied in `openEditorTab` — keep it on every new tab.
- `m.findReplace.Replace` is a string (not `[]rune`); use `len()` not `utf8.RuneCountInString()` for length math.
- The popup's `fw.SetContent` is the only way to refresh without closing/reopening. The `dirty = true` flag must be set after refresh or the view cache will serve stale content.
- The `m.findReplace` state holds the popup's text but the editor's `Search()` is the source of truth for matches. Always read match count from `m.editor.Search()` after mutations.
- For right-click: the existing LSP `requestDefinitionCmd` returns a `tea.Cmd` — the context menu's `Action` callbacks can't return Cmds, so we use `_, _ = m.requestDefinitionCmd(), 0` to discard. The Cmd fires asynchronously, so the result still arrives via the normal `lspDefinitionResultMsg` path.
- For tree operations: `m.treeModel.ExpandToPath` was just added — make sure to call it after any tree mutation so the new path is visible.
- The chat input paste: `m.input.Paste()` may already exist. Check before writing a new implementation.
- **ANSI search false-matches**: NEVER use `strings.Index(rawLine, label)` on an ANSI-styled string — the label text can match inside escape codes (e.g., `"Off"` in an ANSI code). Always `stripAnsi` first, then search plain text.
- **UTF-8 byte positions**: Box-drawing chars like `│` (U+2502) are 3 bytes. Byte-position assertions break if you count them as 1. Use `len([]rune(s))` for char count, or account for the 3-byte offset.
- **`buildTopBorder` has 4 separate cases** (no title/icon, icon only, title only, both). Each case has a unique fixed-width formula. Changing one case without updating the others creates off-by-ones. Always recompute from scratch per case — do NOT use a single unified formula with conditionals for the spacer.

### Pre-existing Test Failures (NOT caused by this work)
- `crude/safety/TestProjectWriteCommands`, `TestDestructiveCommands`, `TestUnknownCommand` — pre-existing. Don't fix unless asked.
- `crude/tui/widgets` had a `padRight` collision between `textoverlay.go` and `contextmenu.go`. Resolved by renaming the new one to `padRightMenuItem`. Keep an eye on it.

### Test Commands
```bash
go build ./...                              # all packages
go test ./tui/editor/... ./tui/viewmodel/... ./tui/widgets/...  # the three packages this work touched
go test ./... 2>&1 | grep -v "^ok\|cached\|^?"  # find any actual failures
```

---

## Hover, Focus, Clipboard & Dialog Refactor (Phases 0–4, in progress)

### Goal

Make the TUI feel like a real GUI: hover-highlights on every interactive element, MS-Edit clipboard shortcuts (Ctrl+C/X/V/A/Z/Y) working everywhere, and a dialog system where per-agent dialogs auto-promote from inline (focused agent) to popup (background agent) and stack like a real window manager.

**Hard rules from the user:**
- Option A (harder but architecturally cleaner) ALWAYS wins over Option B (faster).
- Esc and Ctrl+Q are universal back/close. Ctrl+C is NEVER back/cancel — it's the copy shortcut.
- Don't delete `components/dialog.go` or `components/buttons.go` — they're the inline-rendering backend and are still used. Add to them; don't replace them.

### What's Done (this round)

**Foundations (Phase 0)**
- **`tui/widget/hoverable.go` (new)** — `widget.Hover` utility: `NewHover()`, `Set(idx)`, `Clear()`, `IsActive()`, `Index`. Single source of truth for the "which item is hovered" concept. Index `-1` means "no item". Embedded by every hover-capable widget.
- **`tui/theme/theme.go`** — Added `Colors.Hover` (`#21262d`) and `TreeColors.Hover` (same hex). Used uniformly for hover backgrounds across all widgets.
- **`tui/components/buttons.go`** — `ButtonGroup` now has full mouse support: `Hover widget.Hover` field; `MouseMotionMsg` updates hover; `MouseClickMsg` activates the hovered button; `SetHover(idx)` returns true on change. New `buttonRender` helper extracted so the hit-test math (`buttonAtX`) uses the same width as the render. **5 new tests in `buttons_test.go`.**
- **`tui/components/dialog.go`** — `Dialog.Update` forwards all messages (including mouse) to its ButtonGroup. Inline-rendered dialogs (Confirm, etc.) now respond to mouse hover/click.

**Hover effects (Phase 1)**
- **File tree (`tui/filetree/{tree,update,render}.go`)** — `Hover` field on `FileTreeModel`; `handleMouseMotion(tea.MouseMotionMsg) bool` updates it; `formatLine` + `Render` apply hover background. **Background priority: cursor > selected > hovered.** Mouse-outside-tree clears hover (`m.treeModel.Hover.Clear()` in `mouse.go:578`). 2 tests in `tree_hover_test.go`.
- **Tab bar (`tui/window/window.go` + `tui/viewmodel/mouse.go`)** — `Window.Hover` field; `renderHorizontalTabItem` + `renderVerticalTabItem` take `isHovered bool`; motion in `dispatchMouseMotion` uses `m.win.HitTest(x, 0)` to find the tab. Subagent did this. 3 tests in `window_hover_test.go`.
- **Command palette (`tui/viewmodel/command_palette.go`)** — `Hover` field + `lineToItem []int` map populated during `View()`; `handleMouseMotion` translates screen Y → item index; updates `m.idx` so the existing render highlight follows the mouse. Subagent did this. 3 tests in `command_palette_hover_test.go`.
- **Confirm dialog** — Motion in `dispatchMouseMotion` is forwarded to `m.confirm` when the mouse is in the overlay region; ButtonGroup updates hover; render shows the highlight.

**Clipboard fix (Phase 3)**
- **`tui/form/item.go` — TextItem full MS Edit** — Selection state (`selStart`/`selEnd int` with -1 = no selection). Public API: `HasSelection()`, `SelectedText()`, `SetSelection(start, end)`, `ClearSelection()`, `SelectAll()`, `deleteSelection()` (private). HandleKey handles: Ctrl+C (copy), Ctrl+X (cut), Ctrl+A (select all), Ctrl+U (clear line), shift+arrow (extend), Home/End. HandlePaste replaces selection. View applies `theme.Styles.Highlight` background to the selected range via three new helpers (`applySelectionToBefore/Mid/Right`) that split the value into pre-cursor, cursor-cell, post-cursor and apply the selection bg to whichever piece falls inside the range. 10 tests in `textitem_test.go`.
- **`tui/form/form.go`** — Removed the `if keyMsg.String() == "ctrl+c" { return m, nil, FormActionBack }` intercept. **Esc is now the only "back" affordance in forms.**
- **Editor (`tui/editor/events.go`)** — Added Ctrl+X (cut: copy + delete selection, no-op if no selection) and Ctrl+V (paste: read clipboard, emit `tea.PasteMsg` for the editor's existing paste handler — same code path as bracketed paste).
- **Find/Replace popup (`tui/viewmodel/app.go`)** — Added MS Edit shortcuts: Ctrl+C (copy active field), Ctrl+X (cut), Ctrl+A (no-op for now, keyboard parity), Ctrl+V (paste). New `pasteIntoFindReplace(fr)` helper reads clipboard synchronously and inserts at the cursor.
- **`dispatchPasteMsg` (`tui/viewmodel/keyboard.go`)** — Complete rewrite. Previously: editor OR chat input. Now: editor / settings (ModeSettings → `m.settings.Update(msg)`) / overlay (other modes) / floating window content (via new `pasteToWindowContent(fw, msg)` helper that probes for `HandleEvent(tea.Msg) tea.Cmd` or `Update(tea.Msg) tea.Cmd`) / chat input (default). **This is the fix for "Ctrl+V doesn't work in settings".**

**Tests added (27 total, 0 regressions)**
- `tui/widget/hoverable_test.go` — 4 tests for the Hover utility
- `tui/filetree/tree_hover_test.go` — 2 tests for tree hover wiring
- `tui/window/window_hover_test.go` — 3 tests (subagent)
- `tui/viewmodel/command_palette_hover_test.go` — 3 tests (subagent)
- `tui/form/textitem_test.go` — 10 tests
- `tui/components/buttons_test.go` — 5 tests

### Files Touched This Round

Modified (14): `components/{buttons,dialog}.go`, `editor/events.go`, `filetree/{tree,update,render}.go`, `form/{form,item}.go`, `theme/theme.go`, `viewmodel/{app,command_palette,keyboard,mouse}.go`, `window/window.go`

New (6): `widget/hoverable.go` + test, `components/buttons_test.go`, `filetree/tree_hover_test.go`, `form/textitem_test.go`, `viewmodel/command_palette_hover_test.go`

### What's Remaining Open

**HIGH PRIORITY — the user's explicit dialog-stacking request**

The user described: "user is currently focused on the agent that showing a dialog? → inline (current), no? → popup (like if he is actively coding by hand in another file or attending another agent), and we need a neat way to stack these windows on top of each other like a real windows composer."

#### Phase 2.2 — DialogStack data model (NEXT, ~30 min)

Create `tui/viewmodel/dialog_stack.go` (new file). The model:
```go
type DialogKind int  // DialogConfirm, DialogYolo, DialogQuestion, DialogFileEdit, etc.
type DialogRequest struct {
    ID       string       // unique per request
    Kind     DialogKind
    AgentID  string       // which agent asked
    // Per-kind payload. Either a struct (Confirm/Yolo) or a JSON blob
    // (Question, since the questions schema is dynamic).
    Payload  interface{}
    Mode     RenderMode   // DialogInline, DialogPopup
    OpenedAt time.Time
}
type DialogStack struct {
    mu       sync.Mutex
    byAgent  map[string][]DialogRequest  // FIFO per agent
    popups   []DialogRequest             // currently-shown popups
    topIdx   int                         // focused popup in stack
}
```

The stack is a FIFO queue **per agent**. When a new dialog arrives:
1. If `m.activeAgentView == req.AgentID` and the existing inline-overlay slot is free → render inline (current behavior).
2. Otherwise → push to `byAgent[req.AgentID]` AND create a popup floating window.

When the user focuses an agent that has pending popups for OTHER agents, the popups stay (they're explicit windows). When the user focuses an agent that has its own pending popups (because the user previously was on another agent), the popup stays as a popup unless the user explicitly dismisses it — auto-promotion from popup→inline is NOT a thing. Auto-promotion is only inline→popup at creation time.

API surface:
```go
func (m *App) PushAgentDialog(req DialogRequest)
func (m *App) ResolveAgentDialog(id string, result any)  // removes from stack + dismisses popup
func (m *App) PendingDialogsFor(agentID string) []DialogRequest
func (m *App) HasOpenDialogs() bool  // used by dispatcher to mark "stacking required" in status bar
```

#### Phase 2.3 — DialogPopup wrapper

Create a new widget that wraps any `Dialog` data into a popupchrome-styled `FloatingWindow` content. Lives in `tui/widgets/dialog_popup.go`. The widget knows how to render:
- Confirm → `widgets.ConfirmDialog` content (already popupchrome-compatible via the existing styles)
- YOLO → `widgets.YoloDialog` content
- Question → `widgets.QuestionForm` content (compressed; full form is too large for a popup, so show summary + "open in overlay" link)
- FileEdit → `widgets.TextOverlay` content (just reuse — it's already a floating window pattern)

Position: cascade. First popup at center; second at +2,+2; etc.

#### Phase 2.4 — Auto-promotion logic

In `App.handleAgentEvent` (wherever dialog requests currently come in), wrap each kind with `PushAgentDialog(req)`. The push function checks `m.activeAgentView` vs `req.AgentID` and chooses mode.

For each existing dialog entry point:
- `m.confirm.ResetWithDetails(...)` and `m.confirm.Reset(...)` — wrap with `PushAgentDialog` of kind `DialogConfirm`. The legacy `m.confirm` field becomes the inline slot for the focused agent's confirm; new popups are created via `DialogPopup`.
- `m.yolo.Show()` — same pattern.
- `m.questionForm.Reset(...)` — same pattern.
- `m.textOverlay.Open(...)` — same pattern.

The inline slot is the existing rendering path. **Don't break the existing inline rendering** — the new path is additive: a new popup gets created IN ADDITION to keeping the existing inline. The inline is hidden when a popup is open for the same dialog (so the user only sees one of them).

Actually, simpler design: the inline slot is per-active-agent. When a new dialog arrives:
- If `activeAgentView == agentID` → render inline (use existing code path, no popup)
- Else → create popup, leave inline alone

When the user switches to the agent that has popups, the popups STAY as popups. The user can dismiss them explicitly. This is the cleanest semantics: inline is "the dialog the user is actively looking at"; popup is "the dialog the user wants to acknowledge later".

#### Phase 2.5 — Window stacking polish

Already 90% there via the WM z-order. Add:
- Cascade positioning: new popups offset by `(idx*2, idx*2)` from center, capped at +10,+10.
- A small stack indicator in the status bar: when 2+ dialogs are open, show `▤ N dialogs` in the status bar; click it (or `Ctrl+Shift+D`) to cycle through.
- "Bring to front" on click: `m.windowManager.BringToFront(fw)` — already exists, just wire it.
- When a popup is dismissed, focus moves to the next popup in the stack (if any). When the last popup is dismissed, focus returns to the previous focus target (chat input or editor).

#### Phase 2.6 — Unify composer + textoverlay (~20 min)

`tui/widgets/composer.go` and `tui/widgets/textoverlay.go` are nearly identical. Both wrap `components.TextInput`, both have `View()` and `Render()`, both use the same padding/hint. The only meaningful differences:
- Composer: `ctrl+enter` submits, `enter` newline
- TextOverlay: `ctrl+s` saves, `enter` newline, has a `refID` + `title` for tracking

Build `tui/widgets/texteditor.go` (new) with a single widget that takes config:
```go
type TextEditorConfig struct {
    SubmitKey     string  // "ctrl+enter" or "ctrl+s"
    Title         string
    RefID         string
    OnSubmit      func(text string)
    ...
}
```
Then `composer.go` and `textoverlay.go` become thin wrappers (or just call sites that construct the new widget with their config).

#### Phase 2.7 — Dual-mode yolodialog + questionform

`tui/widgets/yolodialog.go` and `tui/widgets/questionform.go` currently render as overlay tabs (full-page). Wire them to the DialogStack system so they can also be shown as popups. Reuse their existing widget; just plumb the request through PushAgentDialog.

#### Phase 2.8 — Dual-mode confirmdialog

Same pattern. `tui/widgets/confirmdialog.go` already has its `components.ButtonGroup` with mouse support; the wrapper is straightforward.

**MEDIUM PRIORITY — more hover widgets**

#### Phase 1.4 — File picker hover
File: `tui/widgets/filepicker.go` (497 lines). Add `Hover widget.Hover` field. In `Update`, handle `tea.MouseMotionMsg`: compute which entry is under the mouse (Y - titleRow), set hover. In the entry list render, apply `theme.Colors.Hover` to the hovered entry. Currently filter is at line 302 (styled string, not input field) — could also migrate to `PopupInputField`.

#### Phase 1.5 — Launcher hover
File: `tui/widgets/launcher.go` + `tui/viewmodel/launcher.go`. The launcher has sessions listed; each session row should hover-highlight on mouse over.

#### Phase 1.6 — Message list rows hover
File: `tui/widgets/messagelist.go` (1511 lines). Each message row is interactive (text selection drag exists). Add per-row hover highlight. Mouse dispatch already routes motion to `activeML.Update(msg)`; just add hover state to the model.

#### Phase 1.7 — Status bar segments
File: `tui/widgets/statusbar.go` (642 lines). The status bar has clickable-ish segments (LSP dot, error toasts). Currently NO mouse handling. Add motion tracking + per-segment hover. This is the lowest-priority hover (the bar is mostly passive).

#### Phase 1.8 — Form items hover
File: `tui/form/form.go`. Each form item (TextItem, SelectItem, CycleItem, NavItem) gets a subtle hover background on the WHOLE row (not just the input field). The hover is independent of focused.

#### Phase 1.9 — Chat input bar hover
File: `tui/input/input.go`. The chat input bar has buttons / hints; hovering them should highlight.

**LOW PRIORITY — cleanup**

#### Phase 2.9 — Dead code removal
- `tui/widgets/findreplace.go:196-213` — `stripANSIForWidth` is dead code, duplicates `rendering.StripAnsi`. Delete.
- `tui/widgets/findreplace.go:259-267` — `stripAnsi` and `VisibleWidth` are unused exports. Delete.
- `tui/widgets/statusbar.go:75-77` — `diagErrors/diagWarnings/diagHints` legacy fields, never read by `View()`. Delete.
- `tui/widgets/statusbar.go:578-595` — `SetDiagnostics` legacy method, superseded by `SetProjectInsights`. Delete.
- `tui/viewmodel/mouse.go:876-883` — `padRight` duplicates `widgets.padVisibleToWidth`. Delete the local copy.

#### Phase 3.6 — Ctrl+C/V on generic floating windows
The `dispatchFloatingWindowKey` generic handler (in `tui/viewmodel/keyboard.go:356-368`) forwards events but doesn't handle Ctrl+C/V. The current popups that need it: text overlay (TextInput-based), composer (TextInput-based), file picker. Most of these have their own dispatch path; the generic handler is for unknown popups. Add a passthrough that calls the same PasteMsg routing.

**TEST COVERAGE GAPS (Phase 4 remaining)**

#### Phase 4.3 — Hover dispatch tests
`tui/viewmodel/` has popupmouse_test.go (19 tests) but no `dispatchMouseMotion` tests. Add: tree motion sets hover, tab bar motion sets hover, mouse-outside-tree clears hover, mouse-outside-tab clears hover. Pattern: see existing `TestClickContextMenu_*` tests in `popupmouse_test.go`.

#### Phase 4.4 — Clipboard routing tests
`dispatchPasteMsg` is now complex. Add tests for: editor paste routes to editor, settings paste routes to settings form, floating window paste routes to content, default routes to chat input. Pattern: see `TestClickOutside_*` in `popupmouse_test.go`.

#### Phase 4.5 — Dialog stack tests
After 2.2 is built. Tests: push to focused agent renders inline, push to non-focused agent creates popup, resolve removes from both stack and popup, multiple popups stack in order.

#### Phase 4.6 — Full build + test cycle
```bash
go build ./...
go test ./tui/... 2>&1 | grep -v "^ok\|cached\|^?"
```

### Immediate Actions (in order)

The next agent should do these in sequence, in one session:

1. **Build Phase 2.2** (DialogStack) — this unblocks everything else. ~30 min.
2. **Build Phase 2.3** (DialogPopup wrapper) — needs 2.2 done. ~45 min.
3. **Build Phase 2.4** (auto-promotion) — wires 2.2+2.3 into the existing flow. ~45 min.
4. **Build Phase 2.5** (stacking polish) — cascade, status bar indicator, focus restoration. ~30 min.
5. **Build Phase 2.6** (composer+textoverlay unification) — independent, can be done any time. ~20 min.
6. **Build Phase 2.7 + 2.8** (yolo/question/confirm dual-mode) — depends on 2.2+2.3. ~60 min combined.
7. **Build Phase 1.4–1.9** (more hover) — can be delegated to subagents one at a time. ~5-10 min each via subagent. 6 widgets = ~60 min.
8. **Build Phase 2.9** (dead code) — quick wins, do last. ~10 min.
9. **Build Phase 3.6** (generic clipboard) — ~10 min.
10. **Build Phase 4.3–4.5** (more tests) — parallelizable after the implementation is done.

Total estimate: ~5-6 hours of focused work. The user has indicated willingness to delegate mechanical pieces to subagents — the hover work (Phase 1.4-1.9) is the most subagent-friendly since each widget follows the same pattern (embed `widget.Hover`, add motion case, apply highlight in render). The dialog system (Phase 2.2-2.5) is the most architecturally complex and should be done by the main agent.

### Design Decisions Already Locked In

- **`widget.Hover` is the canonical hover type.** Don't create a new one.
- **`theme.Colors.Hover` is the canonical hover background colour.** `#21262d`. Don't add per-widget hover colors.
- **Hover priority: focused (keyboard) > selected > hovered > nothing.** This is the rule applied across the file tree, tabs, and ButtonGroup. Keep this order for new widgets.
- **`Esc` is back, `Ctrl+Q` is close, `Ctrl+C` is copy.** Never reuse Ctrl+C for navigation.
- **Bracketed paste (`tea.PasteMsg`) is the primary paste mechanism.** Ctrl+V (KeyPressMsg) is a fallback that emits a `tea.PasteMsg` via closure. Don't duplicate the paste logic.
- **Dialog system is per-agent FIFO.** Inline shows only for the focused agent; everything else is a popup. No auto-promotion from popup back to inline.
- **Stack-based dialog popups (no auto-popup→inline).** User explicitly dismisses. Focus restoration: when last popup closes, focus returns to the previous target.
- **`components/dialog.go` + `components/buttons.go` are the inline-rendering backend.** Don't delete them. The popup rendering path uses `popupchrome` directly (via the new `DialogPopup` wrapper).
- **Tests follow the existing pattern**: see `tui/window/window_hover_test.go` (subagent-built) and `tui/form/textitem_test.go` (manually-built) for templates.

### Subagent Patterns (verified working)

Subagents can safely do mechanical work IF the prompt is tight and specifies:
1. The exact `widget.Hover` import path and API.
2. The exact existing struct field for the widget to modify.
3. The existing render loop / hit-test to extend.
4. "DO NOT" list of files NOT to touch.
5. "Run `go build ./...` after every change."
6. "Return: file:line changes + final test result."

The two subagent tasks that worked:
- Tab bar hover (Phase 1.2)
- Command palette hover (Phase 1.3)

The user's warning: "agents are extremely dumb and they might clash or write bad code". So:
- Run ONE subagent at a time, in a different package than the main agent is working in.
- Verify the subagent's output (read the diff, run the tests) before moving on.
- Subagents should NOT touch `tui/viewmodel/app.go` (too large, too interconnected).

### Pitfalls / Gotchas

- **`m.dirty = true` is set on every Update call** (`tui/viewmodel/update.go:26`), so motion events naturally re-render. Don't over-engineer the dirty tracking for hover — the simple `if widget.Hover.Set(idx) { m.dirty = true }` is sufficient.
- **Dialog system: the existing `m.confirm`/`m.yolo`/`m.questionForm` fields are SINGLETONS.** The new DialogStack needs to coexist with them. Cleanest: keep the singleton for the inline-rendering path, and have the DialogStack track the additional popup-required dialogs. When `m.activeAgentView` switches, the singleton's "current dialog" gets either dismissed or re-rendered as a popup.
- **WM stacking: the existing z-order handles popups correctly.** New popups at `AddWindow` go on top. Don't reinvent.
- **`clampPopupPosition` exists** in `tui/viewmodel/mouse.go:976`. Use it for new popup placement.
- **The `lastContextMenu` and `contextMenuOpenedAt` fields** are for the context menu's slide-in animation, NOT for the dialog system. Don't conflate.
- **For Phase 2.6 (unify composer+textoverlay)**: `components.TextInput` has `Selecting`, `HasActiveSelection`, etc. (lines 571-653 in `tui/components/text.go`). The selection API is there but not wired to keyboard/mouse. **The new TextEditor should also wire up Ctrl+C/X/V/A** to TextInput, mirroring what TextItem does. Otherwise the unified widget loses functionality.
- **Box-drawing characters in ANSI** (e.g., `│` = 3 bytes in UTF-8) — byte-position assertions break. Use `rendering.VisibleWidth(s)` for char counts. The shared `widgets.padVisibleToWidth(s, width)` is the right pad helper.
- **For popup positioning: the WM uses screen-absolute (X, Y) coordinates.** Don't forget to translate the local-popup-content (X, Y) to screen (X, Y) using the floating window's `Position()`. Look at `m.commandPaletteModel.Update` in `tui/viewmodel/command_palette.go` for the pattern.


