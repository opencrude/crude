# Crude Architecture & Working Knowledge

## Agent Boot Contract

This root `AGENTS.md` is the canonical startup instruction file for this
repository. Read it before inspecting or changing code. Do not substitute a
generic README, tool probing, or assumptions from another project.

### First actions

1. Check `git status --short`; preserve unrelated user or agent changes.
2. Read the relevant sections of this file, then consult `PLAN.md`,
   `TUI_COMPONENT_SYSTEM_PLAN.md`, `TERMINAL_WIDTH.md`, and the relevant
   `docs/tui/*.md` file before changing architecture or terminal UI code.
3. Use `Glob` and `Grep` for repository discovery. Do not probe for generic
   screenshot tools when a repository workflow exists.

### Layer boundaries

- `app/` owns application configuration, workspace lifecycle, and CLI startup.
- `workspace/` owns project state, sessions, orchestrators, LSP, persistence,
  and domain services. It must not import `crude/tui`.
- `tui/` owns rendering and input routing. It calls workspace methods and
  subscribes to workspace events; it does not own domain persistence.
- Keep presentation-neutral contracts in `crude/types` or workspace view data.

### Visual capture workflow

Use the repository workflow only: `scripts/tuishot.sh`. For discussion-sized
captures use a readable full-width terminal, for example:

```bash
COLS=160 ROWS=40 FONT_SIZE=11 scripts/tuishot.sh /tmp/crude-screenshots \
  key:ctrl+alt+p type:settings key:Down key:Return
```

`Ctrl+Alt+P` opens the Command Palette. From the launcher, filter with
`type:settings`, press `Down` to select the Tools > Settings entry, then press
`Return`.

After every capture, close the Alacritty/Crude window before finishing. Do not
leave a `CrudeShot` window or capture process running. Keep `KEEP_OPEN=0` for
normal captures; if `KEEP_OPEN=1` is used for an explicit interactive review,
close it and verify `xdotool search --class CrudeShot` returns no window.

### Validation baseline

Run `go build ./...` after implementation changes. For TUI changes, run the
focused package tests and then `go test ./...`; run `golangci-lint run ./...`
when the change is complete. Do not update golden output unless the visual
change is intentional and verified.

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

### Current State (what is in the code today)

The three-layer split (`app/` → `workspace/` → `tui/`) is **DONE** — see
`PLAN.md` for that history. The root-host component-system cutover is also
landed: the viewmodel mounts the shell through one root `ui.Host` and paints
the persistent shell into one main surface. Later phase entries in
`TUI_COMPONENT_SYSTEM_PLAN.md` are historical status and design context; do
not assume an older phase description matches the current implementation.

| Area | Current state |
|---|---|
| `app/` | Thin CLI entry: creates workspace, bootstraps session, starts TUI. No callbacks. `app/app.go` ~270 lines. |
| `workspace/` | Per-project state machine (repo root, NOT `tui/workspace/`). Owns sessions, orchestrators, LSP manager, file ops, project search, workspace DB. Zero `tui/` imports (production + tests). |
| `tui/` | Dumb renderer + input router. Holds `*workspace.Workspace`, calls methods, subscribes to the event bus. `tui/tui.go` has no callbacks. |
| `tui/viewmodel/App` | Presentation state holder. Uses one root `ui.Host` (`rootHost`) + one shell surface (`mainSurf`), with retained per-session `deckHost`s mounted through the shell. |
| `crude/types` | Neutral contracts package; `tui/types` is a compatibility alias. Config split done: `AppSettings` + `SessionSettings` (three inheritance levels). Workspace overrides are FIELD-level, sessions snapshot — see `docs/tui/SETTINGS.md`. |
| `session/` | Session domain library (sessions are workspace-scoped under `~/.crude/workspaces/<hash>/sessions/`); consumed by `workspace/`. |

See `PLAN.md` (architecture + workspace progress) and
`TUI_COMPONENT_SYSTEM_PLAN.md` (component-system phases + §26 execution
order) for the phase plans. `ARCHITECTURE_TRANSITION.md` was deleted once
the transition it planned completed.

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

