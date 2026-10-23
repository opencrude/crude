# Control Usage Map

> Status: Current reference. Maps control categories to their `tui/ui` implementations.

Maps current interactive control categories to their implementations and the
target replacement under `tui/ui/`.

## Text entry

Form fields with one-line controls may highlight their label row. Multiline
`TextArea` and `KeyValueGrid` fields keep the label on the containing surface
even when focused or hovered; only their separate editor body gets the control
fill. The shared `ui.FormField` painter owns this rule for every consumer.

| Category | Current implementations | Target |
|----------|------------------------|--------|
| Full input engine | Chat via `shellinput` + `ui.TextArea` (shell `ColorBg`, not popup fill, no leading `❯`); composer + text overlay via `ui.TextArea` screens; find/replace via `ui.TextEdit` + `ui.TextField` (`BuildFindReplaceFrame`) | `ui.TextField` / `ui.TextArea` |
| Project search query | native `ui.SearchField` in `tui/screens/findinfiles` (SearchGlyph + PromptStart; Aa/ab/.* case, whole-word and regex toggles on the right) | same |
| Project replace field | native `ui.TextField` on the Find in Files sibling (Replace in Files) | same |
| Form field editor | native `ui.Form` / `FormField` / `TextField` (workspace/session/question/background); `FieldSelect` paints a bordered overlay dropdown (`PaintOverlay`) so opening it does not grow form height or push later fields; `Form.Paint` clips to the viewport so scrolled fields cannot leak into chrome; docked forms use `SetArrowExitsForm(true)` so Up/Down cycle onto the ActionBar; floating settings also exit to the category rail; Esc emits `form.cancel`; Tab/Shift+Tab still leave the form; spacious settings forms paint one row per field with a breathing row between fields, never fill a row, align every control box on one value column and show the hovered (else focused) field's help in the breathing row above the settings footer (see [Settings design language](settings-design.md)); `FieldCycle` styles are switch (a compact `‹ On ›` box; compact rows read `‹ On ›` too), segmented pill (click selects a segment) and the fixed-width `‹ value ›` stepper whose halves step back/forward and whose ends hold; unit and `Numeric` fields are short boxes with a muted unit; compact `FieldNav`/`FieldCycle` share `planCompactTable`: leftover width becomes space after the label and before the last column (never filler periods), `·` is a separator only, labels/values use `ColorFg`, inherited/meta use `ColorMuted`, focused row uses `ColorHover` + accent label. Lists/menus use the same `TableMinGap` + `MenuInk`. Trailing `TreeCollapsed` (`▸`) on nav only. Never `❯`. `FieldKeyValues` (`ui.KeyValueGrid`) edits a keyed list as a Name/value table below the label — captions from the field's two `Columns` (default `Name`/`Value`), an append row named by its `Placeholder` (default `Add header`), row removal with the shared `Close` glyph, arrows move rows/cells, `MultiLine()` keeps Up/Down inside the table, and `Form.moveFocus` blurs the open cell editor. Row states reuse `ListRowColors`/`MenuInk` (focused accent, selected, hovered). | `ui.Form` / `FormField` over shared TextEdit engine |
| Hand-built prompt | `tui/ui/textprompt.go` (product: TextEdit + WindowFrame) | `PromptField` |
| Append/backspace filter | palette, launcher, settings, session model/provider pickers | `FilterField` / `SearchField` with `SearchGlyphEnd` + `PromptEnd` (magnifier at the trailing edge) and `FilterSurface` (`ColorPopupSubtleBg`) so the input never shares the list/dock fill |
| File/workspace picker filter | native `ui.TextField` in `tui/screens/filepicker` | same |
| Document editor | `tui/editor/` PieceTable; main viewport `shelleditor` Host; gutter paints diagnostics then Git line edits (`+`/`~`); blame is status-bar + editor context menu, not a sidebar | Specialized `DocumentEditor` (kept) |

## Buttons

