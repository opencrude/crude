# Crude Architecture & Working Knowledge

## Target Architecture: Three-Layer IDE

```
┌──┐
│  app/  — IDE Shell, workspace manager               │
│  Owns: app config (theme, font, UI prefs),          │
│        workspace list/create/resume/delete, CLI      │
│  ~/.crude/app.db, ~/.crude/config.json               │
│  NO session/agent/orchestrator knowledge             │
└─┬┘
                 │ creates, owns, persists
                 ▼
┌──┐
│  workspace/  — Per-project state machine             │
│  Owns: orchestrators, sessions, LSP connections,     │
│        editor buffers, file tree, TUI layout prefs,  │
│        workspace settings (inherits app config)       │
│  ~/.crude/workspaces/<id>/workspace.db               │
│  NO rendering or input handling logic                │
└─┬┘
                 │ method calls ← → event bus
                 ▼
┌──┐
│  tui/  — Dumb renderer + input router                │
│  Does: render surfaces, route key/mouse/paste,       │
│        window compositing, widget rendering          │
│  Does NOT: own business state, persist anything,     │
│            manage orchestrators, resolve settings    │
│  Litmus test: swap Bubble Tea for web UI →           │
│  workspace/ and app/ stay unchanged                  │
└──┘
```

### Communication Rules

No callbacks between layers. Two mechanisms only:

| Direction | Mechanism | Example |
|-----------|-----------|---------|
| TUI → workspace | Method calls | `ws.OpenFile(path)`, `ws.SendChat(text)` |
| Workspace → TUI | Event bus | `bus.Publish(FileOpened{path})` → TUI re-renders |
| Workspace → entities | Event bus | `bus.Publish(SettingsSaved{...})` → LSP reconnects |

The `app/` layer does NOT wire TUI → workspace. The TUI holds a
reference to `*workspace.Workspace` and calls methods directly. The
workspace publishes events that the TUI subscribes to.

### Current State (what's in the code vs what we're building)

The codebase is mid-transition. The current layout is a hybrid:

| Exists today | → Target |
|---|---|
| `tui/viewmodel/App` — 60+ fields god object | → Split: business state moves to `workspace/`, rendering stays in `tui/` |
| `app/app.go` — 446 lines, 15+ callbacks, session+app config mixed | → Thin CLI entry point (~50 lines), no callbacks |
| `tui/tui.go` — callback holder (OnSettingsSave, OnQuit, OnNewSession...) | → Removed: TUI takes `*workspace.Workspace` directly |
| `tui/types.Settings` — mixed app+session config | → Split: `AppConfig` + `SessionSettings` |
| `tui/workspace/` — managers + event bus | → Expanded: owns ALL business state |
| `session/` — standalone package | → Absorbed into `workspace/` |
| `app/config.go` — FileConfig mixing app+session | → App config only |

See `ARCHITECTURE_TRANSITION.md` for the phase plan.

---

## Current Code Layout (for working in the codebase TODAY)

### `tui/viewmodel/` — Bubble Tea model (being slimmed down)
| File | Role |
|---|---|
| `app.go` | App struct + NewApp + thin callbacks |
| `update.go` | tea.Model.Update + message dispatch |
| `view.go` | tea.Model.View + render pipeline + surface renders |
| `keyboard.go` | ALL key dispatch + LSP commands |
| `mouse.go` | ALL mouse dispatch + popup mouse |
| `agent_events.go` | Agent event handlers |
| `agent_tabs.go` | Agent lifecycle, tab sync, setMode |
| `editor_tabs.go` | Editor tab open/close/switch |
| `findreplace_handler.go` | Find/replace popup |
| `context_menu_builders.go` | Context menu builders |
| `lsp_handlers.go` | LSP events + navigation |
| `tree_operations.go` | Tree rename/delete/create prompts |
| `clipboard.go` | Copy/cut/paste helpers |
| `dirty_prompt.go` | Dirty-close save/discard dialog |
| `editor_state.go` | Active editor tab tracking |
| `overlay_state.go` | Mode + blocking + yolo flags |
| `lsp_popup_state.go` | LSP popup state (outline/references/completion) |
| `launcher.go` | Session launcher |
| `command_palette.go` | Command palette |
| `focus.go` | Focus target helpers |
| `layout.go` | Layout calculations |
| `cancel.go` | Cancel handler |
| `popupmouse.go` | Popup mouse dispatch |
| `slash_commands.go` | Slash command parsing |

### `tui/workspace/` — State managers (being expanded)
| File | Role |
|---|---|
| `workspace.go` | Workspace struct (composition root: managers + references) |
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

