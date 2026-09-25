# Crude Architecture & Working Knowledge

## Agent Boot Contract

This root `AGENTS.md` is the canonical startup instruction file for this
repository. Read it before inspecting or changing code. Do not substitute a
generic README, tool probing, or assumptions from another project.

### First actions

1. Check `git status --short`; preserve unrelated user or agent changes.
2. Read the relevant sections of this file, then consult `docs/plans/workspace-refactor-plan.md`,
   `docs/plans/tui-component-system-plan.md`, `docs/reference/terminal-width.md`, and the
   relevant `docs/tui/*.md` file before changing architecture or terminal UI code.
3. Use `Glob` and `Grep` for repository discovery. Do not probe for generic
   screenshot tools when a repository workflow exists.

## Engineering Standard

Crude is held to production IDE standards. A change is complete only when the
system is coherent, dependable in real workloads, understandable to future
maintainers, and pleasant for users. Passing a narrow unit test is necessary,
never sufficient.

### Diagnose before changing

- Reproduce the user-visible failure through the real product path before
  choosing a fix. Preserve the exact workload shape that exposes it: resumed
  dense sessions, paced pointer input, nested panes, narrow terminals, dirty
  buffers, concurrent agents, or cross-platform filesystem behavior.
- Measure the stage that the user says is slow or broken. Separate mutation
  latency, derivation/reflow latency, presentation latency, persistence, and
  asynchronous completion. Do not substitute a proxy counter for the outcome:
  "zero message renders" did not prove dense resize was fast while an O(N)
  export path still consumed each frame.
- Use profiles and call-path evidence when cost scales with data size. Compare
  empty/small and dense/hydrated workloads; a fast empty state does not validate
  a production session.
- Treat the user's observation as acceptance evidence. If the reported defect
  remains visible, the issue is still open even when tests written for the first
  hypothesis pass.
- When an attempted fix does not improve the measured result, stop layering
  patches. State that it failed, remove or correct the false assumption, and
  continue to the actual owner.

### Choose the right-sized fix

- **Crude is pre-production: do not preserve legacy product paths or persisted
  development formats.** Prefer the clean, fast, correct, maintainable design
  even when old local databases, snapshots, configuration shapes, tests, or UI
  adapters must be deleted or recreated. Add compatibility only when the user
  explicitly identifies a real external/production consumer; otherwise one
  authoritative path is mandatory.

- Perform a cost/benefit check before broad work. Use the smallest correction
  when one owner is clearly wrong and the invariant can be permanently guarded.
  Research established designs first when several caches, clocks, models, or
  layers compete for authority.
- Prefer one authoritative owner over synchronization among duplicate owners.
  Layout computes geometry; configurators publish it; projections derive rows;
  screens retain and paint them. No second layer may "helpfully" write the same
  state during a frame.
- Eliminate bug classes, not screenshots. Shared primitives must cover every
  consumer, including manually composed and nested variants. Do not leave a
  second legacy interaction, cursor, scrollbar, persistence, or rendering path
  that can drift again.
- Do not add compatibility code without a concrete persisted or external
  consumer. When compatibility is required, define its retirement boundary and
  keep it out of the new product path.

### End-to-end means end-to-end

- Follow the complete lifecycle: create, update, render, persist, resume,
  switch workspace/session/tab, resize, cancel, fail, retry, restore, prune, and
  shut down. Verify generation boundaries and stale asynchronous results.
- Correctness includes interaction ownership, focus/capture, loading/empty/error
  states, confirmations, recovery, discoverability, and clear status. A backend
  API without usable product UX is not a finished user feature.
- Safety-sensitive operations require immutable preview, precondition checks,
  transactional or rollback behavior, durable audit context, and an undo path.
  Fail closed on ambiguity; never silently guess ownership or overwrite later
  work.
- Respect all layer boundaries and UI-thread I/O rules. Moving blocking work to
  a different callback without ownership, cancellation, or generation checks is
  not an asynchronous architecture.

### Performance engineering

- Optimize measured work, not frame suppression. Model mutations are never
  dropped, delayed, or replayed slowly to make CPU graphs look better. Coalesce
  presentation independently and always commit the latest state by a bounded
  deadline.
- Never use zero-FPS locking. Idle cadence remains at least 2 FPS for cursor,
  terminal, hover, and activity correctness; active interaction targets one
  frame per 16 ms without forcing every subsystem to recompute.
- Cache derived data by explicit source/geometry/theme generations. Focus, tab
  selection, ticks, and merely presenting a frame are not source invalidation.
  Stable-height row changes are deltas; structural and height changes alone
  rebuild indexes.
- Add the benchmark that would have caught the regression before optimizing.
  Gate mutation latency and presented-frame latency separately, and include
  realistic dense content. Tighten ceilings when a structural fix lands.
- Do not loosen a gate to land a regression. If a required feature has an
  irreducible measured cost, document the reason and set a new ceiling from
  repeated measurements with limited headroom.
- CPU, allocation, memory, I/O, and startup costs all matter. Avoid per-item
  maximum-size buffers, per-frame O(N) exports, eager offscreen reflow, and
  repeated verification of immutable objects in hot paths.

### Validation and accountability