| Current | Target |
|---------|--------|
| Confirm / dirty / tree-delete ActionBar | `ui.ActionBar` / `ui.Button` via `BuildConfirmFrame` and friends. Floating bars right-align (`alignEnd`); `NewDockedActionBar` packs from the left with a top rule. Constrained rows first omit displayed shortcut hints, then shrink the widest buttons within the supplied width; no trailing decision may escape the parent's paint/hit bounds. Button labels elide inside their brackets; widening restores hints and shortcuts remain active throughout. Idle buttons use variant-colored `[ Label ]` on the surface; hover/focus invert — variant color becomes the fill, `cell.ContrastColor` picks a readable fg. Up/Down leave the bar (`actionbar.exit.*`) so forms and buttons cycle together; Left/Right wrap among buttons. `SetHint` puts muted guidance on the button row itself, at the end opposite the decisions; a row too narrow for both keeps every button and drops the hint whole, never a fragment. |
| Find in Files / Replace in Files | native `ui.ActionBar` (Open/Close; Replace/All on the replace row), right-aligned |
| Agent question actions | native `ui.ActionBar` in `tui/screens/question` | same |
| Tool confirmation / YOLO actions | native PanelDeck `ui.ActionBar` with typed quick keys and Escape actions | same |
| Background command termination | One retained `ui.Button` per running row, with exact command identity; compact close label only when constrained. Opens passively; explicit interaction alone retains button focus. | same |
| Chat Latest | Exact-screen native `ui.Button` centered in the existing activity gap when scrolled up; materializes the tail and re-arms follow, without extra transcript height. | same |
| Footer panel controls | `tui/screens/shellfooter` | Contiguous theme-derived strip in order: `Problems`, `Notices`, `Terminals`, `Git`, `Board`, `Memory`, `Performance`, `Workspace`. The Git pill carries no count; the changed-file count stays in the header Git identity. Board and Memory are workspace-wide; Performance is an app utility with no session slash command. All remain available without an active agent. The selected destination, actionable attention, Board, Memory and Workspace take priority when space is constrained; a reserved `More` target exposes every destination through the footer's shared projection and action path, without clipped pills. Ranked guidance and completion feedback retain up to half the row before navigation overflows. Previous sessions are searchable date-grouped rows inside Workspace. |

## Collections