### `tui/editor/` — Core editor engine
| File | Role |
|---|---|
| `editor.go` | Editor struct (cursor, scroll, undo, selection, buffer view) |
| `buffer.go` | PieceTable (text data) |
| `buffer_manager.go` | Buffer + BufferManager (shared buffers across tabs) |
| `events.go` | Editor event handling |
| `render.go` | Editor rendering (syntax highlighting, diagnostics, search) |
| `search.go` | SearchState, SearchMatch, find/replace operations |
| `movement.go` | Cursor movement |
| `edit.go` | Edit operations |
| `selection.go` | Selection state |

### `app/` — CLI entry point + config (being slimmed down)
| File | Role |
|---|---|
| `app.go` | App struct, Run(), wireTUI() — TOO BIG, being split |
| `config.go` | FileConfig, LoadFileConfig, SaveFileConfig — being split |

---

## Rendering Architecture (stable — no planned changes)

### Surface Tree (Compositing Engine)

The compositor uses a tree of `Surface` nodes. Each `Surface` has a
`CellBuffer` (flat `[]cell.Cell` array) and can have child surfaces
with z-ordering.

**Composition pipeline:**
1. `BeginFrame()` — clears root children + back buffer
2. Background renders into root surface
3. Window manager renders floating windows (each window has its own
   surface with a child content surface)
4. `Composite()` — flattens surface tree via `FlattenZOrder()`, blits
   all surfaces bottom-to-top into the back buffer using `surface.Blit()`
5. Blit compares old vs new back buffer, sends ANSI diff to terminal

### Floating Window Rendering

Each `FloatingWindow` has:
- A main surface (`fw.surf`) sized `w x h`; WM owns placement and z-order only
- A full-size child content surface (`fw.contentSurf`)
- Frame interaction geometry for focus, close, title drag, and resize hit testing
- `ui.WindowFrame` content for visual background, border, title, and close chrome

**Render flow:**
1. `fw.surf.Buffer().Clear()` — transparent blanks
2. `fw.contentSurf.Buffer().Clear()` — transparent blanks
3. `fw.Content.Render(fw.contentSurf, ctx)` — normally ComponentWidget paints WindowFrame + body
4. Content surface added as child of window surface
5. Compositor blits window surface then content surface onto back buffer

WM must never paint borders, titles, backgrounds, or close glyphs. All product
floating windows use `ui.WindowFrame`; `wm/decoration.go` no longer exists.

### Floating vs Docked Overlay Ownership (CRITICAL)

- `FloatingWindow` is the actual positioned compositor window. It owns its
  window/content surfaces and supports focus, movement, resize, and close hit regions.
- `OverlayHost` is the typed lifecycle and policy controller around
  `WindowManager`/`FloatingWindow`. It is not a competing visual window type and
  must not be removed when a floating window body migrates to native components.
- Workspace settings, file/workspace picker, Find in Files, composer,
  text/reference editor, palettes, menus, and LSP popups are essential floating IDE
  surfaces. Keep their `OverlayHost` + `FloatingWindow` shell while migrating only
  their legacy body content.
- `PanelDeck` is exclusively for session-owned docked content between messages and
  chat input: session settings, questions, tool confirmation, YOLO, and background
  job details.
- The old `App.Overlay` inline tab system is deleted. Never reintroduce it or route
  floating windows through `PanelDeck` as a shortcut.

### Cell Types

- `Cell.Char` — the rune
- `Cell.Width` — display width (1 or 2 for wide chars, 0 for continuation)
- `Cell.FG` / `Cell.BG` — colors (`ColorDefault` = inherit during Blit)
- `Cell.Transparent` — if true, Blit skips this cell

### Blit Semantics

`surface.Blit()` composites a source buffer onto a destination:
- Transparent source cells are **skipped** — destination shows through
- Cells with `BG.IsDefault()` inherit the destination cell's BG color
- Wide cells (Width=2) write their continuation cell automatically
- Continuation cells (Width=0) are skipped

### Main Content Cache Rule (CRITICAL)

The `m.contentDirty` flag gates the `buildMainView()` call in `view.go`.
The cache is ONLY used when the editor is the active view — chroma
syntax highlighting is expensive:

```go
isEditorMain := m.EditorState.ActiveView != "" && m.EditorState.Editor.IsOpen()
useCached := isEditorMain && !m.contentDirty && m.mainContentCached != "" && m.mainContentSurf != nil
```

Agent tabs call `buildMainView()` fresh every frame. The launcher is a
native component tree painted into `mainLauncherSurf` every frame, so its
cursor, hover, scroll, and animation state remain responsive without using
the main-view ANSI cache.

**RULES:**
- `m.contentDirty = true` must be set ONLY in paths that modify editor
  content or cursor position.
- Do NOT set `m.contentDirty` for agent events, launcher operations,
  tree operations, floating windows, context menus — the cache is
  never read there.
- `m.contentDirty` is reset to `false` at the end of `View()`.

---

## Unified Popup Design System