- Every regression test must fail for the original reason. Assert the user
  outcome as well as internal state: painted content moved, final geometry
  arrived, immediate and settled frames match, cached tabs stay warm, and files
  roll back byte-for-byte.
- Validate focused behavior, complete affected packages, race paths, full build,
  lint, docs, benchmark gates, and the repository test suite when feasible.
  Cross-platform code receives cross-compilation or platform-specific coverage.
- Use the repository visual workflow for real UI acceptance. Test both readable
  desktop and constrained terminal sizes, and close every capture process.
- Never call a task complete while a known reproduction still fails. Report
  blocked or aborted gates explicitly and distinguish them from passing gates.
- Keep architecture, plans, performance baselines, interaction references, and
  this file synchronized with the implementation. A stale invariant is a future
  defect, as demonstrated when obsolete window-decoration guidance disabled
  close, hover, drag, and resize together.

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

**Never run two `scripts/tuishot.sh` captures concurrently.** Alacritty window
matching, geometry, focus, and input injection are process-global; concurrent
captures can target the wrong window and invalidate both results. Finish and
close one capture before starting the next.

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

### Compact chrome language
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
`docs/plans/workspace-refactor-plan.md` for that history. The root-host component-system cutover is also
landed: the viewmodel mounts the shell through one root `ui.Host` and paints
the persistent shell into one main surface. Later phase entries in
`docs/plans/tui-component-system-plan.md` are historical status and design context; do
not assume an older phase description matches the current implementation.

| Area | Current state |
|---|---|
| `app/` | Thin CLI entry: creates workspace, bootstraps session, starts TUI. No callbacks. `app/app.go` ~270 lines. |
| `workspace/` | Per-project state machine (repo root, NOT `tui/workspace/`). Owns sessions, orchestrators, LSP manager, file ops, project search, local-history lifecycle, workspace DB. Zero `tui/` imports (production + tests). |
| `tui/` | Dumb renderer + input router. Holds `*workspace.Workspace`, calls methods, subscribes to the event bus. `tui/tui.go` has no callbacks. |
| `tui/viewmodel/App` | Presentation state holder. Uses one root `ui.Host` (`rootHost`) + one shell surface (`mainSurf`), with retained per-session `deckHost`s mounted through the shell. |
| `crude/types` | Neutral contracts package; `tui/types` is a compatibility alias. Config split done: `AppSettings` + `SessionSettings` (three inheritance levels). Workspace overrides are FIELD-level, sessions snapshot — see `docs/reference/settings.md`. |
| `session/` | Session domain library (sessions are workspace-scoped under `~/.crude/workspaces/<hash>/sessions/`); consumed by `workspace/`. |
| `localhistory/` | Immutable content-defined workspace snapshots, SQLite timeline/message labels, restore plans, verification, retention, and rollback. No TUI imports. |

See `docs/plans/workspace-refactor-plan.md` (architecture + workspace progress) and
`docs/plans/tui-component-system-plan.md` (component-system phases + §26 execution
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
    ├── footer     shellfooter.Screen   — always; rect (0, H-2, W, 2)
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
  and footer). Children must be laid out with `{X:0, Y:0, ...}` rects.
  Guarded by `TestAgentView_ChatPaintsInsideSlot` /
  `TestAgentView_LongChatDoesNotOverflowInputOrStatus`.
- **Chat gravity:** short chat content anchors to the BOTTOM of the viewport
  (`ui.VirtualList.SetBottomGravity(true)`, set by `shellchat.New`) so the
  transcript remains attached to the input prompt. Guarded by
  `TestAgentView_ShortChatAnchorsToBottom`.
- **Input shell is clamped to the footer:** `computeContentLayout`
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
  pushes the right half of the footer and its separator out of
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
  job details. Those docks share the chat `ColorBg` — they are not popup cards.
- **Deck and tab-bar slots span the FULL content column** (`SidebarW..width`, no
  `PadLR` inset in their slot rects) so separator rules run edge to edge, same as
  the chat and terminal dock. The `PadLR` model lives inside the components:
  `PanelDeck.contentPad` and `shelltabs.Screen.Layout` self-derive it from
  `TokenSet.PadLR()` (clamped at half the width on narrow terminals) and inset
  only their tabs and content.
- **Full-bleed separator rules are configurable, parent-published:** each
  `PanelDeck.Layout` calls `applyRuleBleed` with the deck row geometry
  (`offsetX`, `width`) through the content tree (`ruleBleeder` interface on Form,
  ActionBar, screens with their own divider). Components expand rules ONLY when
  they opted in with `SetRuleFullBleed(true)` — `NewDockedForm`/`NewDockedActionBar`
  do it by default; floating popups keep rules bounded. The Form paints into a
  buffer that reserves the left spill (`off = -min(0, ruleX)`) and blits shifted,
  so rules can start in the left padding without moving field geometry.
- **Readable-width panels:** `PanelEntry.Width = PanelReadableWidth` (session
  settings) clamps content via `ui.ReadableWidth` (≥200 → 1/3, ≥110 → 1/2, else
  full), left-aligned in the padded column; the parent enforces it.
