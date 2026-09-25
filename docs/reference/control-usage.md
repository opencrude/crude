# Control Usage Map

> Status: Current reference. Maps control categories to their `tui/ui` implementations.

Maps current interactive control categories to their implementations and the
target replacement under `tui/ui/`.

## Text entry

| Category | Current implementations | Target |
|----------|------------------------|--------|
| Full input engine | Chat via `shellinput` + `ui.TextArea` (shell `ColorBg`, not popup fill, no leading `❯`); composer + text overlay via `ui.TextArea` screens; find/replace via `ui.TextEdit` + `ui.TextField` (`BuildFindReplaceFrame`) | `ui.TextField` / `ui.TextArea` |
| Project search query | native `ui.SearchField` in `tui/screens/findinfiles` (SearchGlyph + PromptStart; Cc/.* toggles on the right) | same |
| Project replace field | native `ui.TextField` on the Find in Files sibling (Replace in Files) | same |
| Form field editor | native `ui.Form` / `FormField` / `TextField` (workspace/session/question/background); `FieldSelect` paints a bordered overlay dropdown (`PaintOverlay`) so opening it does not grow form height or push later fields; `Form.Paint` clips to the viewport so scrolled fields cannot leak into chrome; docked forms use `SetArrowExitsForm(true)` so Up/Down cycle onto the ActionBar; floating settings also exit to the category rail; Esc emits `form.cancel`; Tab/Shift+Tab still leave the form; compact `FieldNav`/`FieldCycle` share `planCompactTable`: leftover width becomes space after the label and before the last column (never filler periods), `·` is a separator only, labels/values use `ColorFg`, inherited/meta use `ColorMuted`, focused row uses `ColorHover` + accent label. Lists/menus use the same `TableMinGap` + `MenuInk`. Trailing `TreeCollapsed` (`▸`) on nav only. Never `❯`. `FieldKeyValues` (`ui.KeyValueGrid`) edits a keyed list as a Name/value table below the label — `Name`/`Value` captions, an `Add header` row, row removal with the shared `Close` glyph, arrows move rows/cells, `MultiLine()` keeps Up/Down inside the table, and `Form.moveFocus` blurs the open cell editor. Row states reuse `ListRowColors`/`MenuInk` (focused accent, selected, hovered). | `ui.Form` / `FormField` over shared TextEdit engine |
| Hand-built prompt | `tui/widgets/textprompt.go` (product: TextEdit + WindowFrame) | `PromptField` |
| Append/backspace filter | palette, launcher, settings, session model/provider pickers | `FilterField` / `SearchField` with `SearchGlyphEnd` + `PromptEnd` (magnifier at the trailing edge) and `FilterSurface` (`ColorPopupSubtleBg`) so the input never shares the list/dock fill |
| File/workspace picker filter | native `ui.TextField` in `tui/screens/filepicker` | same |
| Document editor | `tui/editor/` PieceTable; main viewport `shelleditor` Host; gutter paints diagnostics then Git line edits (`+`/`~`); blame is status-bar + editor context menu, not a sidebar | Specialized `DocumentEditor` (kept) |

## Buttons

| Current | Target |
|---------|--------|
| Confirm / dirty / tree-delete ActionBar | `ui.ActionBar` / `ui.Button` via `BuildConfirmFrame` and friends. Floating bars right-align (`alignEnd`); `NewDockedActionBar` packs from the left with a top rule. Idle buttons use variant-colored `[ Label ]` on the surface; hover/focus invert — variant color becomes the fill, `cell.ContrastColor` picks a readable fg. Up/Down leave the bar (`actionbar.exit.*`) so forms and buttons cycle together; Left/Right wrap among buttons. |
| Find in Files / Replace in Files | native `ui.ActionBar` (Open/Close; Replace/All on the replace row), right-aligned |
| `PopupButton` / `PopupButtonRow` | same Button + ActionBar |
| Agent question actions | native `ui.ActionBar` in `tui/screens/question` | same |
| Tool confirmation / YOLO actions | native PanelDeck `ui.ActionBar` with typed quick keys and Escape actions | same |
| Footer workspace-panel controls | `tui/screens/shellfooter` | Contiguous theme-derived strip: `Problems`, `Notices`, `Terminals`, `Workspace`, with color boundaries and no gaps. `TokenSet.StatusStripColors` blends each hue into the current shell theme: idle / attention / open-panel weights are 55% / 75% / 90%, with WCAG-contrasting ink and bold active labels. Problems uses error/warning only for real issues; unread notices, live terminals, and background work/input change their controls' color or strength. Previous sessions are actual searchable, date-grouped rows inside Workspace, resumed directly without a picker popup. |

