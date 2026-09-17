# Dialog & Prompt Design Language

> Status: **Authoritative**. Every modal dialog, floating prompt, and confirm
> window follows this language. Reviewers reject deviations; do not add
> exceptions without updating this document.

Standardized 2026-08-28 after the confirm-prompt redesign. The reference
implementation is `tui/ui/confirm_frame.go` (confirms) and
`tui/ui/textprompt_frame.go` (text prompts).

## Chrome — WindowFrame only

- Every dialog is a `ui.WindowFrame` on the `FrameDialog` role, hosted in a
  `wm.FloatingWindow` via `ui.ComponentWidget`. No hand-drawn borders, no
  bespoke chrome, no popup-chrome escape hatch.
- **One icon per dialog**: the severity glyph lives in the title bar
  (`frame.SetIcon`). Content text never repeats it. Severity glyphs: danger
  `✕`, warning `⚠`, question `?`, info `i` — sourced from the severity enum,
  never inline literals in product code.
- Severity tints the border accent (`SetBorderAccent`): danger → `ColorError`,
  warning → `ColorWarning`, otherwise default popup chrome.
- Dialogs are **movable by the frame title row** (`wm.WithMovable(true)`).
  Row 0 of the frame is the title bar whether or not WM decoration is on.
- Modals keep `WithModal(true)` + outside-click ignore: click outside never
  silently dismisses a destructive dialog; the user must choose. Z-order is
  the standard WM stack — a modal blocks clicks to windows below it, and it
  may itself be stacked above other windows (nested confirm over picker).

## Content

- Question: one line, bold `ColorFg` on `ColorPopupBg`. No icon prefix.
- Detail (path, filename): muted, wrapped to dialog width — word wrap, and
  `/`-boundary breaking for path-like tokens (`splitLongToken`). Optional
  `DetailMaxLines` cap collapses overflow to `…`.
- Spacing comes from a single spacer row above the action row; the frame
  chrome supplies the rest.
- Dirty-state markers (`●`) never enter dialog text: use `editor.BaseTitle()`
  when the tab title carries the modified marker.

## Mode strips

Dialogs that switch modes (Status Overview's Tabs/Workspace/Activity/
Notifications, Workspace Settings' Application/Workspace scope) use the shared
`ui.Tabs` strip — no bespoke segmented switch, no local painter. The strip
reserves its own two rows: labels on the first, baseline rule on the second,
active mode in accent + accent underline, heavy accent baseline while the strip
holds focus. In a dialog footer the strip's rule and the command row follow the
Status Overview layout exactly: labels, one rule, then the command row flush on
the bottom row — never a blank row between them. Full contract:
[`docs/reference/control-usage.md`](./control-usage.md) Tab bar contract.

## Buttons

- Sized to their labels, packed left with gap 2 (`Stack` with expand-last
  **off** — the default). A full-width accent slab in a dialog is a bug.
- **Variant by action semantics** (`actionVariant`), not dialog severity alone:
  - Save / Allow / affirmative → `VariantPrimary` (accent fill on focus)
  - Discard / Deny / Delete / destructive → `VariantDanger` (error fill)
  - Cancel / No / neutral → `VariantSecondary`
  - Generic `Yes` follows dialog severity (yes on a danger dialog is danger).
- Order: affirmative, destructive, then Cancel last (rightmost).

## Control states (buttons, rows — mirrors `ButtonColors`/`ListRowColors`)

Precedence: disabled > focused > selected > pressed > hovered. Colors resolve
only through `TokenSet` semantic roles — never raw theme hex.

- **Keyboard focus** = variant accent fill + contrast fg + bold.
- **Mouse hover** = subtle `ColorHover`/`ColorPopupButtonBg` tint — never the
  accent/variant fill. Hover and focus must be distinguishable at a glance:
  pointer hover must never fake keyboard focus, and hovering must not steal
  focus (`SetConfirmHover`, not `SetConfirmFocus`).
- Dialog focus moves with arrows/Tab (`ui.MoveConfirmFocus`) **in place** —
  never rebuild the component tree for a focus change. Every visual change
  must mark window content dirty (`MarkContentDirty` bridged via
  `ComponentWidget.SetMarkContentDirty`, wired automatically by
  `wm.NewFloatingWindow`): Host paint flags alone never reach the compositor.

## Hints

- Standard hint row, muted italic: `←→ switch · enter confirm · esc cancel`.
- Text prompts: `enter <verb> · esc cancel` with the kind-specific verb
  (create/rename/submit).

## Interaction defaults

- Arrows/Tab (Shift+Tab reverse) move focus; Enter activates the focused
  button; Esc triggers the safe action (cancel).
- Direct letter shortcuts per dialog are optional (S/D/C, Y/N) and must be
  mirrored in the hint row when present.
- **Safe default focus**: destructive dialogs land on the non-destructive
  choice (Cancel / No) so accidental Enter cannot destroy work.

## Compositor contract (for any in-place visual change)

Floating windows composite from cached surfaces; content re-renders only when
`FloatingWindow.contentDirty` is set. Any code that changes dialog visuals
without a tree rebuild must end in a path that calls
`ComponentWidget.MarkContentDirty()`.

## Test checklist for new dialogs

1. Label-sized buttons (no stretch).
2. Severity border accent + single title icon.
3. Variant per action semantics.
4. Focus move repaints (content-dirty), wraps, Enter/Esc act on the right
   button, safe default focus.
5. Hover ≠ focus fill; hover does not steal focus.
6. Long detail wraps; no stray markers in text.