- **Compact form tables:** `planCompactTable` caps the label→value bonus
  (`TableMaxLabelGapBonus`) and spends the rest before the trailing meta column;
  a `TableMinGap` gutter always separates the last column from the nav chevron.
  `FieldState.Columns` are also accepted by `FieldCycle` as spacer slots so a
  cycle control can align with a specific shared value column (Multi Agent sits
  under · provider). Zero-width leading columns must never abort row painting —
  `paintTableCells` skips blank cells before the width guard. Single-line
  editor rows (`FieldText`/`FieldPassword`) participate in the same table:
  `compactColumnsRaw` exposes their value as one cell, and `compactEditorX`
  starts the retained `TextField` at the plan's first value cell (both in
  `Layout` and `Paint`), so every second-column value on a docked form — nav
  columns, cycle controls, and editable text — shares one column. Never
  reintroduce label-hugging control geometry for compact editor rows; narrow
  docks fall back to `controlX` only when the row cannot hold the table.
- **Form hover-focusing:** `Form.Handle` moves the field highlight to the row
  under the pointer on `EventMouseMotion` (decorative rows never take it), like
  menus and lists; an open dropdown owns hover first.
- The old `App.Overlay` inline tab system is deleted. Never reintroduce it or route
  floating windows through `PanelDeck` as a shortcut.

### Cell Types

- `Cell.Char` — the rune
- `Cell.Width` — measured display width (1+ for a primary, 0 for continuation)
- `Cell.FG` / `Cell.BG` — colors (`ColorDefault` = inherit during Blit)
- `Cell.Transparent` — if true, Blit skips this cell

### Blit Semantics

`surface.Blit()` composites a source buffer onto a destination:
- Transparent source cells are **skipped** — destination shows through
- Cells with `BG.IsDefault()` inherit the destination cell's BG color
- Multi-column cells write all `Width-1` continuation cells automatically
- Continuation cells (Width=0) are skipped
- Occlusion is grapheme-atomic: writing over a primary OR continuation blanks
  the old span before the new cell is written. Never allow half an emoji to
  survive under a floating border.

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
  `docs/reference/performance-baselines.md`) and cache at the editor level.
- `computeContentLayout()` may be called every frame; it only computes
  shell geometry (slot rects, chat/editor/input/deck rects) — no ANSI and NO
  renderer/model setters. `configureChatScreenWithLayout` is the sole writer of
  chat renderer geometry; duplicate width writers caused dense-chat resize to
  invalidate every message twice per frame.
- `chatprojection.Renderer` owns the generation-keyed `MessageRowsSnapshot`.
  `shellchat.Screen` rebuilds its VirtualList only for structure/height/width
  changes and applies stable-height row deltas by index. Never export and
  republish the full conversation merely because a frame was presented.
- Committed chat width changes use dynamic-height virtualization: source and
  hydration invalidation remain eager, but width-only invalidation reflows
  viewport plus overscan and retains offscreen estimated heights until scroll
  exposes those rows. Never restore frame-time `EnsureHeights` for width-only
  changes; it caused multi-second release stalls on dense resumed sessions.
- Dynamic rows declare frame sensitivity in their rendered projection metadata;
  the renderer tracks those item IDs and invalidates only that set when the
  shared phase changes. Never restore role/tool-specific tick classifiers or
  broad tick invalidation. In-place metrics update through Conversation version
  changes and invalidate their exact row independently of animation.
- `m.cachedOutput` is presentation-only: `frameGate` reuses it while draining
  high-rate pointer/stream/PTY events, at most until one 16 ms deadline. Model
  mutations are never queued or dropped. Every presented frame still performs
  the unconditional root-shell repaint described above; idle ticks remain
  400 ms (2.5 FPS), never zero-FPS locking.
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
- Product `FloatingWindow`s use `WithDecoration(true)` to enable WM frame
  interaction geometry (close, title drag, border hover, resize). This flag
  paints nothing: WM still owns placement/interaction only and `WindowFrame`
  owns every visual chrome cell.
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
  launcher. `Count()` stays the true total for the aggregate status report and
  the Agents rail badge. The footer remains a quiet system-level utility rail;
  provider waits and tool-specific lifecycle detail live in the active header
  and chat timeline.
- Restoring: the Agents panel lists minimized agents with a `minimized`
  detail; activating a row calls `restoreAgent`. `switchToAgentView` unhides
  on its own so every path back works.
- Terminating is explicit: `terminateAgent` (tab and Agents-panel context
  menus) still cancels the turn, stops the orchestrator, and drops state.
  `cleanupAgent` remains the destructive path; do not wire it to tab close.
- Guarded by `tui/viewmodel/agent_minimize_test.go`.

### New Agent Surfaces a Tab Immediately (since 2026-08-14)

Creating or restoring an agent **must add its window tab and make it the
painted main view even while editor tabs are open**. A stray early return
in `syncAgentWindowTabs` when `EditorState.ActiveView != ""` used to skip
`OpenTab` entirely: New Agent created the session in `AgentMgr`, but the
tab bar stayed on the editors until every editor tab was closed and the
next tick finally synced.

- `syncAgentWindowTabs` always creates/updates/purges **visible** agent
  tabs, including while an editor is showing. Minimized agents still get
  no tab (`AgentTabViews` filters `Hidden`).
- Tick and other background callers must not steal the editor: `OpenTab`
  activates the new tab, so when an editor is the painted view the sync
  restores that editor as the window's active tab **without**
  `handleTabSwitch` (which would clear `ActiveView`).