## Collections

| Current | Target |
|---------|--------|
| Command palette list | sectioned VirtualList |
| File tree | `ui.Tree` via `shelltree` |
| Activity lists (Agents / Terminals / Git) | `ui.List` via `activitylist` |
| Message list | ✅ `chatprojection.Renderer` export; `shellchat` paints message-level `ui.VirtualList`; all rows native `PaintLine` (markdown/thinking bridged at render time via `BridgeAnsiLine`); main `ScrollbarControl` |
| Context menu | `ui/overlay.ContextMenu` / WindowFrame + List |
| Tabs | `ui.Tabs` only for actual content/mode tabs — consumers include `shelltabs` (main tab bar) and `ui/panel_deck.go` (session deck). Provider templates use a table and focused editor, not tab pages. Application/Workspace scope is a two-option, background-selected `ui.ToggleButton` switch aligned on the same row as Save/Cancel; one full-width separator sits directly above that footer row. It is not a tab strip. The workspace panel has no duplicate mode strip; terminal instances use a right-side `ui.List` rail. |
| File picker entries | native `ui.List` |
| Session picker | native `ui.List` section headers + `ScrollbarControl` (`tui/screens/sessionpicker`) |
| Find in Files results | native `ui.VirtualList` + `ui.ScrollbarControl` |
| Launcher workspaces | VirtualList + SearchField + ScrollbarControl; shared pane viewport geometry |
| LSP popup lists | List |
| Agent question choices | native Form select/multi-select collection | List-backed form collection |

### Tab bar contract (one look)

Every actual tab bar in the app is the same control with the same look — the Status
Overview mode strip is the reference. `ui.Tabs` owns the structure; `TabsChrome`
selects only the background the strip sits on (shell `ColorBg` vs popup
`ColorPopupBg`), and no consumer may restyle it.

- **Two rows, owned by the strip.** Row 0 is labels, row 1 is the baseline rule.
  `ui.StripHeight` is the row count every host reserves when it mounts a tab strip.
- **Active tab** = accent + bold on the label row, plus an accent underline under
  that tab's exact columns on the baseline row.
- **Focus** = the whole baseline turns into the heavy accent rule (`━`). Focus is
  distinct from hover and from the active tab; hover only fills the tab
  background (`ColorHover`) and never underlines.
- **Inactive labels** are muted. Truncation is `…`; the close affordance (` ×`)
  and the attention marker (`● `) are additive and never restyle the tab. The
  marker is a prefix, so it can never collide with a label's own trailing
  characters; consumers mark a state with it instead of editing label text. The
  main bar uses it for an editor tab with unsaved changes (`window.TabModified`,
  synced per frame from the live editor by `viewmodel.syncEditorTabPresentation`).
- **Sizing** is content-sized. Short labels are never padded into fixed slots, so
  the active underline matches the label. The cell is exactly `label + close
  affordance`, marker included: `idealTabWidth` measures the composed label the
  painter draws, so a marked tab never grows padding columns between its name and
  its close glyph. A strip that does not fit shrinks
  toward `tabMinW` (8) and then scrolls to keep the active tab visible; arrows
  wrap (Left from the first tab selects the last).
