# Crude Architecture & Working Knowledge

## Architecture Vision: `workspace/` vs `viewmodel/`

The codebase has a strict two-layer separation:

### `workspace/` — DATA LAYER ("what IS")
Pure data structures, managers, and state machines. No Bubble Tea model methods (no Update/View/Init). No rendering logic.

```
Belongs here:
├── Manager files   → agent_manager, pane_manager, buffer_manager, lsp_manager
│                     (owns things that exist: agents, panes, buffers, LSP)
├── Data structs    → session, agent_state, editor_tab, subpane, focus_stack
│                     (what the data looks like — fields, not display)
├── Stores          → references.go (ReferenceStore: persists to SQLite)
├── Infrastructure  → foundation.go (EventBus, Logger, enums: Mode, FocusTarget)
└── workspace.go    → Workspace struct (composition root: holds all managers)
```

**Litmus test:** "Would I put this in a database?" → `workspace/`. A session, an agent, a buffer, an LSP connection, a reference store — these have identity and lifetime independent of display.

### `viewmodel/` — CONTROLLER LAYER ("what's VISIBLE and what HAPPENS")
Display state, Bubble Tea event handling, and the render pipeline.

```
Belongs here:
├── app.go          → App struct (embeds workspace, owns compositor/WM/surfaces)
├── update.go       → tea.Model.Update()
├── view.go         → tea.Model.View() + render pipeline + surface renders
├── keyboard.go     → ALL key dispatch
├── mouse.go        → ALL mouse dispatch + popup mouse
├── agent_events.go → agent event handlers
├── *state.go       → editor_state, overlay_state, lsp_popup_state
│                     (which tab is active? what mode? where's the cursor?)
├── *_tabs.go       → agent_tabs, editor_tabs (tab lifecycle)
├── *_handlers.go   → findreplace_handler, lsp_handlers, dirty_prompt
├── *_builders.go   → context_menu_builders
├── *_operations.go → tree_operations, clipboard
├── layout.go, cancel.go, launcher.go, command_palette.go, slash_commands.go
└── focus.go        → focusTarget() + unfocus() + focus helpers
```

**Litmus test:** "Is this about what's visible right now or what the user does?" → `viewmodel/`. Which tab is active, which mode is shown, what's hovered, popup cursor position, keyboard/mouse event routing.

### File naming convention:
| Pattern | Examples |
|---------|----------|
| `*_manager.go` | `agent_manager.go`, `buffer_manager.go`, `lsp_manager.go` |
| `*_state.go` | `editor_state.go`, `overlay_state.go`, `lsp_popup_state.go` |
| `*.go` (data) | `session.go`, `agent_state.go`, `subpane.go` |
| `*.go` (handlers) | `keyboard.go`, `mouse.go`, `agent_events.go` |
| `*_tabs.go` | `agent_tabs.go`, `editor_tabs.go` |
| `*_handlers.go` | `findreplace_handler.go`, `lsp_handlers.go`, `dirty_prompt.go` |
| `*_builders.go` | `context_menu_builders.go` |
| `*_operations.go` | `tree_operations.go`, `clipboard.go` |

### What goes where — clear rules:

| Lives in `workspace/` | Lives in `viewmodel/` |
|---|---|
| What EXISTS (managers, data, state machines) | What's VISIBLE (active tab, mode, popups) |
| AgentManager, BufferManager, PaneManager, LSPManager | EditorState, OverlayState, LSPPopupState |
| AgentNode, AgentState, Session | TreeState display fields (Visible, Width) |
| ReferenceStore, FocusStack | LauncherState, command palette |
| Enums (Mode, FocusTarget, PaneType) | Event handlers (keyboard, mouse) |
| EventBus, Logger | Render pipeline (view.go, update.go) |

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

### Main Content Cache Rule (CRITICAL)

The `m.contentDirty` flag gates the `buildMainView()` call in `view.go`. **The cache is ONLY used when the editor is the active view** — chroma syntax highlighting is expensive:

```go
isEditorMain := m.EditorState.ActiveView != "" && m.EditorState.Editor.IsOpen()
useCached := isEditorMain && !m.contentDirty && m.mainContentCached != "" && m.mainContentSurf != nil
```