- Explicit user actions — New Agent / `launcherNewSession` /
  `AddOrchestrator` / restore / `switchToAgentView` — call
  `activateAgentWindowTab` after sync so the agent becomes the selected
  tab and `handleTabSwitch` clears `ActiveView`. Do not call that helper
  from the tick path.
- Guarded by `tui/viewmodel/agent_new_session_focus_test.go`.

### Popup Click-Outside Behaviors
| Behavior | Meaning |
|----------|---------|
| `CloseRoute` | Close + route click through |
| `KeepRoute` | Keep open, route click to editor |
| `Ignore` | True modal — suppress click entirely |

---

## Terminal Display Width (emoji / ZWJ / ☰ / UV)

**Canonical doc:** [`TERMINAL_WIDTH.md`](./docs/reference/terminal-width.md)

Read that before changing padding, truncation, hit-testing, or icon layout.
Summary:

- Layout and ultraviolet **must** share one width path (`third_party/xansi`
  + `tui/rendering.WidthMethod = GraphemeWidth`).
- `third_party/bubbletea` forces UV `cellbuf.Method = GraphemeWidth` from
  frame 0 (upstream defaults to WcWidth until mode 2027).
- Start from Unicode-16 grapheme widths, then patched Bubble Tea asynchronously
  CPR-probes the effective terminal/multiplexer and publishes one immutable
  x/ansi profile shared by layout, surfaces, UV, and the renderer. Never infer
  widths from `runtime.GOOS`, `WT_SESSION`, or `$TERM`.
- Modern grapheme widths are normally 0/1/2; measured legacy grids may report
  wider clusters, and surfaces/UV preserve every continuation column.
- Never use `uniseg` / `runewidth` directly for column layout and never add a
  widget/platform correction. Calibration belongs in the shared width profile.

### Historical: Scrollbar Artifact Bug

`VisibleWidth` once used `uniseg` (☰ → 1) while UV used `ansi` (☰ → 2).
The first fix shared GraphemeWidth but incorrectly keyed a correction to
`runtime.GOOS`, which failed under WSL/multiplexers. The current solution is
the async effective-grid profile in `docs/reference/terminal-width.md`.

Deleted experiments: `terminal_probe.go`, `isWideCJK()`, `probedWide()`,
`makeNoColorBgTransparent()`.

---

## Pitfalls / Gotchas

### UI-thread I/O and project generations (2026-08-13)
- **`EventBus.Send` is synchronous.** Never call it from a command that is
  already executing on Bubble Tea's `Update` goroutine. File mutations return
  `FileOperationResultMsg` directly, terminal close is command-owned, and OSC
  title publication runs from a `tea.Cmd`; reintroducing an event echo on any
  of those paths can deadlock `Program.Send`. Manual tree mutations use
  `ProcessLocalMutation`; only real background watcher events use
  `ProcessWatcherEvent`. Backend agent events enter the TUI through its FIFO
  message pump so session teardown never waits on the active `Update`.
- **Clipboard and picker I/O never run in `Update`.** Clipboard requests use
  the App-owned FIFO worker and carry an editor/session/find-replace owner plus
  generation; image reference persistence is a separate command.
  Directory reads are cancelable/bounded at the UI boundary and require the
  picker pointer, open epoch, intent sequence, and request generation; a stale
  result cannot mutate a reopened picker. Keep test-only backend seams in
  `async_io_test_helpers_test.go`, not production code. Editor Git blame and
  line-edit annotation (`AnnotateFile`) also run in a command, keyed by
  `editorGitGen` plus `workspaceSwitchGen`.
- **A workspace switch is an explicit generation boundary.** Stop the old tree
  watcher first; cancel search without replacing its monotonic ID service;
  invalidate git/launcher/editor-git/LSP callbacks and request contexts; close every
  distinct editor watcher and project-bound overlay; clear the old input draft;
  then rebuild managers and projections. Validate the target directory and DB
  before teardown, apply its effective theme before construction, and build
  the root tree plus recursive watcher asynchronously with generation checks.
  Terminal IDs are process-wide monotonic so queued old-manager messages cannot
  target a replacement PTY.
- **The header Git identity reconciles itself from watcher events.** The
  branch, changed-file count, and tracking on the header segment come from the
  Source Control snapshot, so that snapshot must not depend on the user opening
  the Source Control panel: file-watcher and repository-metadata events arm a
  debounced (`gitSnapshotIdleDelay`), burst-capped (`gitSnapshotHoldLimit`),
  fallback-bounded (`gitSnapshotFallback`) silent read that the tick loop
  starts (`tui/viewmodel/git_snapshot.go`). Reconciliation is silent by
  contract — no panel loading flash, no toast, no `StatusCenter` operation, no
  commit-field touch — and it never starts while any Git read is in flight,
  because superseding a refresh or mutation would drop that operation's notice,
  commit clear, and error report. An unchanged projection must not rebuild the
  panel list or invalidate the editor gutter. `beginWorkspaceSwitch` abandons
  in-flight read slots and `restore` gives them back, so an old project's
  result can never occupy the replacement project's slot.