- **The baseline row always starts at the strip's own left edge.** The rule is
  the row's spine and so is the active underline: both reach the row start, never
  begin inside the row with empty columns to their left. Inset the strip's
  *bounds* only when some chrome owns those columns; otherwise the row reads as
  floating stray space.
- **Only the label content may be inset.** `SetLabelInset(n)` indents the label
  content (and, with it, the truncation budget) while the tab's *cell* still
  reaches the row edge: hover fill, hit area, and the active underline start at
  the cell start, so the first tab's indicator touches the bar's start. That is
  the main tab bar's shape (`shelltabs.RailInset`, one column): `Main Agent`
  gets one column of air beside the navigation rail while the underline still
  begins on the rail. No other strip takes an inset — the session deck
  sits inside its own chrome, while the workspace panel
  and tab-statistics modal intentionally have no mode strip
  (`TestRailInsetIsMainBarOnly`).
- **Rule bleed** is only for a strip that does not span its row: the parent
  publishes the row geometry (`SetRuleFullBleed` + `SetRuleBleed`) so the
  baseline still reaches the row edge while labels stay on the content rail.
  The shell tab bar and session deck span their row and need no bleed. Settings
  scope is not a tab strip. Terminal mode has no horizontal strip; its action glyphs sit
  above the PTY and its instance list is vertical on the right.
- **Icons are not painted in tab bars.** Window tab icons live in `window.Tab`
  (disambiguation) and are deliberately not plumbed into the strip — glyph tofu
  risk. Labels are the only tab content.

## Splits

| Current | Target |
|---------|--------|
| File tree vs main view | `ui.SplitDivider` in `shelltree` / `activitylist` + `split.resize` → `Tree.Width` |
| Settings rail vs form | `ui.SplitModel` + `SplitDivider` on `WorkspaceSettingsSurface` |
| Find in Files results vs preview | vertical `ui.SplitModel` + `SplitDivider` |
| Launcher left vs workspaces | `ui.SplitDivider` replacing the paint-only `vDivider` |
| Two-child containers | `ui.SplitPane` (owns model + divider, Host capture) |

## Scrollbars

| Current | Target |
|---------|--------|
| ~~`tui/scrollbar.ScrollBar`~~ | **DELETED** — use `ui.ScrollModel` + `ui.ScrollbarControl` only |
| Main chat | `shellchat.Screen` — external `ScrollbarControl` + `ScrollModel` on `VirtualList` |
| Nested thinking/compact subpanes | Per-pane `*ui.ScrollModel`; compact rows are `chatprojection.PaintLine` → native `CellBuffer` paint in `shellchat` RowPainter (selection-capable; gutter column painted separately); thinking pane lines use the same render-time ANSI→PaintLine bridge; persistent nested gutter when overflow (no fade timers) |
| Find in Files inline glyphs | deleted |
| Launcher inline glyphs | deleted; ScrollbarControl owns the final pane column |
| File picker | native `ui.ScrollbarControl` |
| Find in Files | native `ui.ScrollbarControl` over `ui.VirtualList` |
| Shell tree / editor main | `ui.ScrollbarControl` (`shelltree`, `shelleditor`) |
| Settings left/right panels | `WorkspaceSettingsSurface` — `ScrollbarControl` on the category list and form, mounted only when `Needed()` (content overflows) |
| Find in Files editor preview | cell-buffer `Editor.Render` adapter in `findinfiles` screen (no `Editor.View()` / `scrollbar_ansi` bridge) |

## Chrome

| Current | Target |
|---------|--------|
| `widgets.PopupChromeLines` | **deleted** — product uses `ui.WindowFrame`; `ContextMenuLines`/`Reveal` deleted with it (WindowFrame reveal animation pending; state-level menu tests kept in `contextmenu_v2_test.go`) |
| `wm` decoration (deleted) | `ui.WindowFrame` |
| Hand-drawn ╭─╮ in LSP popups | same WindowFrame |

## Host pointer routing