### `workspace/` (repo root) — Business state machine
| File | Role |
|---|---|
| `workspace.go` | Workspace struct (composition root: managers + references) |
| `foundation.go` | Mode/FocusTarget/PaneType enums, Logger, EventBus |
| `agent_manager.go` | AgentNode/AgentManager (tree + flat index) |
| `session.go` | Session struct (dialogs, refs, token metrics) |
| `agent_state.go` | AgentState streaming/thinking state + HandleEvent |
| `agent_state_events.go` | Tool handling sub-methods on AgentState |
| `conversation.go` | Canonical live message store (Conversation/Message) |
| `focus_stack.go` | FocusStack LIFO |
| `lsp_manager.go` | LSPManager (connections, diagnostics cache) |
| `references.go` | ReferenceStore (SQLite-backed @references) |
| `file_ops.go` | FileOpsService (filesystem mutations) |
| `project_search.go` | ProjectSearchService (Find in Files execution) |
| `view_data.go` | Presentation-neutral view data seams (TreeRowView, TabView, StatusView) |
| `bootstrap.go` | Session create/resume/delete, launcher flows |
| `db.go` / `paths.go` | Workspace DB + `~/.crude/workspaces/<hash>/` paths |

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

### Root Host + Shell Surface (Phase 13.9, since 2026-08-01)

The whole persistent shell is ONE component tree mounted on a single
`ui.Host` (`App.rootHost`) painted into ONE full-screen compositor surface
(`App.mainSurf`, z=0, re-mounted after every `BeginFrame`). The composite
is `shellRoot` (`tui/viewmodel/shell_root.go`, ID `"shell"`) whose slots
map to the shell screens:

```text
rootHost
└── shellRoot ("shell")
    ├── header     shellheader.Screen   — always; rect (0, 1, W, 2)
    ├── status     shellstatus.Screen   — always; rect (0, H-2, W, 2)
    ├── tabs       shelltabs.Screen     — active when tabBarVisible()
    ├── sidebar    shelltree.Screen     — active when Tree.Visible
    ├── chat       shellchat.Screen     — active when chat visible
    ├── input      shellinput.Screen    — active when input visible
    ├── editor     shelleditor.Screen   — active when editor open
    ├── launcher   launcher.Screen      — active when launcher visible
    └── deck       deckMount            — wraps the ACTIVE session's
                                          retained deckHost (per-session
                                          PanelDeck, Phase 12 design)
```

Rules:

- `View()` = bg fill → re-mount `mainSurf` → `configureShell()` (geometry
  via `computeContentLayout` + slot rects + screen projections) →
  `renderShellToSurface()` (unconditional `MarkLayout` + `Paint`) → WM
  render → composite. `syncRootFocus()` maps `focusTarget()` to the root
  host's single focused component each frame.
- `renderShellToSurface` fills `mainSurf` with the theme background BEFORE
  the tree paints: unmounted slots (launcher replacing the editor, chat
  replacing the launcher, ...) must not leave their previous opaque cells
  behind — every cell not covered by an active slot shows the clean
  background (this fixed the editor→launcher gutter artifacts).
- Mouse targeting is `rootHost.Handle` with raw screen coordinates; the
  Host computes `LocalX/LocalY` from layout geometry. `classifyShellRegion`
  remains only for viewmodel POLICY (focus-stack pushes, editor legacy
  routing via `routeEditorMouse`, sidebar-divider drag, status row,
  context menus). Host pointer capture is the ONE capture service — hidden
  slots cancel capture via `Host.Layout` `validatePointerCapture`.
- The editor screen stays input-no-op: editor keys/mouse route through
  viewmodel → `Editor.HandleEvent`. Deck panels keep their per-session
  `deckHost`; `deckMount` adapts paint (blit) and input (local coords).
- **Parent-local child rects:** `Host.paintNode` paints a child at (parent
  origin + child bounds) — child bounds are PARENT-LOCAL by contract. A
  shell screen that passes its absolute slot rect to a child's `Layout`
  double-counts the origin (the shellchat double-origin bug: chat content
  shifted right/down, and long conversations painted over the input prompt
  and status bar). Children must be laid out with `{X:0, Y:0, ...}` rects.
  Guarded by `TestAgentView_ChatPaintsInsideSlot` /
  `TestAgentView_LongChatDoesNotOverflowInputOrStatus`.
- **Chat gravity:** short chat content anchors to the BOTTOM of the viewport
  (`ui.VirtualList.SetBottomGravity(true)`, set by `shellchat.New`) —
  mirrors the renderer's `assembleOutput` gravity. Without it, short chats
  painted at the top of the chat area with a huge gap above the input
  prompt. Guarded by `TestAgentView_ShortChatAnchorsToBottom`.