- **Blocking PanelDecks shield pointer input as well as keys.**
  `blockingDeckShieldsPointer` consumes coordinates outside the deck before
  root-Host hit testing, cancels stale underlying captures, and permits only a
  deck-owned drag to release outside its slot.
- **PTY input is bounded and non-blocking.** `ptyWriter.Write` either queues the
  complete payload or returns `ErrTerminalBackpressure`; never silently drop
  bytes. `NormalizeTerminalSize` is the shared emulator/PTY 8×2 floor, and a
  failed PTY resize must not commit the emulator grid.

### Editor motion
- **`Editor.moveWordLeft` must keep climbing across blank lines**: word-left
  from a line start must skip whitespace-only lines and land on the previous
  line's last word start (guarded by `TestMoveWordLeft_CrossesLines`). A
  b323a3b rewrite once replaced the climb loop's `continue` with `return`,
  parking the cursor on blank lines — and SA4004 flagged it as a dead loop.
  When a "dead loop" warning appears, check for a swallowed `continue` first.

### ANSI & Width
- **Full process:** see [`TERMINAL_WIDTH.md`](./docs/reference/terminal-width.md).
- **ANSI search false-matches**: NEVER use `strings.Index(rawLine, label)`
  on an ANSI-styled string — the label text can match inside escape codes.
  Always `stripAnsi` first, then search plain text.
- **UTF-8 byte positions**: Box-drawing chars like `│` (U+2502) are 3
  bytes. Byte-position assertions break if you count them as 1. Use
  `len([]rune(s))` or account for the 3-byte offset.
- **`VisibleWidth`** is the only layout width API — it must stay identical
  to ultraviolet’s grapheme measure (`third_party/xansi`).
- **Editor cells must retain full graphemes.** Geometry width plus only the
  first rune is lossy: UV remeasures the emitted rune and shifts right-edge
  chrome. Paint `Cell.Content` plus the calibrated width and continuations.
- **`buildTopBorder` has 4 separate cases** (no title/icon, icon only,
  title only, both). Each case has a unique fixed-width formula. Changing
  one without updating the others creates off-by-ones.
- **go.mod replaces** (do not drop without understanding `docs/reference/terminal-width.md`):
  - `charm.land/bubbletea/v2` → `./third_party/bubbletea`
  - `github.com/charmbracelet/x/ansi` → `./third_party/xansi`
  - `github.com/charmbracelet/ultraviolet` → `./third_party/ultraviolet`
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
- **One owner for text selection and copy**: `ui.TextClipboardTarget` +
  `textClipboardChord` in `tui/ui/text_clipboard.go`. Every text control
  (TextArea, TextField, and the composites that wrap them) accepts Ctrl+C and
  Ctrl+Shift+C (copy the selection), Ctrl+X (cut the selection, or the whole
  value when nothing is selected), Esc (drop the selection, then fall through),
  and copy-on-release after a mouse drag — never keep a pointer-owned selection
  after the release. Hosts add policy on top: the chat composer clears its
  draft on an unhandled Ctrl+C, and its Esc ladder is selection → draft →
  cancel the turn.
- **Whole-text Copy/Cut in context menus** comes from
  `App.textClipboardMenuSection` (`tui/viewmodel/text_clipboard_menu.go`): the
  composer menu acts on the draft, and any text control under a right-click
  (popup forms, filters, search fields, question-deck write-ins) gets the same
  section. Cut must flow through the owning component's `Handle`, never by
  mutating the inner editor directly, so composite models stay in sync.
- **Masked controls (`TextSecret`) refuse every clipboard command**, so a
  password/API-key field can never be exfiltrated through the clipboard.

### Hover
- **`widget.Hover`** is the canonical hover type (`tui/widget/hoverable.go`).
- **`theme.Colors.Hover`** (`#21262d`) is the canonical hover background.
- **Priority: focused (keyboard) > selected > hovered > nothing.**
- **Activity rail entries are whole bands.** `activitybar.RowStride` is odd
  so the glyph sits on the band's exact middle row (`iconRow`), and every
  highlight — hover background and the `▌` accent marker — covers the full
  band. A one-row marker next to a multi-row hover box reads as
  misalignment. `SetPanelOpen(false)` drops the marker when the sidebar is
  collapsed so the rail shows its toggle state. The rail keeps one blank
  column before its panel rule so file-tree / Agents / Terminals / Git
  left borders are not flush against the icons.

### Scrollbar direct manipulation
- `ui.ScrollbarControl` is the only click/drag/wheel interaction math. Nested
  chat panes and terminal scrollback adapt their models to it; never duplicate
  thumb mapping or track-click formulas.
- Keep the painted stroke one cell. `PointerHitBounds` expands inward to a
  configurable 2-3 cell target, and explicit hit bounds outrank deeper content
  descendants in `Host.HitTest`. Adjacent scrollbars must cap expansion so
  their targets never overlap.
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

### Text cursor
- Every product text cursor is a blinking reverse-video block overlay. Never
  paint `|`, `│`, a bar, or an underline cursor.
- The cursor preserves the grapheme under it and toggles `cell.AttrReverse`;
  at end-of-input it reverses the existing blank cell. It never inserts a
  glyph, shifts placeholder text, or consumes layout width.
- Native TextField/TextArea and editor paths share `ui.PaintBlockCursor`
  semantics. Embedded terminal applications may control blink/steady timing,
  but DECSCUSR shape requests are normalized to the same block geometry.