Agent tabs and the launcher ALWAYS call `buildMainView()` fresh every frame. They use cheap ANSI rendering and need every frame for cursor blink, hover highlights, and streaming tokens.

**RULES:**
- `m.contentDirty = true` must be set ONLY in paths that modify editor content or cursor position. The only valid places are:
  - `update.go` Update-level: WindowSize, KeyPress, PasteMsg, MouseClick, MouseWheel, cursor-blink tick
  - `mouse.go`: editor mouse motion (MouseMotion path)
  - `clipboard.go`: cut/paste into editor
  - `editor_tabs.go`: diagnostic cursor navigation
- Do NOT set `m.contentDirty` for agent events, launcher operations, tree operations, floating windows, context menus, or any other non-editor path — the cache is never read there.
- The old `m.dirty bool` field was dead code (set 60+ times, never read) and was removed. Do not reintroduce it.
- `m.contentDirty` is reset to `false` at the end of `View()`. Any `contentDirty = true` set during Update() causes a single full re-render of the main view.

---

## Key File Organization

### `tui/viewmodel/` (21 files)
| File | Role | Methods |
|---|---|---|
| `app.go` | App struct + NewApp + thin callbacks | 13 |
| `update.go` | tea.Model.Update + message dispatch | 23 |
| `view.go` | tea.Model.View + render pipeline + surface renders | 8+4 |
| `keyboard.go` | ALL key dispatch + LSP commands | 34 |
| `mouse.go` | ALL mouse dispatch + popup mouse | 18 |
| `agent_events.go` | Agent event handlers | 13 |
| `agent_tabs.go` | Agent lifecycle, tab sync, setMode | 23 |
| `editor_tabs.go` | Editor tab open/close/switch | 15 |
| `findreplace_handler.go` | Find/replace popup | 5 |
| `context_menu_builders.go` | 6 context menu builders | 10 |
| `lsp_handlers.go` | LSP events + navigation | 7 |
| `tree_operations.go` | Tree rename/delete/create prompts | 13 |
| `clipboard.go` | Copy/cut/paste helpers | 5 |
| `dirty_prompt.go` | Dirty-close save/discard dialog | 8 |
| + editor_state.go, overlay_state.go, lsp_popup_state.go, launcher.go, command_palette.go, focus.go, layout.go, cancel.go, popupmouse.go, slash_commands.go |

### `tui/workspace/` (14 files)
| File | Role |
|---|---|
| `workspace.go` | Workspace struct (composition root: managers + callbacks + references) |
| `foundation.go` | Mode/FocusTarget/PaneType enums, Logger, EventBus |
| `agent_manager.go` | AgentNode/AgentManager (tree + flat index) |
| `session.go` | Session struct (dialogs, refs, token metrics) |
| `agent_state.go` | AgentState streaming/thinking state + HandleEvent |
| `agent_state_events.go` | Tool handling sub-methods on AgentState |
| `editor_tab.go` | EditorTab wrapper (path + editor + dirty flag) |
| `focus_stack.go` | FocusStack LIFO |
| `lsp_manager.go` | LSPManager (connections, diagnostics cache) |
| `lsp_popup_state.go` | LSPPopupState (refs/outline/completion/rename state) |
| `overlay_state.go` | OverlayState (mode + blocking + yolo flags) |
| `pane_manager.go` | SubPane + PaneManager |
| `references.go` | ReferenceStore (SQLite-backed @references) |
| `tree_state.go` | TreeState (file tree model + renderer + layout) |

### `tui/editor/` (core editor engine)
| File | Role |
|---|---|
| `editor.go` | Editor struct (cursor, scroll, undo, selection, buffer view) |
| `buffer.go` | PieceTable (text data) |
| `buffer_manager.go` | **NEW** Buffer + BufferManager (shared buffers across tabs) |
| `events.go`, `render.go`, `search.go`, `movement.go`, `edit.go`, `selection.go` | Editor subsystems |

---

## Progress & Next Steps