All product floating windows use `ui.WindowFrame` (via `WindowFrameWidget` /
frame builders under `tui/widgets/*_frame.go` and native screen packages):
```
╭─ Title ──╮
│  body content line 1                   │
│  body content line 2                   │
╰─╯
```
- `PopupContentWidth(innerW)` helpers live on frame builders where needed
- FloatingWindow is always `WithDecoration(false)` — WM owns placement only
- Chrome is self-drawn by WindowFrame / ComponentWidget bodies

Legacy `tui/widgets/popupchrome.go` is **deleted**.

### Popup Click-Outside Behaviors
| Behavior | Meaning |
|----------|---------|
| `CloseRoute` | Close + route click through |
| `KeepRoute` | Keep open, route click to editor |
| `Ignore` | True modal — suppress click entirely |

---

## Terminal Display Width (emoji / ZWJ / ☰ / UV)

**Canonical doc:** [`TERMINAL_WIDTH.md`](./TERMINAL_WIDTH.md)

Read that before changing padding, truncation, hit-testing, or icon layout.
Summary:

- Layout and ultraviolet **must** share one width path (`third_party/xansi`
  + `tui/rendering.WidthMethod = GraphemeWidth`).
- `third_party/bubbletea` forces UV `cellbuf.Method = GraphemeWidth` from
  frame 0 (upstream defaults to WcWidth until mode 2027).
- Width is always 0/1/2; emoji/ZWJ → 2; ☰ (U+2630–2637) → 2 under
  Unicode 16 and current Windows Terminal.
- Never use `uniseg` / `runewidth` for column layout. Never probe only one
  side of the pipeline.

### Historical: Scrollbar Artifact Bug

`VisibleWidth` once used `uniseg` (☰ → 1) while UV used `ansi` (☰ → 2).
Matched UV; later full solution is `TERMINAL_WIDTH.md` (shared method +
corrections + bubbletea force-GraphemeWidth).

Deleted experiments: `terminal_probe.go`, `isWideCJK()`, `probedWide()`,
`makeNoColorBgTransparent()`.

---

## Pitfalls / Gotchas

### ANSI & Width
- **Full process:** see [`TERMINAL_WIDTH.md`](./TERMINAL_WIDTH.md).
- **ANSI search false-matches**: NEVER use `strings.Index(rawLine, label)`
  on an ANSI-styled string — the label text can match inside escape codes.
  Always `stripAnsi` first, then search plain text.
- **UTF-8 byte positions**: Box-drawing chars like `│` (U+2502) are 3
  bytes. Byte-position assertions break if you count them as 1. Use
  `len([]rune(s))` or account for the 3-byte offset.
- **`VisibleWidth`** is the only layout width API — it must stay identical
  to ultraviolet’s grapheme measure (`third_party/xansi`).
- **`buildTopBorder` has 4 separate cases** (no title/icon, icon only,
  title only, both). Each case has a unique fixed-width formula. Changing
  one without updating the others creates off-by-ones.
- **go.mod replaces** (do not drop without understanding TERMINAL_WIDTH.md):
  - `charm.land/bubbletea/v2` → `./third_party/bubbletea`
  - `github.com/charmbracelet/x/ansi` → `./third_party/xansi`

### Popup Positioning
- **`clampPopupPosition` exists** in `tui/viewmodel/mouse.go`. Use it
  for new popup placement.
- **WM uses screen-absolute (X, Y) coordinates.** Translate local-popup
  (X, Y) to screen (X, Y) using the floating window's `Position()`.

### Clipboard
- **Bracketed paste (`tea.PasteMsg`) is the primary paste mechanism.**
  Ctrl+V (KeyPressMsg) is a fallback that emits a `tea.PasteMsg`.
- **Ctrl+C is copy, NOT back.** Never reuse Ctrl+C for navigation.
  Esc is the universal back.

### Hover
- **`widget.Hover`** is the canonical hover type (`tui/widget/hoverable.go`).
- **`theme.Colors.Hover`** (`#21262d`) is the canonical hover background.
- **Priority: focused (keyboard) > selected > hovered > nothing.**

### Test Helper Ownership
- Go `_test.go` files in one package share package scope. A helper defined in a
  feature-specific test file may silently serve unrelated tests.
- Before deleting a legacy test file, search the whole package for every helper it
  defines (`func`, test constants, fixtures, and builders), not just tests named
  after that feature.
- Move genuinely shared helpers to the package's neutral `test_helpers_test.go` in
  the same change. Do not restore deleted production UI or add compatibility code
  merely to make tests compile.
- Run the complete package test and `go test ./...` after deletion; targeted screen
  tests cannot detect missing package-wide helpers.

---

## Pre-existing Test Failures (NOT caused by our work)
None — all previously-failing cross-platform tests (LSP path separators,
safety system-path detection on Windows, app config `HOME` vs
`USERPROFILE`) have been fixed.

## Test Commands
```bash
go build ./...
go test ./tui/editor/... ./tui/viewmodel/... ./tui/workspace/...
go test ./... 2>&1 | grep -v "^ok\|cached\|^?"
```