- Cursor visibility derives from the App-owned Unix-millisecond frame clock;
  floating Hosts receive the same frame through `ComponentWidget`. Focused
  frame-sensitive windows alone invalidate on idle ticks. `ColorCursor` maps to
  the softer muted palette before reverse-video, never full foreground white.

### Editor horizontal geometry
- Horizontal scrollbar extent is the whole document, never visible rows. The
  PieceTable revision and retained per-line width index rebuild once on open or
  width-profile change, update one line for ordinary edits, and reduce the max
  when the former widest line shrinks.
- Handle native `MouseWheelLeft`/`MouseWheelRight`; Shift+vertical-wheel is the
  compatibility fallback for terminals that encode horizontal trackpad motion
  that way. Horizontal gestures must not change vertical offset.

### Editor gutter and save authority
- `editor.Buffer` is the one live-text/savepoint authority shared by views.
  `workspace.DocumentSaveService` is the one autosave-deadline and disk-commit
  authority. Editor, PieceTable, watchers, and LSP must never write files or
  maintain competing dirty flags.
- Git gutter state is structured HEAD-to-buffer hunks, never reparsed rendered
  patches. Paint derives current-line rails and exact deletion boundaries from
  those hunks; diagnostics, numbers, change rails, and source spacing keep
  independent stable lanes. Stale async hunks never attach to new revisions.
- Automatic saves are opt-in and text-only. They never format, reload, move the
  cursor, clear selection/undo, or emit synthetic text changes. Manual format
  runs on an immutable snapshot and applies as one revision-checked undo group.
- Save acknowledgements identify an exact buffer revision. Newer text remains
  dirty. Exact watcher reconciliation replaces time-based self-event suppression;
  known external changes fail closed and preserve both local and disk content.
- Every Crude-owned writer, file operation, and Local History restore shares
  `fileio`'s cancellable mutation lease. This prevents autosave, agent tools,
  rename/delete, workspace edits, and restore from racing each other.

### Local History
- Startup recovery reads only manifests/plans missing from the SQLite index.
  Snapshot identity and entries commit atomically; never replay all historical
  entries on open. LoadManifest/LoadPlan still verify immutable data on use.
- `localhistory.Store` owns immutable objects/manifests/plans and SQLite index
  metadata. `workspace.LocalHistoryService` owns watcher lifecycle, settings,
  message scopes, reconciliation, and restore suppression. TUI only calls
  workspace methods and renders neutral plans.
- Never write history inside the project, follow a symlink parent, or apply a
  plan whose current hash/mode/type differs from preview. Safety snapshots are
  mandatory; every failure after mutation rolls back before returning.
- `/undo` always opens preview. Conflicts require an explicit exact choice;
  non-conflicting later text edits may use the all-hunks-applied reverse merge.
  Esc, close, or an empty selection never changes files.
- Agent undo is operation-scoped, not a message-wide workspace reset. Direct
  file tools durably record exact preimages and intended postimages before the
  first mutation; failure to persist aborts the tool. Watcher-only, editor,
  external-actor, and opaque-command changes remain workspace history but are
  never assigned to an agent. Selective undo reverses operation receipts, preserving
  pre-operation external state and failing closed across overlapping/later
  ownership.
- Agent filesystem authority is not confined to the project root. Direct tool
  mutations use compact relative keys inside the workspace and canonical
  absolute keys outside it; both receive the same operation receipt, preview,
  safety snapshot, crash recovery, and selective restore guarantees. Never
  reject or silently omit a direct mutation merely because its path is external.
- Snapshot, SQLite, verification, pruning, and restore work always runs in a
  command/background service, never Bubble Tea `Update`. Project switch and
  shutdown wait for active restore ownership.
- Restores append one hidden `<workspace_history_event>` to provider context and
  one visible canonical `history_restored` context-boundary table without
  rewriting accepted conversation prefix. The completed boundary is static,
  survives resume, closes the restore modal, and exposes its safety-snapshot
  inverse from the row context menu; never render it through `ActivityDot`.

### Shared text input
- `ui.TextEdit`, `ui.TextField`, and `ui.TextArea` own the non-document editing
  contract: selection replacement, undo/redo, word/document/page movement,
  Ctrl/Super shortcuts, copy/cut, bracketed paste, owner-keyed async Ctrl+V,
  multi-click selection, and captured outside drag with autoscroll. Add behavior
  there, never to one popup.
- `Form` is a composite pointer owner because its retained fields are not Host
  children. Once an inner input starts a drag, `Form.pointerField` must route
  every captured motion/release to that same field before hover-focusing.
- Child bounds stay parent-local during paint as well as layout. A FormField
  passes its own origin unchanged to TextArea; pre-adding the textarea's Y=1
  shifts its background into the description row.
- Source-code projections expand tabs at tab stops before width, wrapping,
  syntax coloring, and paint. Raw tab cells have no stable terminal geometry.

### Activity-dot animation
- `ui.ActivityDot(spinFrame)` is the only product activity-dot animation:
  synchronized Unix-millisecond clock, `●` for 400ms then `○` for 400ms.