| Current | Target |
|---------|--------|
| Command palette list | sectioned VirtualList |
| File tree | `ui.Tree` via `shelltree` |
| Activity lists (Workspaces / Agents / Skills / MCP) | `ui.VirtualList` via `activitylist`. Resources may supply a secondary line; workspace paths sit under their names in theme-muted ink, with no blank separators. Current-resource fill is derived from the open resource, independently of the focused browsing cursor and pointer hover. Command rows keep a theme-derived `ColorSubPaneBg` idle fill and share the sidebar focus/hover palette. The launcher's `ui.ProgressRule` sits beneath search, with loading motion on the Host clock and no loading-text row. Terminals and Git live in the workspace dock, never in the activity rail. |
| Git change list | `gitpanel` grouped list (Merge Conflicts / Staged / Changes) with section bulk actions and hover/selection inline row actions; `gitdiff.Document` for the preview and the main-area multi-file review (sticky headers, folded gaps, unified/split) |
| Message list | ✅ `chatprojection.Renderer` export; `shellchat` paints message-level `ui.VirtualList`; all rows native `PaintLine` (markdown/thinking bridged at render time via `BridgeAnsiLine`); main `ScrollbarControl` |
| Context menu | `ui/overlay.ContextMenu` / WindowFrame + List |
| Tabs | `ui.Tabs` only for actual content/mode tabs — consumers include `shelltabs` (main tab bar), `ui/panel_deck.go` (session deck), and `termdock` (compact numbered terminal instances). Provider templates use a table and focused editor, not tab pages. Application/Workspace scope is a two-option, background-selected `ui.ToggleButton` switch aligned on the same row as Save/Close; one full-width separator sits directly above that footer row. It is not a tab strip. The workspace panel has no duplicate destination strip; the footer owns dock switching. |
| File picker entries | Native `ui.Tree` with the shared file-tree row painter and `ScrollbarControl`. Startup and in-project pickers share [folder movement](interaction-matrix.md#shared-folder-picker-movement). |
| Session picker | native `ui.List` section headers + `ScrollbarControl` (`tui/screens/sessionpicker`) |
| Find in Files results | native `ui.VirtualList` + `ui.ScrollbarControl` |
| Launcher workspaces | VirtualList name/path pairs with non-interactive separator rows in the right pane; dedicated-row FilterField, ScrollbarControl and ProgressRule; one shared row stride drives paint, scrolling and context menus |
| Recent Workspaces sidebar | The same retained `activitylist` screen as Agents/Skills/MCP: `panelchrome` icon/search/count header, launcher-standard ProgressRule, name/path pairs in one variable-height resource item (Current on the attached root), + New Workspace command row with a persistent idle fill, ScrollbarControl and SplitDivider. Paths use theme-muted text and remain visible beneath names; no empty separator rows. Both lines share selection, hover and context targeting. A single resource click or Enter requests the exact-path switch confirmation; it does not attach until approved. New Workspace activates on one click. Refresh stays in the context menu; Open/Copy path/Delete use the same resource-menu builder as Home. Home and rail share one recent-access catalog and the same Cancel-default navigation confirmation. |
| LSP popup lists | List |
| Current plan hover reader | Passive WindowFrame with VirtualList and ScrollbarControl over the plan owner's complete immutable step projection; drag selection uses the shared chat line painter's selection columns and copies on release; no tool-JSON parsing or storage reads on paint | same |
| Agent question choices and review | `question.Screen` composes one wrapped `ui.VirtualList` + `ScrollbarControl`, bounded `TextArea`, optional question `List` rail and pinned `ActionBar`. Source/width changes alone derive wrapped rows; continuation clicks hit the same option. Mandatory complete-question/answer review supports Edit before explicit Submit. | same |
| Skills/MCP catalogs | store-style retained `VirtualList` cards (quiet selection fill, accent edge) with native inline Install/Manage buttons, live-searching `FilterField` over a `ProgressRule`, an `Installed` chip (`ToggleButton.SetChip`) and Source/Sort choice lists on the search row; detail `ui.Tabs`/`Form`/read-only `TextArea`, contained skill `Tree`, and bounded bottom actions; scope stays in the separate Application/Workspace footer switch |
| Session capability switches | `capabilitypicker.Screen` in modal WindowFrame; separate Skills / MCP servers views (no `Tabs`), padded body heading, local `FilterField`, one-column card list + scrollbar, one **Install & manage** `ActionBar` button. `/skills`, `/mcps` and the Session Skills / Session MCP Servers palette actions. A card's switch (click or Space; semantic `…/switch` activate/set_checked) changes only the owning session's gate; clicking elsewhere on a card, Enter or the card's semantic activate expands/collapses its full details in place (Right/Left open/close). Up/Down move between cards and back to search; Esc closes. Never starts a server or grants access. |
| Memory / Board | retained workspace-panel owners, native knowledge/episodes or threads/participants tabs, search and scrollable copyable evidence detail; tabs/title share their first row with host controls, browse search and local actions share a row, narrow rows expose local actions through a same-row More button and accessible list; no floating memory overlay or session-deck mount |

### Tab bar contract (one look)

Main-view groups each retain their own `shelltabs.Screen` + `ui.Tabs` and
independently selected tab. Topology and resource identity belong to `Window`,
not the strip. See [Main View Panes](main-view-panes.md) for the current
workspace-scoped, process-only layout and focus/ownership contract.
Main strips opt into the shared drag gesture; body edge preview is theme
`ColorHover` and commits only on release, while center/strip drops move/reorder
the same identity. Esc/outside release cancels. The native New session **+**
is content-adjacent when labels fit, pinned at the trailing edge on overflow;
the strip's parent-published baseline includes its action columns. Ctrl+T then
N/+ and Ctrl+Alt+N expose the same action without pointer input.
The action uses a bold accent + and a five-column, one-label-row native hit band;
hover and focus fill that band only. The separator is neither highlighted nor
actionable and remains owned entirely by `ui.Tabs`.

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
  The main strip reserves this treatment for deliberate keyboard navigation.
  Pointer selection/close returns focus to content; overflow browsing and hover
  do not retain strip focus. After close decisions, the first rendered frame and
  next keystroke already belong to the surviving content, not the tab bar.
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
- **Overflow browsing** uses wheel/trackpad left/right, wheel up/down over the
  strip, and visible `‹` / `›` controls. Scrolling browses without selecting a
  different tab and survives repaint and same-active projection updates. A new
  selection reveals itself; Home/End select and reveal the first/last tab.
  A supplied compact label is used before shrinking, and short labels never
  expand to the eight-column shrink floor.
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
  scope is not a tab strip. Terminal mode places compact numbered instance tabs
  beside its action glyphs on one label row, with a full-width baseline and PTY
  body. Terminal labels use suffix numbers (`Terminal 1`, `Terminal 2`); their
  title portion truncates within 16 columns while retaining the number. Cramped
  strips use `T 1`, `T 2`, … before shared overflow scrolling. The picker keeps
  the same numbers and names Docked/Floating placement instead of raw enum values.
- **Icons are not painted in tab bars.** Window tab icons live in `window.Tab`
  (disambiguation) and are deliberately not plumbed into the strip — glyph tofu
  risk. Labels are the only tab content.

## Splits

| Current | Target |
|---------|--------|
| File tree vs main view | `ui.SplitDivider` in `shelltree` / `activitylist` + `split.resize` → `Tree.Width` |
| Settings rail vs form | `ui.SplitModel` + `SplitDivider` on `WorkspaceSettingsSurface` |
| Find in Files results vs preview | vertical `ui.SplitModel` + `SplitDivider` |
| Launcher left pane vs recent workspaces | Wide: full-width New Session, then `ui.SplitModel` + `SplitDivider`; New Workspace starts the left pane and Skills/MCP split its remaining height equally after one blank row, while recent workspaces occupies the right. Cramped: actions stack with one blank row, capability pane hides, recent workspaces becomes full width. |
| Two-child containers | `ui.SplitPane` (owns model + divider, Host capture) |
| Recursive main tab groups | `window.PaneNode` topology; shared `ui.SplitModel` + mounted `ui.SplitDivider` per branch under root Host capture. Nested width/height splits preserve per-leaf selection. Too-small geometry presents only the focused group without discarding the tree; group cycling still works. |
| Git / local comparison | `screens/diffview` owns an aligned immutable projection, shared SplitModel/Divider, one vertical ScrollModel/ScrollbarControl and independent horizontal controls. Unified fallback preserves preferred split/ratio; source copy excludes alignment blanks and gutters. |

Empty main groups use retained native `ui.Button` controls for New Session and
Open File. N/O activate directly, Tab/Shift+Tab chooses and Enter/Space activates.
Ctrl+W removes only an empty group; resource closing keeps its existing safety
and agent-minimize semantics. Palette operations use the shared ActionRegistry.

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
| Popup chrome | `ui.WindowFrame` only; the old string chrome helpers are deleted (a WindowFrame reveal animation is pending; state-level menu tests live in `contextmenu_v2_test.go`) |
| `wm` decoration (deleted) | `ui.WindowFrame` |
| Hand-drawn ╭─╮ in LSP popups | same WindowFrame |

## Host pointer routing

Wheel routing is a separate, uncaptured gesture. `ui.WheelGesture` retains an
exact mounted scroll owner through a continuous burst (500 ms idle boundary),
with two columns or one row of deliberate movement from its origin starting a
new target. A sibling surface/window boundary immediately chooses that exact
mounted surface; keyboard focus never changes. A direct scrollbar can name a
mounted ancestor via `WheelTargetProvider`, so the owning screen arbitrates its
wheel without replacing the scrollbar's click/drag/capture math. Source/mount
replacement, resize, click, key, blur, modal and attachment boundaries end stale
ownership; ordinary content/export revisions do not.

Chat carries exact outermost-to-leaf pane ancestry with each rendered row. An
ongoing conversation scroll is not stolen by a pane moving under a stationary
pointer. A fresh gesture or deliberate movement into pane content scrolls it;
its gutter stays directly draggable. At a boundary only unconsumed signed rows
chain to logical ancestors, never siblings or windows, and subsequent events
stay with the ancestor until new intent. Direction reversal resets acceleration,
not ownership. Acceleration is computed once per physical wheel event; no
near-tail snap may consume extra rows. Headers/spacers target their enclosing
content rather than silently scrolling the pane whose title they paint.

Persistent shell screens, including recursive main groups, mount under the
viewmodel's root `ui.Host`; retained session decks and floating bodies use their
own adapted Hosts. Host hit-tests pointer messages,
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

Viewmodel routes captured motion/release through the root Host before normal
region routing, so shellchat selection, tab drags, main/sidebar divider drags
and scrollbar manipulation remain owned outside their original bounds. Main
groups use parent-local child bounds; the Host derives local pointer coordinates
from screen geometry, including captured points outside the owning component.

When a component unmounts, Host layout validates capture and cancels stale
ownership. `Host.CancelPointerCapture()` runs optional
`PointerCaptureCanceller` hooks (selection/autoscroll, scrollbar/tab/divider
drag), so stale gestures cannot resurface after a component returns.

Keyboard dismiss during chat text selection and `setMode` switches call the same
teardown: cancel root Host pointer capture when armed, then
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

Editor pointer ownership enters through its mounted `shelleditor.Screen`, which
adapts the editor engine and shared scrollbars. Floating overlay content Hosts
use the window-manager forward path,
not this persistent-Host elevation.

API: `HasPointerCapture()`, `CancelPointerCapture()` on each product Host;
`PointerCaptureCanceller` on components that hold drag/selection across capture.

## Interaction contract tests (Phase 15)

| Surface / policy | Test path | Notes |
|------------------|-----------|-------|
| Shell tree keyboard (expand/collapse/Home/End/filter/Esc) | `tui/screens/shelltree/tree_contract_test.go` | Host-level table; filter typing; visible row/selection/expand state |
| Tree focus + filter routing (viewmodel) | `tui/viewmodel/tree_focus_contract_test.go`, `tree_filter_contract_test.go` | sidebarHost gating, two-step Esc, Ctrl+C not back, hidden tree, open-once |
| Overlay click-outside (20 static product overlays) | `tui/viewmodel/overlay_click_outside_contract_test.go` | Full declared policy matrix plus real-opener integration by policy group; includes the modal capability picker. Dynamic terminal owners use separate IDs. |
| Shell header | `tui/screens/shellheader/header_contract_test.go` | **Display-only** — `Handle` no-op |
| Shell status hints | `tui/screens/shellfooter/footer_test.go`, `tui/viewmodel/status_keyboard_contract_test.go` | **Display-only** Screen; chords via ActionRegistry |
| Status hint chords (save/undo) | `tui/viewmodel/status_keyboard_contract_test.go` | `dispatchEditorKey` + `Text: "ctrl+…"` form |
| Shell tabs / input | `tui/screens/shelltabs/tabs_contract_test.go`, `shellinput/input_contract_test.go` | Prior pass |
| Welcome launcher | `tui/screens/launcher/composition_test.go`, `tui/screens/launcher/interaction_test.go`, `tui/screens/launcher/capabilities_test.go`, `tui/viewmodel/launcher_lifecycle_test.go` | Wide full-width New Session with New Workspace back in the left pane above an equal-height Skills/MCP split; cramped stacked actions with hidden capabilities and full-width Recent workspaces; bottom-pinned guide, dedicated search, quiet separators, filter/paste/Esc, independent scrolling, owner-safe async loading/error/retry, named live capabilities, context delete/undo |
| Session PanelDeck (docked) | `tui/viewmodel/panel_deck_contract_test.go`, `tui/viewmodel/session_deck_test.go`, screen `*_test.go` | Blocking capture, settings Esc/Tab, confirmation `y`/Esc, question cancel, YOLO Esc, passive background jobs, session isolation |
| Agent chat list | `tui/screens/shellchat/scroll_contract_test.go`, `host_test.go`, `tui/viewmodel/chat_keyboard_contract_test.go`, `host_capture_test.go`, `mouse_capture_test.go` | PgUp/PgDown/Home/End, wheel disarm/re-arm, scrollbar drag, selection copy once, capture teardown, input-empty routing |
| Text selection & clipboard chords | `tui/ui/text_area_clipboard_test.go`, `tui/ui/textfield_clipboard_test.go`, `tui/input/clipboard_test.go`, `tui/viewmodel/text_clipboard_menu_test.go` | Ctrl+C / Ctrl+Shift+C copy, Ctrl+X cut (selection → whole value), Esc selection-first ladder, editable drags keep their selection while read-only drags copy on release, masked fields refuse the clipboard, whole-text Copy/Cut menus (composer + popup forms) |

## Text selection visibility

Text selection uses `ColorTextSelection` / `ColorTextSelectionFg`, not the quiet
list/chip `ColorSelection`. The theme owns both colors. Shared text controls,
chat, document editors, comparisons and embedded terminals use visible selected
fills; syntax ink is retained only when readable. Runtime theme changes preserve
selection state. Search/navigation cues use contrast-checked ink, never reverse
video over coincident syntax and highlight colors.
The default dark theme uses a neutral gray text-selection fill with readable
light ink; colored preset identities and semantic search/diff accents remain
separate from drag-selection coloring.

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
(`TestSourceControlMutationHasDeadline`, `git_panel_mutation_test.go`).

## Compact chrome language

- Prefer established, theme-aware symbols over spelled-out words in constrained
  chrome when the symbol is meaningful and consistent elsewhere in Crude. Keep
  text when a symbol would be ambiguous; never introduce one-off icon dialects.
- **One tab-bar look**: every tab bar is the shared `ui.Tabs` strip (labels row
  + its own baseline rule, accent bold + accent underline on the active tab,
  heavy accent baseline while the strip is focused). Never hand-paint a tab row,
  never add a bespoke segmented switch, and never restyle per consumer —
  `TabsChrome` only picks the background, `SetLabelInset` only indents label
  content. `ui.StripHeight` rows are reserved by every host, and the baseline row
  — rule and active underline — always starts at the strip's own left edge
  (empty columns to its left read as a broken bar). Contract:
  `docs/reference/control-usage.md` (Tab bar contract).
- Workspace docks share their first content row with the host's More/Maximize/Hide
  controls: searchable status panels reserve trailing search space, while Memory
  and Board reserve it beside their tabs or detail title. Browse search and local
  actions share a row; constrained widths replace local buttons with More and
  an accessible action list rather than splitting ordinary dock chrome.
  Terminal instances use compact numbered `ui.Tabs` beside the terminal controls,
  with one full-width baseline and a full-width PTY body, never a side instance rail.
  Numbers follow titles (`Terminal 1`); cramped strips use `T 1`. Shared Tabs
  overflow is independently browsable by wheel/trackpad and `‹`/`›`, preserving
  manual position across repaint/same-active projections. Selection alone reveals
  the active tab. Picker numbers match dock tabs; placement is Docked/Floating,
  never a raw enum.

## Hover

- **`ui.ColorHover`** (theme `Hover`) is the one hover background token; never
  hard-code a hover color in a component.
- **Priority: focused (keyboard) > selected > hovered > nothing.**
- **Activity rail entries are whole bands.** `activitybar.RowStride` is odd
  so the glyph sits on the band's exact middle row (`iconRow`), and every
  highlight — hover background and the `▌` accent marker — covers the full
  band. A one-row marker next to a multi-row hover box reads as
  misalignment. `SetPanelOpen(false)` drops the marker when the sidebar is
  collapsed so the rail shows its toggle state. The rail keeps one blank
  column before its panel rule so file-tree / Agents / Terminals / Git
  left borders are not flush against the icons.

## Scrollbar direct manipulation

- `ui.ScrollbarControl` is the only click/drag/wheel interaction math. Nested
  chat panes and terminal scrollback adapt their models to it; never duplicate
  thumb mapping or track-click formulas.
- Keep the painted stroke one cell. `PointerHitBounds` expands inward toward the
  content, capped by the configured target, and explicit hit bounds outrank
  deeper content descendants in `Host.HitTest`. Adjacent scrollbars must cap
  expansion so their targets never overlap. Settings scrollbars set a 1-column
  target (`SetPointerTargetSize(1)`) and reserve a blank gutter column
  (`ui.ScrollbarWidth+1`), so a click aimed at the field, filter, or list row
  beside the bar cannot land on the scrollbar.
- A changed offset invalidates layout AND paint: retained forms encode scroll
  in child bounds while lists consume it during paint. Offset-only tests are
  insufficient; assert that painted content moves.
- Exact Host hits do not bubble pointer events through the parent. Consumers
  with an authoritative mirror (notably file tree and terminal scrollback) use
  `ScrollbarControl.OnScroll`; never rely on a parent fallback handler to copy
  the offset.
- Mounted scrollbars route through Host capture. Manual composite routing uses
  `ui.ChildEventContext`, never unchanged parent-local coordinates. Editor
  pointer input enters through `shelleditor.Screen` so drags release outside.
- Decide visibility with `NeededForViewport(candidateViewport)`, not stale
  previous-frame viewport state.

## Sidebar list rows: resource vs command
`ui.ListItem.Command` marks a button-like row (e.g. "New Terminal").
`activitylist` arms resource rows on the first click and activates on the
second for picker-style collections. Workspace navigation uses the shared
single-click activation policy to request its existing confirmation. Command
rows always fire on a single click — a
button that needs a double click reads as broken.
Command rows retain `ColorSubPaneBg` when focus or hover leaves; focus/hover
feedback never provides their only visible background. This is control chrome,
not the current-resource selection fill.

The same `ui.ProgressRule` used by the launcher and settings sits directly under
sidebar search. Its row stays reserved so catalog refresh never moves the item
hit targets. Loading changes only the rule's ink, not labels or row count; the
catalog owner stops animation on completion and only visible loading views use
the fast frame clock.

`ui.ListItem.Subtitle` retains an optional secondary resource line. The shared
VirtualList owns its two-row height, scrolling and hit lookup; names and paths
are never separate selectable entries. Subtitles use `ColorMuted` from the live
theme and middle truncation at the pane width, without extra separator rows.

## Workspace and Open Tabs rows

- Workspace and Open Tabs collapsed resource rows occupy exactly one line.
  Keep explicit Workspace section headings even for a single group. Click,
  Enter/Space, and Left/Right own disclosure; Open/Resume is an explicit action
  inside expanded details, not an ambiguous first click.