Product screens mount under `ui.Host` instances owned by viewmodel (`chatHost`,
`launcherHost`, `sidebarHost`, `editorHost`, …). Host hit-tests pointer messages,
updates hover, and dispatches to the resolved target.

### PointerPolicyPassthrough

Components such as paint-only `ui.VirtualList` children (launcher workspaces,
find-in-files results, shellchat message rows) implement `PointerPolicyProvider`
with `PointerPolicyPassthrough`. On pointer messages, Host walks from the hit
component toward the root while policy is passthrough and dispatches to the
nearest ancestor that does **not** passthrough (typically the owning screen).
Keyboard focus and non-pointer messages are unaffected — passthrough applies
only to click / motion / release / wheel routing.

Default policy (`PointerPolicyDefault`) keeps dispatch on the hit component.
Launcher and find-in-files VirtualLists use default policy and retain direct
list click / wheel behavior.

### Pointer capture

After a **handled left** `MouseClickMsg`, Host captures the resolved dispatch
target (post-passthrough). While captured:

- `MouseMotionMsg` and **matching-button** `MouseReleaseMsg` route to the capture
  owner regardless of the current hit target or hover.
- Non-matching `MouseReleaseMsg` does **not** clear capture; it routes to the
  current hit target under normal hit-test semantics.
- Hover tracking still follows the pointer for paint feedback.
- Matching `MouseReleaseMsg` clears capture, even when the target disappears or
  returns unhandled.
- `SetRoot`, layout registry changes, and removed capture targets clear stale
  capture before the next dispatch.

Without capture, `MouseReleaseMsg` uses the current coordinates and hit-test
(not stale hover).

Viewmodel routes motion/release to any **visible active** product Host
(`chatHost`, `sidebarHost`, `launcherHost`) before popup hover and normal region
classification whenever that Host reports pointer capture, so shellchat text
selection, sidebar scrollbar drags, and launcher list clicks stay owned when the
pointer leaves the surface rect or crosses other regions (chat, input, status,
sidebar, popups). Coordinates are translated per Host (chat rect origin, sidebar
`Tree.YOffset`, launcher main-pane origin) including points outside the Host
bounds.

When a product Host becomes hidden (editor/launcher takeover, no active message
list, sidebar hidden, launcher min-size gate, host replacement), `Update` calls
`syncPersistentHostCapture()` which invokes `Host.CancelPointerCapture()`. That
clears Host capture IDs and runs optional `PointerCaptureCanceller` hooks on the
captured component (shellchat selection/autoscroll, scrollbar thumb drag) so
stale drag state cannot resurface after the surface returns. Duplicate captures
across Hosts are reduced to one winner (chat → sidebar → launcher).

Keyboard dismiss during chat text selection and `setMode` switches call the same
teardown: cancel `chatHost` pointer capture when armed, then
`shellchat.Screen.ClearSelection()` for any remaining range. In-progress
selection or scrollbar drag cancels capture without swallowing unrelated keys; a
non-empty `HasActiveSelection` range still consumes the dismissing keypress.

Precedence for motion/release (after global WM drag/resize):

1. Active product Host pointer capture (chat selection, scrollbar thumbs,
   **split dividers**, input selection, deck gutters). Capture keeps routing
   after the pointer leaves the handle — including the file-tree sidebar
   divider (`shelltree` `ui.SplitDivider`).
2. Popup hover pre-selection (focusable floating windows)
3. Normal region routing (sidebar, editor, chat, input, …)

Editor mouse is **not** routed through `editorHost`; it uses the editor package
path directly. Floating overlay content Hosts use the window-manager forward path,
not this persistent-Host elevation.

API: `HasPointerCapture()`, `CancelPointerCapture()` on each product Host;
`PointerCaptureCanceller` on components that hold drag/selection across capture.

## Interaction contract tests (Phase 15)