- Every active fixed-strip page uses `ui.TextTide`, a paint-only ink sweep that
  remains visible throughout its cycle. Explicit reasoning kind and semantic
  blocks come from provider wire contracts. Flattened unlabelled text gets a
  neutral provisional strip preview and expandable full train, not a guessed
  raw pane or confirmed summary. A strong provider-reported usage discrepancy
  can settle the accepted request and, with repeated connection-scoped evidence,
  calibrate subsequent streams. Explicit wire raw always keeps its pane. Never
  classify by model name or infer from current-request usage before it arrives.
- Active collapsed compact thoughts paint no stray transcript header. The fixed
  strip owns a clickable chevron and the normal disclosure shortcut reveals the
  complete retained trace in the transcript. Thinking ink is muted/theme-aware,
  not foreground white. Provisional thinking tides reversibly; a confirmed
  summary tides left-to-right through the remaining active turn for that serving
  connection. A provider/account/model change resets the direction. Do not
  change transcript text, width or rows on animation frames.
- Reasoning contracts live in `types/reasoning`, request outcomes in
  `types/accounting`, and immutable plan activity in `types/activity`. Rendering
  and the event pump never import provider implementations for these contracts.
  `chatprojection.ActivityStrip` derives cached rows; `shellchat` only publishes
  geometry and paints. Never parse Markdown, decode plan-tool JSON, or access
  storage from `Screen.Paint`.
- Chat reserves activity-strip geometry independently of phase. Transient
  provider/tool status must not appear/disappear as transcript rows. Durable
  stop, history and compaction boundaries remain visible. Memory-summary titles
  load through session-owned cancellable jobs, never from a screen or Update.
- The strip prioritizes upload bytes/percent, provider waiting, retry and required
  action over optional rotation. Tidal motion applies to every active strip
  state; provisional and summarized thinking get the compact preview. Do not
  echo the user prompt there. Plan progress/current
  steps, objective and next step follow in order, packing to width or rotating
  on the shared clock. Successful-call counts are trailing, lowest-priority data.
  The session's one `plan.Store` publishes its committed immutable activity view;
  the strip never becomes another plan owner.
- Active chat lifecycle/thinking/tool/sub-agent/session rows use it with one
  stable semantic color. Never add local speed formulas, brightness/color
  pulses, braille frames, or `AttrBlink`; see `docs/guides/component-authoring.md`.
- Activity groups and parallel batches share one staged tool-leaf projection.
  Expansion reveals hidden branches in place; it never replaces the tree with
  standalone legacy rows. Execution events carry exact tool arguments, not
  formatted/truncated summaries. Native command JSON envelopes are decoded only
  at presentation; file edits/writes/patches stay separate and default-open.
  Summary counts and visible membership are source/disclosure-derived, not
  rescanned on animation ticks. See `docs/reference/chat-activity.md`.
- Tool titles keep action, operand and metadata as distinct semantic spans,
  with theme-dependent colors that do not change with lifecycle. Wrapping must
  retain those roles, including in compact sub-agent lists. Reserve the red
  title and `×` indicator for actual failures; never recolor the operand or
  whole result red, or turn settled tool rows into full-foreground text.
- Keep at least six recent eligible tool leaves and the last two tool request
  rounds visible, extending backward by whole rounds; completion alone must not
  fold a leaf. Completed thoughts do not consume that tool budget. Promoted
  thinking returns to its original canonical transcript slot on completion and
  releases the temporary pane, so long thoughts cannot orphan adjacent tools.
  Transcript items own one leading spacer; subpanes never add trailing gaps.
  Persist `ToolRoundID` and inherit it in parallel projections. Retry
  feedback is one retained, cancellable row. User stops preserve readable work
  and append a durable `turn_stopped` boundary; never strike through cancelled
  content or invalidate a prior completed turn.
- The footer is static, low-profile aggregate text. Only required
  user action (`⚠`) and failures (`✕`) receive attention signs.
- Compact editor status retains current-line blame in the active header; full
  blame identity and Copy blame commit stay available in Status Overview and
  the editor context menu. Footer-right carries meaningful workspace-scoped LSP
  health, project Problems, and background work (Git identity and tracking stay in
  the header branch segment); it omits zero counts but must not collapse to a
  placeholder when those facts exist.
- Editor position uses compact `line:column` header geometry in the same muted
  treatment as blame/uptime. Footer-left is the guidance group: unread Notices,
  then a small ranked navigation set (find, next tab, focus tree), then Help.
  It must remain focus-aware and must not regress into the old Save/Undo/Redo
  inventory. The status group is capped at half the footer row so a wide status
  set never truncates the guidance group; keep the two-group split in
  `configureFooterScreen`, not in the screen painter.
- Agent header is muted telemetry only: model, `↑` latest reported input tokens
  over that request's runtime context capacity/occupancy, `↓` cumulative agent
  output, reported cache-hit rate, known estimated cost, and uptime. It never
  changes `↑` into upload bytes or adds lifecycle/status text. Request age,
  serialized payload and lifecycle belong in agent/tab details and Activity;
  existing permission/question/safety surfaces remain authoritative.
- Accounting request IDs use `math/rand/v2` for non-security uniqueness, never
  runtime turn/step counters that restart after resume. UI and persistence share
  `sessiondb.UsageRecordFromEvent`; preserve uint64 generations and per-field
  reported availability. Provider omissions are not measured zeros. A revision
  replaces the same request once and older revisions cannot overwrite it. Context
  stays agent-local even when inspecting subtree/session totals.