| What | Status | Notes |
|---|---|---|
| EventBus (Phase 1) | ✅ DONE | Ring-buffer event log for LSP + file watcher |
| AgentManager (Phase 2) | ✅ DONE | Collapsed 9 parallel maps into tree |
| FocusStack (Phase 6) | ✅ DONE | Replaced booleans with LIFO stack |
| Workspace extraction (Phase 7) | ✅ DONE | App shrunk from 150→27 fields. All state in Workspace |
| **Architecture cleanup** | ✅ DONE | Moved display state to viewmodel, deleted 11 alias files, extracted app.go 82→13 methods |
| **BufferManager (Phase 3)** | ⏳ **INFRA DONE** | Buffer + BufferManager created, Editor.OpenBuffer added, shared buffers across tabs. **Full Editor migration (delegate all text access to Buffer) still pending** |
| God Settings (Phase 4) | ❌ | Floating settings panel (Ctrl+,) |
| Clean Orchestrator Settings (Phase 5) | ❌ | Strip app-level stuff from overlay settings |
| Dialog Stack (Phase 2.2–2.8) | ❌ | Per-agent dialog popup system |
| More hover widgets (Phase 1.4–1.9) | ❌ | File picker, launcher, message list, status bar, form items, input |

### Are we ready for Phase 3? **Yes and no.**

**Infrastructure is done:** `BufferManager`, `Buffer`, `Editor.OpenBuffer()`, sharing buffers across tabs. The editor tabs now use BufferManager to get/create shared buffers.

**Full migration is NOT done:** The `Editor` struct still has legacy `buf *PieceTable` and `filePath string` fields. Many Editor methods (Value, LineCount, LineText, Save, Modified) still access the local PieceTable instead of delegating to `e.buffer`. The full delegation is the remaining work.

Before starting Phase 3 completion, consider whether the current shared-buffer behavior is sufficient for your immediate needs. The Editor works correctly with shared buffers via `OpenBuffer()` — each tab gets the same `Buffer` from BufferManager. The remaining work is purely internal cleanup (removing the legacy direct PieceTable access).

### What's next (in priority order):

1. **Complete BufferManager migration** — make Editor delegate ALL text access to `e.buffer`, remove legacy `e.buf`/`e.filePath`/`e.modified` fields
2. **Dialog Stack** — per-agent dialog popup system (user's highest-priority feature request)
3. **God Settings** — floating all-mighty settings panel
4. **Clean Orchestrator Settings** — strip app-level stuff from overlay
5. **More hover widgets** — file picker, launcher, message list

---

## Historical Debugging Journal (Scrollbar Artifact Bug)

*Kept for reference — the root cause and fix remain relevant for future width-sensitive rendering issues.*

### Problem
Stale characters at the right edge (scrollbar column) of the command palette when scrolling.

### Root Cause
`VisibleWidth` used `uniseg.StringWidth` (width=1 for ☰ U+2630) but ultraviolet's parser uses `ansi.StringWidth` (width=2). This 1-column mismatch made ultraviolet's diff see "no change" at the old scrollbar position.

### Fix
`VisibleWidth` → `ansi.StringWidth(StripAnsi(s))` — matching ultraviolet exactly.

### Deleted: `terminal_probe.go`, `isWideCJK()`, `probedWide()`, `makeNoColorBgTransparent()`

---

## Unified Popup Design System

All popups use `tui/widgets/popupchrome.go`:
```
╭─ Title ────────────────────────────────╮
│  body content line 1                   │
│  body content line 2                   │
╰────────────────────────────────────────╯
```
- `PopupContentWidth(innerW)` → total: `innerW + 6`
- `PopupContentHeight(contentRows)` → total: `contentRows + 2`
- FloatingWindow is always `WithDecoration(false)` — just a positioned box
- Chrome is self-drawn by the widget

### Popup Click-Outside Behaviors
| Behavior | Meaning |
|----------|---------|
| `CloseRoute` | Close + route click through |
| `KeepRoute` | Keep open, route click to editor |
| `Ignore` | True modal — suppress click entirely |

---

## Pre-existing Test Failures (NOT caused by our work)
- `crude/safety/TestProjectWriteCommands`, `TestDestructiveCommands`, `TestUnknownCommand`

## Test Commands
```bash
go build ./...
go test ./tui/editor/... ./tui/viewmodel/... ./tui/workspace/...
go test ./... 2>&1 | grep -v "^ok\|cached\|^?"
```

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