| Surface / policy | Test path | Notes |
|------------------|-----------|-------|
| Shell tree keyboard (expand/collapse/Home/End/filter/Esc) | `tui/screens/shelltree/tree_contract_test.go` | Host-level table; filter typing; visible row/selection/expand state |
| Tree focus + filter routing (viewmodel) | `tui/viewmodel/tree_focus_contract_test.go`, `tree_filter_contract_test.go` | sidebarHost gating, two-step Esc, Ctrl+C not back, hidden tree, open-once |
| Overlay click-outside (18 product overlays) | `tui/viewmodel/overlay_click_outside_contract_test.go` | Full policy matrix (18) ≠ real-opener integration; matrix via `handlePopupClickOutside`; one `dispatchMouseClick` path per policy group |
| Shell header | `tui/screens/shellheader/header_contract_test.go` | **Display-only** — `Handle` no-op |
| Shell status hints | `tui/screens/shellstatus/status_contract_test.go` | **Display-only** Screen; chords via ActionRegistry |
| Status hint chords (save/undo) | `tui/viewmodel/status_keyboard_contract_test.go` | `dispatchEditorKey` + `Text: "ctrl+…"` form |
| Shell tabs / input | `tui/screens/shelltabs/tabs_contract_test.go`, `shellinput/input_contract_test.go` | Prior pass |
| Welcome launcher | `tui/screens/launcher/launcher_contract_test.go`, `tui/viewmodel/launcher_keyboard_contract_test.go` | Filter `/` once, Backspace/Esc, nav bounds, PgUp/PgDn/Home/End, workspace scroll, Enter activation, Ctrl+C noop, global Ctrl+B/Ctrl+Alt+P, min-size hidden gate |
| Session PanelDeck (docked) | `tui/viewmodel/panel_deck_contract_test.go`, `tui/viewmodel/session_deck_test.go`, screen `*_test.go` | Blocking capture, settings Esc/Tab, confirmation `y`/Esc, question cancel, YOLO Esc, passive background jobs, session isolation |
| Help overlay | `tui/screens/help/help_test.go`, `tui/viewmodel/help_test.go` | Modal Ignore; live catalog; F1 toggle; Esc closes; `/help` does not dump into chat |
| Agent chat list | `tui/screens/shellchat/scroll_contract_test.go`, `host_test.go`, `tui/viewmodel/chat_keyboard_contract_test.go`, `host_capture_test.go`, `mouse_capture_test.go` | PgUp/PgDown/Home/End, wheel disarm/re-arm, scrollbar drag, selection copy once, capture teardown, input-empty routing |
| Text selection & clipboard chords | `tui/ui/text_area_clipboard_test.go`, `tui/ui/textfield_clipboard_test.go`, `tui/input/clipboard_test.go`, `tui/viewmodel/text_clipboard_menu_test.go` | Ctrl+C / Ctrl+Shift+C copy, Ctrl+X cut (selection → whole value), Esc selection-first ladder, copy-on-release, masked fields refuse the clipboard, whole-text Copy/Cut menus (composer + popup forms) |

## Async I/O boundaries

- Viewmodel clipboard helpers enqueue typed requests into one app-owned FIFO
  worker. Reads and writes therefore preserve user order (copy followed by
  paste cannot read the previous clipboard value) without blocking `Update`.
  Results carry their editor, find/replace `TextEdit`, or session owner, and
  stale generations cannot paste into a newly focused control. Clipboard-image
  file/SQLite persistence is a second `tea.Cmd`; only its owner-bound completion
  inserts the reference into the active input.
- File-picker directory reads run off the update goroutine. The picker keeps
  its last successful rows while showing `Loading…`; pointer + open-epoch +
  intent-sequence + request-generation ownership drops close/reopen ABA and
  reordered intents. Closing cancels ownership immediately, reads have a
  bounded wait, and errors restore the previous raw directory listing without
  duplicating the synthetic parent row.

Source-control snapshots and mutations also run as `tea.Cmd`, with one
30-second context deadline covering the mutation and its follow-up snapshot.
A wedged Git subprocess therefore resolves back into the normal error state
instead of leaving the panel loading indefinitely
(`TestSourceControlMutationHasDeadline`).