- Current model selection is read from the owning session's configuration, not
  its last usage record or another tab's backend. Per-model accounting groups
  the immutable producing account/provider/model. Displayed turns count successful
  API calls, not actor generations; request outcomes are durable and immutable
  across billing revisions. Unknown outcomes remain unknown. Actor generations
  are separate cancellation/recovery identities. Preserve cost availability.

### Dense resume and memory shutdown
- Hydration publishes one normalized stopped transcript, never each growing
  prefix. Diff previews select their lexer once per file and tokenize only their
  visible preview budget; counting omitted visual rows must not highlight them.
- Hydration projects explicit display snapshots first, then excludes context-only
  provider envelopes using visibility/metadata. Recalled memory without a display
  snapshot must contribute zero chat rows, not blank fallback rows sorted at the
  transcript tail by their later persistence timestamp. Restore and compaction
  boundary snapshots remain visible; provider history is not deleted.
- Memory extraction and maintenance are session-owned cancellable jobs. Closing
  cancels and joins them before the workspace DB closes, without launching new
  provider work. Durable turn mirrors finish, and pending extraction watermarks
  recover on next startup, including a resume of the same session.
- Workspace memory and the session registry are mandatory session dependencies.
  `CreateSession`/`ResumeSession` retry a transient workspace-DB startup failure
  and wire the recovered store before returning; never launch an agent with a
  nil memory store and a merely logged `db_open_failed` warning.
- Turn finalization writes its one batched transcript mirror synchronously on the
  off-UI orchestrator goroutine before releasing the turn. Never register a
  flush WaitGroup after observers may already be waiting; that races shutdown,
  manual compaction, and persistence acceptance tests.

### Context compaction and runtime continuity
- Provider-reported request input is the context authority. Project the next
  request from that baseline plus reported completion and semantic local deltas;
  fallback estimation is `ceil(runes/4)` over canonical content, never stream
  envelopes or repeated provider-item wrappers.
- Completed provider replay metadata is canonicalized before estimation,
  persistence, replay, and logging. OpenAI reasoning fragments merge once;
  signed or encrypted protocol blocks remain byte-exact.
- Opaque replay state is bound to the producing provider endpoint/protocol and
  exact request prefix. Request-local filtering never mutates the transcript:
  cross-provider or changed-prefix turns omit opaque reasoning, while a model
  switch on the same provider preserves it for the provider's documented
  compatibility check. Compaction summarizes visible/tool state only and never
  attempts to inspect ciphertext.
- Established partial stream interruptions never replay a stochastic request. Commit
  canonical partial assistant output and append one hidden continuation request;
  if tool-call construction was interrupted, execute none of those calls, drop
  malformed protocol fragments, and ask the model to issue complete calls from
  scratch. Upload/header stalls may retry the original request because no model
  output was accepted.
- Automatic and manual compaction use the resolved Aeron category model for
  four no-tools extraction stages over one immutable cached source (intent,
  work, state, continuation). The retained boundary records that actual model.
  Models emit terse plain text; Crude escapes and wraps the four bodies in one
  deterministic XML checkpoint. Retry only the current stage, at most three
  recoveries; cancellation never retries. Previous-generation archival,
  checkpoint installation, and terminal operation state commit atomically.
  The exact source context is durable before extraction starts, including
  in-flight turn state. Failure, cancellation, or pre-commit crash leaves that
  source authoritative.
- Compaction presentation is one retained, operation-ID-keyed context-boundary
  table on the normal chat background with full-bleed top/bottom rules using
  the active-rail accent color. It shows
  real stage units, never fabricated completion percentages. Active, retrying,
  completed, cancelled, failed, and resumed-interrupted forms share geometry;
  generic turn cleanup must never consume these durable operation rows.
- Context-boundary tables are ordinary chat content for interaction: native
  rows, full-bleed rules, and body cells all participate in drag selection,
  clipboard copy, hover/hit testing, and row context menus. Their distinct
  visual treatment must never create a second opaque interaction path.
- Message origin and visibility, not provider role or string prefixes,
  distinguish human/BTW input, runtime context, continuation controls, child
  wakes, assignments, and checkpoints. Runtime or memory data is never
  attributed as human intent.
- New-format resume is passive: restore exact per-agent active context and turn
  state, mark interrupted work, issue zero provider calls, and restart zero
  children. Existing development sessions have no compatibility path.
- Root checkpoints preserve human intent and child-result ownership; child
  checkpoints preserve assignment, policy, file ownership, evidence, and the
  `write_report` obligation. Compaction requests never enter memory extraction.

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
make docs-check   # documentation acceptance gate
```

## Documentation Gate

Documentation layout, naming, status headers, and cross-references are
enforced by `scripts/docs_check.sh` (`make docs-check`, also a CI step) —
rules and index live in `docs/README.md`. Doc filenames are kebab-case under
`docs/{architecture,reference,guides,plans}/`; every document carries a
`> Status:` header; a completed plan moves to `docs/plans/` and gets
DONE/LANDED/RESOLVED/SUPERSEDED in its header instead of being deleted.

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