- **Input shell is clamped to the status bar:** `computeContentLayout`
  clamps `InputLineCount` to `height - 2 - 3 - deck - tabs - 1` (status +
  top + deck + tab bar, one chat row minimum). The old layer surfaces masked
  oversized inputs with the status surface's higher z-order; the root host
  paints status FIRST in tree order, so an unclamped shell painted its rows
  over the status separator/content. The textarea scrolls internally within
  the clamped height. Guarded by
  `TestAgentView_LongInputDoesNotOverlapStatus`.
- **TextArea.Paint FillRect uses the PAINT origin, not the translated
  origin:** `TextArea.Paint` computes `ox = originX + r.X` /
  `oy = originY + r.Y` for its content, but `FillRect(buf, ox, oy, r, …)`
  re-adds `r.X`/`r.Y` internally, so passing `ox, oy` double-counts the
  origin and shifts the bg fill down by `r.Y` and right by `r.X`. For the
  chat input (bounds `r={X:pad, Y:1}` under a `shellinput` slot at row 56),
  the white `PopupBg` fill landed at row `56 + 2*1 = 58` (the status
  separator) and col `SidebarW + 2*pad + 2`, erasing the right portion of
  the divider and leaving a white band over the status content ("input
  pushes the right half of the bottom status and its separator out of
  screen"). `TextField.Paint` already passed `originX, originY` correctly;
  `TextArea` was the lone offender. Guarded by
  `TestAgentView_TextAreaBgDoesNotOverwriteStatus`.
- **Launcher workspace load is async** (2026-08-01): `openLauncher` paints
  immediately (last-known data) and loads `ListWorkspaceInfos` in a
  goroutine via `refreshLauncherAsync(cwd)` → `launcherWorkspacesMsg`
  (event-bus `Send`, generation-deduped by `App.launcherLoadGen`). The
  ~200ms DB scan must never block the update goroutine (back-to-launcher
  hotpath). Guarded by `launcher_async_test.go`.
- `themeConsumers()` walks `rootHost` (SetTheme propagates tokens to all
  mounted screens via `applyTokenSet`).

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

### Unconditional Main-Content Repaint (was: contentDirty cache — REMOVED 2026-07)

The `m.contentDirty` flag and the main-content ANSI cache were deleted.
`View()` rebuilds the main content surface every frame. This is safe because
the editor's expensive chroma work is **internally cached by line**
(`chromaLineCache` + `chromaDirtyFrom` + `lineGeomCache` in `tui/editor`),
so an editor repaint is a cell fill plus a cached-color walk:
~180-250µs at 120x40 (see `BenchmarkEditorPaint_*` in `tui/screens/shelleditor/`).

**RULES:**
- Never reintroduce a frame-level dirty flag for the main content. If a
  future perf problem appears, add a benchmark first (compare against
  `docs/tui/PERFORMANCE_BASELINES.md`) and cache at the editor level.
- `computeContentLayout()` may be called every frame; it only computes
  shell geometry (slot rects, chat/editor/input/deck rects) — no ANSI.
- The only remaining output cache is `m.cachedOutput`, used exclusively for
  drag throttling (FloatingWindow drag with no position change).
- `wm.FloatingWindow.contentDirty` is a separate, unrelated mechanism
  (window-surface invalidation) — do not confuse it with the removed flag.

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

### Agent Tab Close Is Minimize, Not Terminate (since 2026-08-13)

Closing an agent tab **minimizes** it: `AgentNode.Hidden` is set, the window
tab goes away, and nothing else changes. The orchestrator keeps streaming,
the conversation, panes, chat projections, scroll offsets, and session
ownership are all retained. A stray click must never destroy work in flight.

- `Workspace.MinimizeAgent` / `RestoreAgent` own the domain flag;
  `AgentManager.SetHidden` hides a whole subtree so a restored orchestrator
  comes back with its sub-agents.
- `AgentTabViews()` filters hidden nodes, so `syncAgentWindowTabs` cannot
  resurrect a minimized tab, and `purgeStaleAgentTabs` closes one that
  lingers.
- **`AgentMgr.Count()` vs `VisibleCount()`**: shell gates choosing between
  launcher / chat / editor must use `VisibleCount()`, otherwise a workspace
  whose agents are all minimized shows an empty chat instead of the
  launcher. `Count()` stays the true total for the status bar and the
  Agents rail badge — that badge is how a backgrounded agent stays
  discoverable, alongside the status bar's 💤 count.
- Restoring: the Agents panel lists minimized agents with a `minimized`
  detail; activating a row calls `restoreAgent`. `switchToAgentView` unhides
  on its own so every path back works.
- Terminating is explicit: `terminateAgent` (tab and Agents-panel context
  menus) still cancels the turn, stops the orchestrator, and drops state.
  `cleanupAgent` remains the destructive path; do not wire it to tab close.
- Guarded by `tui/viewmodel/agent_minimize_test.go`.

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

### Editor motion
- **`Editor.moveWordLeft` must keep climbing across blank lines**: word-left
  from a line start must skip whitespace-only lines and land on the previous
  line's last word start (guarded by `TestMoveWordLeft_CrossesLines`). A
  b323a3b rewrite once replaced the climb loop's `continue` with `return`,
  parking the cursor on blank lines — and SA4004 flagged it as a dead loop.
  When a "dead loop" warning appears, check for a swallowed `continue` first.

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
  - `github.com/charmbracelet/x/powernap` → `./third_party/powernap`
    (vendored so the LSP transport can be patched; see LSP I/O below)

### LSP I/O Architecture (since 2026-08-01)

The LSP subprocess pipes must NEVER block a caller goroutine (previously a
synchronous, context-unaware jsonrpc2 bufio flush on a back-pressured server
pipe froze Bubble Tea's update goroutine — the tab-close hang).

- `powernap` is vendored (`third_party/powernap`) with a `TransportWrap`
  hook in `ClientConfig` (see `lsp.ClientConfig`); `LSPIntegration.Start`
  passes a wrapper that installs `lspPipeMonitor` (`tui/editor/lsp_pipe.go`).
- `asyncPipeWriter`: `Write` only enqueues a copy (O(1)); ONE dedicated
  writer goroutine performs the actual pipe writes in FIFO order. The queue
  is byte-bounded (`lspWriteQueueCap`); a fully-stalled server trips the cap
  → fatal error → the jsonrpc2 connection fails fast (never blocks, never
  corrupts framing).
- `lspPipeMonitor.Done()` closes when the connection dies for ANY reason
  (read EOF = server died, write error, queue-cap, graceful close). The
  workspace `LSPManager` reaps dead clients via `reapOnDone`: removes the
  client, marks it `LSPFailed`, kills the lingering process, and the next
  `OpenFile` starts a fresh server.
- The workspace `LSPClient` interface has `Done() <-chan struct{}`; test
  fakes return nil to opt out of reaping.
- `LSPIntegration` gained `WithServerEnv(map[string]string)` (server
  subprocess env) and `MODELS_DEV_URL` overrides the models.dev catalog
  endpoint (tests / air-gapped).
- `readWorkspaceMetaBounded` (launcher workspace listing) is context-bound:
  both the read AND its goroutine's channel send abort on timeout — a stale
  read must never leak an open sqlite handle (that kept `workspace.db`
  locked on Windows). It also closes its `Rows` explicitly before the count
  query: the pool is capped at one connection, and an open Rows held it —
  the count waited on itself (self-deadlock) and every workspace scan
  burned the full 150ms bound (~12s for 92 workspaces on Windows; the
  launcher appears instantly now).

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
- **Activity rail entries are whole bands.** `activitybar.RowStride` is odd
  so the glyph sits on the band's exact middle row (`iconRow`), and every
  highlight — hover background and the `▌` accent marker — covers the full
  band. A one-row marker next to a multi-row hover box reads as
  misalignment. `SetPanelOpen(false)` drops the marker when the sidebar is
  collapsed so the rail shows its toggle state.

### Sidebar list rows: resource vs command
`ui.ListItem.Command` marks a button-like row (e.g. "New Terminal").
`activitylist` arms resource rows on the first click and activates on the
second (matching the pickers), but command rows fire on a single click — a
button that needs a double click reads as broken.

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

## Lint Gate (fully enabled + full-repo zero 2026-08-01)

The Phase 15 lint gate runs ALL default-family linters — `govet`,
`ineffassign`, `misspell`, `staticcheck`, `gosimple`, `unused`, `errcheck` —
with zero issues across the ENTIRE repo (CI runs `./...`). Do not
reintroduce dead code or unchecked errors: a new `unused`/`errcheck`/
`gosimple` finding fails CI.
```bash
golangci-lint run ./...
```

The design audit (`scripts/tui_design_audit.sh`) additionally enforces the
three-layer boundary: `workspace/` must never import `crude/tui`, raw ANSI
SGR lives only in `tui/cell|compositor|rendering`, and hard-coded hex lives
only in `tui/theme/`.
