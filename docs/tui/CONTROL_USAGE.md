# Control Usage Map

Maps current interactive control categories to their implementations and the
target replacement under `tui/ui/`.

## Text entry

| Category | Current implementations | Target |
|----------|------------------------|--------|
| Full input engine | Chat via `shellinput` + `ui.TextArea`; composer + text overlay via `ui.TextArea` screens; find/replace via `ui.TextEdit` + `ui.TextField` (`BuildFindReplaceFrame`) | `ui.TextField` / `ui.TextArea` |
| Project search query | native `ui.SearchField` in `tui/screens/findinfiles` | same |
| Form field editor | native `ui.Form` / `FormField` / `TextField` (workspace/session/question/background); legacy `tui/form` **deleted** | `ui/form` field over shared engine |
| Hand-built prompt | `tui/widgets/textprompt.go` (product: TextEdit + WindowFrame) | `PromptField` |
| Append/backspace filter | palette, launcher, settings | `FilterField` / `SearchField` |
| File/workspace picker filter | native `ui.TextField` in `tui/screens/filepicker` | same |
| Document editor | `tui/editor/` PieceTable; main viewport `shelleditor` Host | Specialized `DocumentEditor` (kept) |

## Buttons

| Current | Target |
|---------|--------|
| Confirm / dirty / tree-delete ActionBar | `ui.ActionBar` / `ui.Button` via `BuildConfirmFrame` and friends |
| `PopupButton` / `PopupButtonRow` | same Button + ActionBar |
| Agent question actions | native `ui.ActionBar` in `tui/screens/question` | same |
| Tool confirmation / YOLO actions | native PanelDeck `ui.ActionBar` with typed quick keys and Escape actions | same |

## Collections

| Current | Target |
|---------|--------|
| Command palette list | sectioned VirtualList |
| File tree | `ui.Tree` via `shelltree` |
| Message list | **PR6 + Phase 13.7/14 DONE:** `chatprojection.Renderer` export; `shellchat` paints message-level `ui.VirtualList` via `ComposeRow` (geometry only) + native nested/main gutter paint; per-pane `ScrollModel`; main `ScrollbarControl` drag + mouse text selection on `shellchat.Screen` |
| Context menu | `ui/overlay.ContextMenu` / WindowFrame + List |
| Tabs | `ui.Tabs` via `shelltabs` |
| File picker entries | native `ui.List` |
| Session picker | native `ui.List` section headers + `ScrollbarControl` (`tui/screens/sessionpicker`) |
| Find in Files results | native `ui.VirtualList` + `ui.ScrollbarControl` |
| Launcher workspaces | VirtualList + SearchField + ScrollbarControl; shared pane viewport geometry |
| LSP popup lists | List |
| Agent question choices | native Form select/multi-select collection | List-backed form collection |

## Scrollbars

| Current | Target |
|---------|--------|
| ~~`tui/scrollbar.ScrollBar`~~ | **DELETED** — use `ui.ScrollModel` + `ui.ScrollbarControl` only |
| Main chat | `shellchat.Screen` — external `ScrollbarControl` + `ScrollModel` on `VirtualList` |
| Nested thinking/compact subpanes | Per-pane `*ui.ScrollModel`; compact rows are `chatprojection.PaintLine` → native `CellBuffer` paint in `shellchat` RowPainter (selection-capable; gutter column painted separately); thinking pane MessageList lines may still use ANSI fallback; persistent nested gutter when overflow (no fade timers) |
| Find in Files inline glyphs | deleted |
| Launcher inline glyphs | deleted; ScrollbarControl owns the final pane column |
| File picker | native `ui.ScrollbarControl` |
| Find in Files | native `ui.ScrollbarControl` over `ui.VirtualList` |
| Shell tree / editor main | `ui.ScrollbarControl` (`shelltree`, `shelleditor`) |
| Find in Files editor preview | cell-buffer `Editor.Render` adapter in `findinfiles` screen (no `Editor.View()` / `scrollbar_ansi` bridge) |

## Chrome

| Current | Target |
|---------|--------|
| `widgets.PopupChromeLines` | **deleted** — product uses `ui.WindowFrame`; ContextMenuLines/Reveal retained for reveal-animation tests only |
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

Precedence for motion/release (after global WM drag/resize and tree-divider
resize owners):

1. Tree divider resize (`Tree.Resizing`)
2. Active product Host pointer capture (chat → sidebar → launcher when multiple
   report capture — lower-priority and inactive Host captures are canceled)
3. Popup hover pre-selection (focusable floating windows)
4. Normal region routing (sidebar, editor, chat, input, …)

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
| Overlay click-outside (17 product overlays) | `tui/viewmodel/overlay_click_outside_contract_test.go` | Full policy matrix (17) ≠ real-opener integration; matrix via `handlePopupClickOutside`; one `dispatchMouseClick` path per policy group |
| Shell header | `tui/screens/shellheader/header_contract_test.go` | **Display-only** — `Handle` no-op |
| Shell status hints | `tui/screens/shellstatus/status_contract_test.go` | **Display-only** Screen; chords via ActionRegistry |
| Status hint chords (save/undo) | `tui/viewmodel/status_keyboard_contract_test.go` | `dispatchEditorKey` + `Text: "ctrl+…"` form |
| Shell tabs / input | `tui/screens/shelltabs/tabs_contract_test.go`, `shellinput/input_contract_test.go` | Prior pass |
| Welcome launcher | `tui/screens/launcher/launcher_contract_test.go`, `tui/viewmodel/launcher_keyboard_contract_test.go` | Filter `/` once, Backspace/Esc, nav bounds, PgUp/PgDn/Home/End, workspace scroll, Enter activation, Ctrl+C noop, global Ctrl+B/Ctrl+Alt+P, min-size hidden gate |
| Session PanelDeck (docked) | `tui/viewmodel/panel_deck_contract_test.go`, `tui/viewmodel/session_deck_test.go`, screen `*_test.go` | Blocking capture, settings Esc/Tab, confirmation `y`/Esc, question cancel, YOLO Esc, passive background jobs, session isolation |
| Agent chat list | `tui/screens/shellchat/scroll_contract_test.go`, `host_test.go`, `tui/viewmodel/chat_keyboard_contract_test.go`, `host_capture_test.go`, `mouse_capture_test.go` | PgUp/PgDown/Home/End, wheel disarm/re-arm, scrollbar drag, selection copy once, capture teardown, input-empty routing |
