# Crude Architecture & Working Knowledge

## Agent Boot Contract

This root `AGENTS.md` is the canonical startup instruction file for this
repository. Read it before inspecting or changing code. Do not substitute a
generic README, tool probing, or assumptions from another project.

### First actions

1. Check `git status --short`; preserve unrelated user or agent changes.
2. Read this file completely, then consult only the references relevant to the
   change. Prompt, model-runtime, or instruction-intake changes require
   `docs/reference/prompt-architecture.md`, `docs/reference/agent-orchestration.md`,
   and the relevant sections of `docs/reference/adaptive-context.md`. Application
   layer changes require `docs/reference/settings.md`; terminal UI changes require
   `docs/guides/component-authoring.md`, `docs/reference/terminal-width.md`,
   `docs/reference/surface-catalog.md`, `docs/reference/control-usage.md`, and the
   applicable current `docs/reference/` interaction contract. Only active plans
   remain under `docs/plans/`.
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

- **Active development: no backward compatibility.** Architecture, internal
  contracts and persisted formats may change incompatibly; code cleanliness and
  one authoritative current path take priority. Do not add automatic upgrades,
  legacy readers, fallback adapters or compatibility branches to accommodate
  older local data.
- **Breaking formats does not permit losing existing work.** Back up affected
  local workspace/session data, manually align it to the current architecture
  outside product code, and verify content, identity, provenance, history and
  resume behavior. One-off conversion tooling must not remain in the codebase.
  Update fixtures and documentation to the current contract rather than keeping
  an old runtime path. Existing local data is not an exception to this rule.
- **Git-based development rollbacks that can discard uncommitted work are
  strictly prohibited during this phase.** Do not use `git checkout --`,
  `git reset`, `git restore`, `git clean`, or equivalent operations to undo a
  task by replacing or discarding local changes. Inspect the affected files and
  manually reverse only the intended edits, preserving all unrelated user and
  agent work. This restriction is about destructive rollbacks, not normal
  non-rollback Git operations.

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
- A failure caused by an outdated local format requires explicit manual data
  alignment, not a compatibility layer added to the current product path.

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

On Linux use `scripts/tuishot.sh`. On Windows use
`pwsh scripts/tuishot.ps1`; it launches native Windows Terminal, tracks the
dedicated top-level HWND rather than the shared `WindowsTerminal.exe` process,
activates and verifies that exact window before input/NirCmd capture, and closes
only what it created. Never use WSL to inspect a Windows product failure and
never capture or send input to an unrelated user window. See
`docs/guides/windows-visual-capture.md`.

Never run two visual captures concurrently on either platform. Alacritty and
Windows desktop input/focus are process-global; concurrent runs can target the
wrong window and invalidate both results.

For discussion-sized Linux captures use a readable full-width terminal, for
example:

```bash
COLS=160 ROWS=40 FONT_SIZE=11 scripts/tuishot.sh /tmp/crude-screenshots \
  key:ctrl+alt+p 'type:application settings' key:Return
```

The equivalent native Windows capture is:

```powershell
pwsh scripts/tuishot.ps1 -Cols 160 -Rows 40 .\tmp\crude-screenshots `
  'key:ctrl+alt+p' 'type:application settings' 'key:Return'
```

`Ctrl+Alt+P` opens the Command Palette. Filter with `type:application settings`
and press `Return` to activate the highlighted Application Settings entry. The
broader `settings` query also matches command descriptions; an extra `Down` can
select a different action and is not part of this route.

After every capture, close the dedicated Crude window before finishing. Do not
leave a `CrudeShot`/`CrudeShot-*` window or capture process running. Keep
`KEEP_OPEN=0` for normal captures. If `KEEP_OPEN=1` is used for an explicit
interactive review, close it and verify no matching window remains
(`xdotool search --class CrudeShot` on Linux; `tasklist /v | findstr
/i CrudeShot` on Windows).

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
│  ~/.crude/config.json and app-global catalogs       │
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
`shellport.Workspace` (see `docs/reference/shell-port.md`; `shellport.Local`
adapts the in-process `*workspace.Workspace`) and calls methods on it. The
workspace publishes events that the TUI subscribes to.

### Current State (what is in the code today)

The three-layer split (`app/` → `workspace/` → `tui/`) and root-host component
system are current architecture: the viewmodel mounts the shell through one root
`ui.Host` and paints the persistent shell into one main surface. Current contracts
live in this file plus `docs/architecture/`, `docs/reference/`, and
`docs/guides/`; completed migration plans are deleted rather than retained as a
second source of truth.

| Area | Current state |
|---|---|
| `app/` | CLI attaches an independent shell via ConnectCore; the per-user core owns application config/catalog/history and constructs retained workspaces. No in-process interactive fallback. |
| `workspace/` | Per-project state machine (repo root, NOT `tui/workspace/`). Owns sessions, orchestrators, LSP manager, file ops, project search, local-history lifecycle, workspace DB. Zero `tui/` imports (production + tests). |
| `tui/` | Dumb renderer + input router. Holds a `shellport.Workspace`, calls methods, subscribes to the event bus. `tui/tui.go` has no callbacks. |
| `tui/viewmodel/App` | Presentation state holder. Uses one root `ui.Host` (`rootHost`) + one shell surface (`mainSurf`), with retained per-session `deckHost`s mounted through the shell. |
| `crude/types` | Neutral contracts package; `tui/types` is a compatibility alias. Config split done: `AppSettings` + `SessionSettings` (three inheritance levels). Workspace overrides are FIELD-level, sessions snapshot — see `docs/reference/settings.md`. |
| `session/` | Session domain library (sessions are workspace-scoped under `~/.crude/workspaces/<hash>/sessions/`); consumed by `workspace/`. |
| `localhistory/` | Immutable content-defined workspace snapshots, SQLite timeline/message labels, restore plans, verification, retention, and rollback. No TUI imports. |

See `docs/architecture/compositing-engine.md`,
`docs/reference/surface-catalog.md`, and `docs/guides/component-authoring.md`
for the implemented shell/component architecture.

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
| `focus.go`, `focus_stack.go` | Shell-owned focus target helpers and LIFO |
| `session_selection.go` | Shell-selected session/model and owner-fenced settings results |
| `layout.go` | Layout calculations |
| `cancel.go` | Cancel handler |
| `popupmouse.go` | Popup mouse dispatch |
| `slash_commands.go` | Slash command parsing |

### `workspace/` (repo root) — Business state machine
| File | Role |
|---|---|
| `workspace.go` | Workspace struct (composition root: managers + references) |
| `foundation.go` | Mode/PaneType enums, Logger, neutral single-sink EventBus |
| `agent_manager.go` | AgentNode/AgentManager (tree + flat index) |
| `session.go` | Exact session backend, references, settings and usage projection |
| `agent_state.go`, `agent_effects.go` | Callback-free ApplyEvent reducer and neutral effects |
| `agent_status_events.go` | Accepted-event status reduction |
| `agent_state_events.go` | Tool handling sub-methods on AgentState |
| `conversation.go` | Canonical live message store (Conversation/Message) |
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

### `prompt/` — Model-neutral prompt and guidance policy
| File | Role |
|---|---|
| `build.go` | Canonical root/child prompt assembly and escaped profile/identity overlays |
| `contracts.go` | Shared behavioral, tool, planning, memory, and project-guidance contracts |
| `compaction.go` | Compaction stages, extraction requests, source fragments, manifests, and checkpoint envelope |
| `environment.go` | Stable environment projection and XML escaping |
| `guidance.go` | Complete `AGENTS.md`/`CLAUDE.md`/`SOUL.md` discovery, precedence, hashing, and bounded source representation |
| `guidance_compile.go` | Oversized-guidance compiler/verifier requests and compiled projection |
| `memory.go` | Memory extraction/consolidation instructions and prompt data envelopes |
| `permission.go` | No-tools permission-review policy and narrowing overlay |
| `runtime.go` | Turn context, continuity, plan, memory recall, delegation, and wake projections |

---

## Rendering Architecture (stable — no planned changes)

### Core/shell extraction (in progress)

The product CLI now attaches independent TUI processes to one per-user core via
typed RPC. Closing a shell detaches it rather than closing sessions or the retained
workspace; server mode retains work with zero shells. Follow
`docs/plans/core-shell-separation.md` for remaining native-platform and release
acceptance limits. Never attach multiple Apps to one mutable Workspace.

- `App` owns selected session, model display alias and focus; `Workspace` has no
  active session. Lifecycle, backend lookup and hydration require explicit session
  IDs. `NewAgentSession`/`OpenAgentSession` return identities without selecting a
  shell; opening an already-live session reuses its backend.
- `App.AgentView` owns selected agent and exact-node tab visibility. AgentManager
  owns inventory only; no shared Hidden/active field may return. Width-dependent
  thinking panes are reversible shell projections of the canonical thought, not
  another Conversation or a core ThinkingPaneID. Each owner uses its own retained
  geometry and session latch; promotion never rewrites shared transcript content.
- `EventBus` accepts a neutral `MessageSink`; the TUI owns the concrete program
  adapter. It remains synchronous single-sink ingress, not a queued/replayable
  subscription stream. Its debug log is not delivery or durable evidence.
- `Workspace.ApplyAgentEvent` owns semantic inventory, conversation, prompt,
  status and request-keyed usage reduction; `AgentState.ApplyEvent` returns neutral
  effects. Root registration and cold hydration install retained canonical state,
  never a one-shot client tail. Snapshot messages detach nested attribution and
  references. Core actors now own semantic ingress/ticks; connected shells install
  immutable deltas on Update and present accepted effects without reducing again.
  `workspace/` must not import Bubble Tea or TUI rendering.
- `lspclient/` owns the nonvisual LSP transport. Workspace installs its default
  runtime without TUI construction. Shared documents own didOpen/didChange/save;
  exact per-client views release only their own LSP interest. Detach never stops a
  peer's language server or replaces canonical text with shell-provided bytes.
- The new `core/` path owns retained workspace actors, atomic snapshot/subscription
  admission, bounded resource workers, durable mutation receipts and private framed
  client transport. `coreclient.WorkspaceView` is a detached read mirror, never a
  fake Workspace containing runtime services. `shellport/remote` is the only CLI
  runtime path; `shellport.Local` remains core/test infrastructure. Read
  `docs/reference/core-client-protocol.md` before extending it. `--ssh` selects a
  strictly host-key-verified system-SSH connection to the fixed stdio bridge;
  remote paths stay in frames. Ten real Linux shells, twenty retained workspaces
  and loopback SSH have measured acceptance, not physically unlimited/efficient
  resources or distinct-host/native-Windows acceptance.
- Retained core creation hydrates Notices and initializes capability metadata on
  its factory worker before actor publication, never from shell startup or a
  model session. Passive capability observations bypass long settings operations;
  editable settings reads retain ordered preimages. Explicit refresh sequence plus
  workspace generation fences stale cancellations. Terminal create receipts mount
  exact returned identity before inventory; Notices→Terminals must repaint.
- `workspace.TerminalService` now owns the one `terminalvt` parser and reply source
  before PTY reads start in the retained core. `shellterm`
  consumes immutable rows and typed asynchronous intents; `termview` owns local
  scroll/selection/wrapping-aware copy. Never restore a second shell parser or raw
  reply-capable output feed. Input/resize use exact controller leases and ordered
  queues; OSC clipboard text is an explicit controller-local copy offer. Placement
  is shell-local. Detach revokes authority, not the PTY.
- `workspace.DocumentService` and the editor's shared-document adapter provide
  revision-fenced live text, safe pending edits and author-specific undo. The
  connected product editors use that authority for every open/edit/undo/save;
  shell PieceTables are optimistic projections with private cursors and no source
  watcher. SQLite current-format checkpoints plus revision-fenced operation deltas
  and exact receipts commit before accepted edits; cold recovery is passive.
  Stable document IDs survive durable file/directory relocation. Core file and
  document watchers publish invalidations; shell reads/transfers use typed RPC.
  Lost edit/save/format outcomes recover their original receipt, never replay an
  effect. Native conflict decisions default to Keep draft; explicit Use shared
  backs up private work and never overwrites collaborators. `document.snapshot_save`
  is checked disk infrastructure, not another live-text authority. `clientstate`
  persists private composers and unresolved editor evidence with profile locks,
  epochs and CAS; old data requires backed-up manual alignment outside product
  code. Prompt history has one core writer. Invocation account/model/provider/key
  selectors are captured per new/cold session, not application defaults; keys are
  write-only ephemeral and `--system` extends the canonical profile overlay.

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
    ├── sidebar    shelltree.Screen     — active when Tree.Visible
    ├── termDock   retained workspace bottom panel
    └── mainPanes  recursive Window-owned tab groups + SplitDividers
        └── group  own shelltabs strip + selected retained view
            ├── editor / chat / git review / empty pane / workspace home
            ├── input  exact session-family composer (one mounted owner)
            └── deck   exact session-family deckMount → retained deckHost
```

Rules:

- `View()` = bg fill → re-mount `mainSurf` → `configureShell()` (geometry
  via `computeContentLayout` + slot rects + screen projections) →
  `renderShellToSurface()` (unconditional `MarkLayout` + `Layout` → focus sync
  → `Paint`) → WM render → composite. `syncRootFocus()` maps `focusTarget()`
  to the root host's single focused component after the current content is
  registered, before its first frame paints.
- `renderShellToSurface` fills `mainSurf` with the theme background BEFORE
  the tree paints: unmounted slots (launcher replacing the editor, chat
  replacing the launcher, ...) must not leave their previous opaque cells
  behind — every cell not covered by an active slot shows the clean
  background (this fixed the editor→launcher gutter artifacts).
- Mouse targeting is `rootHost.Handle` with raw screen coordinates; the
  Host computes `LocalX/LocalY` from layout geometry. `classifyShellRegion`
  remains only for viewmodel POLICY (focus-stack pushes, status row,
  context menus). Host pointer capture is the ONE capture service — hidden
  slots cancel capture via `Host.Layout` `validatePointerCapture`.
- **Main-tab actions return keyboard focus to content.** Pointer selection,
  close and overflow browsing never retain `FocusTabBar`; only deliberate
  keyboard strip navigation keeps its heavy baseline until committed/exited.
  Release a close action before a dirty-close modal captures return focus,
  never at a late save acknowledgement. Background tab sync/completion must
  preserve explicit focus. Guarded by `tab_action_focus_test.go`.
- Editor keys route through viewmodel; pointers enter retained
  `shelleditor.Screen` with Host-local coordinates and capture. Deck panels keep their per-session
  `deckHost`; `deckMount` adapts paint (blit) and input (local coords).
- **Main panes have one selection/topology authority:** `window.Window` owns
  the recursive split tree, tab membership and selected tab per group. Moving
  tabs never clones resources. `mainPaneLayout` retains geometry/screens, not
  another active-view model. Every view has unique mounted component identity;
  focused-screen aliases are only consumers. Per-main-agent inline embedding
  registries isolate nested geometry/scroll. A family shares exactly one draft
  and deck; rebind all visible composers before pointer dispatch, never activate
  another session from Paint. Main tabs opt into shared drag/drop + reserved +;
  previews are theme-colored, non-mutating until release. Small grids retain the
  tree and show the focused group, with keyboard/header-menu navigation. Layout
  is process-local and resets on workspace switch, not persisted on resume.
  See `docs/reference/main-view-panes.md`; floating main-view pop-outs remain
  separate future work, not an excuse to bypass modal settings/decision policy.
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
  caps the full composer at one third of its available column, with the text
  viewport growing from one to at most six rows. The remaining footer/deck/tab
  clamp still applies on tiny grids (one chat row minimum). The old layer
  surfaces masked oversized inputs with the status surface's higher z-order; the root host
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
- **Workspace home preserves its original action controls.** On wide grids New
  Session keeps the full-width four-row outline with the original cycling icon,
  project path and Enter hint. Beneath it, the folder-icon New Workspace control
  returns to the left pane; after one blank row, Skills and MCP split all remaining
  left-pane height equally while Recent workspaces stays in the right pane. On
  cramped grids the two action controls stack with one blank row, the capability
  pane hides, and Recent workspaces becomes full width. Do not replace this with
  a full-width resource grid or add a repeated workspace heading or duplicate
  Settings/Help/palette/session controls. Search always owns a separate row.
  Workspace names and paths share a two-row hit target with a non-interactive
  blank separator between entries. The Recent-workspaces guide is pinned to the
  pane bottom regardless of resource count. Retry lives beside the heading,
  never inside the search row.
  Named Skills distinguish enabled modes from live Loaded bindings; MCP shows
  actual connected or attention states, never cached discovery as connected.
  Runtime observation is cancellable and off-UI, with no transport startup or
  skill-file scan. Loading uses `ui.ProgressRule` ink motion and keeps the last
  usable listing; settled home stops fast ticks. Delete stays in the workspace
  context menu, previews saved-data consequences, and moves data to recoverable
  storage with Undo; project files are untouched. See the workspace-home section
  of `docs/reference/interaction-matrix.md`.
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
  chat renderer setters or conversation export. `configurePaneChat` is the sole writer of
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

### Background Command Termination

- The background-jobs session deck opens passively; each running row has a
  native Terminate button. Explicit button interaction retains keyboard focus;
  Esc returns it to the composer. Finished rows have no termination target.
- Termination captures the exact owning backend and the observed
  `fileio.AsyncCmdRef` (handle, PID, start time), never the active session or a
  process-name search. Replaced, missing and settled references fail closed.
  Stopping one command must leave neighboring commands and the session usable.
- Command-tree lifetime owns all signals. Unix observes exit without reaping,
  cleans its process group while the leader still pins the identity, then reaps;
  Windows terminates its retained Job Object, never `taskkill /PID`. Completion,
  explicit stop and session shutdown cannot signal a settled/recycled identity.
- Background-job columns are content-sized with terminal-width helpers.
  Optional PID/exit telemetry yields before handles are shortened; elapsed labels
  use whole seconds (`2m 05s`, `1h 03m 09s`), never fractional seconds.
- Noninteractive Windows commands use `CREATE_NO_WINDOW` plus `HideWindow`;
  package managers such as WinGet must never attach to or paint into Crude's host
  console. Stdout/stderr is visible only through the captured tool/job result.
- Every session publishes one stable session-owned cross-platform
  `<temporary_directory>` and exports it as `CRUDE_TEMP_DIR` to commands. Use it
  for noisy logs and transient artifacts. `read_async_output` returns bounded
  raw-byte pages (32 KiB default, 64 KiB maximum) with an explicit next/total
  cursor; large completed synchronous output is a bounded head/tail excerpt plus
  a retained handle. Never restore an unbounded model-visible output read.

### Agent Tab Close Is Minimize, Not Terminate (since 2026-08-13)

Closing an agent tab **minimizes** it: the shell's `AgentView` hides it, the window
tab goes away, and nothing else changes. The orchestrator keeps streaming,
the conversation, panes, chat projections, scroll offsets, and session
ownership are all retained. A stray click must never destroy work in flight.

- `App.MinimizeAgent` / `RestoreAgent` change only that shell's exact-node
  visibility map; `AgentView.IsHidden()` also checks
  ancestors. Minimizing a root hides its subtree without overwriting archived
  child choices. Restoring it returns previously open children, while completed
  review children remain closed. Explicitly opening a nested child reveals its
  ancestor path and leaves archived siblings closed.
- `AgentTabViews()` filters effectively hidden nodes, so `syncAgentWindowTabs` cannot
  resurrect a minimized tab, and `purgeStaleAgentTabs` closes one that
  lingers.
- Initial tabs for each session put its main agent before visible descendants.
  Background sync preserves deliberate user order and group selections; new
  children belong with their parent rather than another focused empty pane.
  Resumed completed children are
  retained in the Agents panel as closed review surfaces, never mass-opened.
- **`AgentMgr.Count()` vs `AgentView.VisibleCount()`**: shell gates choosing between
  launcher / chat / editor must use the latter, otherwise a workspace
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

### Settings Design Language

- Every Application/Workspace Settings page follows
  `docs/reference/settings-design.md`: the surface paints one three-row header
  (title/breadcrumb, subtitle), fields are one row with a breathing row between
  them, the hovered (else focused) field's help reads in the empty row above the
  footer rule, and the shared one-row footer owns page notices, unsaved status
  and the buttons — `[ Close ]` always rightmost, preceded by `[ Save ]`
  or by an editor's decisions (primary first, Back last). Catalogs never paint their own titles or help strips;
  rail rows mark unsaved pages; rail and search share one navigation path.
- Controls: every box starts on one value column and shares one width; rows are
  never filled (focus = accent edge + label). Compact `‹ On ›` boxes for booleans (never
  `●`/`○`), segment pills for short choices, `‹ value ›` steppers over naturally
  ordered ladders whose ends hold, short number boxes, name/value tables for
  lists. Never put units in labels, raw JSON in ordinary fields, or a per-page
  footer/label dialect.
- Skills/MCP search live after a typing pause over the shared `ProgressRule`;
  a newer live search supersedes an in-flight one. Language Servers is a
  list → detail page; `LSPSettings.Languages` is the only server authority.

### Agent Settings and Session Root Selection

- Accounts and Agents catalogs share a full-width search and
  bottom-anchored actions; narrow screens stack action rows below results.
  Main Agent remains available as a fallback. Built-in roles, Memory Agent,
  and Auto Review Agent
  can be edited or reset; named agents can be cloned, enabled/disabled, or
  removed. Memory's background jobs are no-tools completions: its account,
  model, instructions and enable flag live in Agents, while job cadence and
  recall limits stay on the Memory page.
- **Accounts own complete connections.** There is no Providers page or persisted
  provider-template map. `provider.Account.Connection` owns endpoint, auth,
  headers, wire protocol/options, dialer and extra body directly. Start from
  copies an embedded preset into a new draft only; Clone creates a new ID with
  detached connection bytes and no credentials. Never reintroduce override
  toggles or live template inheritance. Account IDs, model pins and provenance
  survive explicit development-data alignment outside product code.
- **Operational startup failures never lock out repair.** Missing/invalid account,
  config, network or session startup opens a usable shell plus a native recovery
  dialog with exact reason, guidance and explicit repair. Invalid network policy
  stays fail-closed; no default account/direct connection/reset is inferred.
  Notices are independently recorded but optional. Failed New Session/Resume
  must not paint error text under the launcher action or instruct users to check
  Notices for the reason. Close/Esc dismisses without replay or draft loss.
- Auto Review Agent is the bounded, no-tools permission reviewer. Every root and
  child gets an independent reviewer instance, per-agent-turn budget and audit
  attribution while sharing the persisted application/workspace policy. The
  owning session's live YOLO state bypasses review and confirmation for that
  root's complete descendant tree without affecting another root/session. Its
  enable switch, explicitly pinned account/model, extra restrictions and
  complete runtime policy live on the Agents Model / Instructions / Review
  tabs. Security > Auto Review Policy is a synchronized projection of that
  same persisted authority, not a second configuration owner. Thinking effort,
  response format, timeout, initial/retry token budgets, attempt count/growth,
  input bytes and denial behavior must never become hidden runtime constants.
  There is no lifetime or per-turn review-count cap: every exact non-hard-blocked
  operation remains eligible throughout a multi-day turn. Technical retries
  alone consume the configured attempt/token budget; valid
  allow/deny/ask decisions are never retried. An unresolved-mode choice may ask
  a human or deny the exact call so unattended work continues, but can never
  approve uncertainty, secret/oversized input or exhausted recovery. Unknown,
  network, destructive and shell-composed operations are reviewer evidence, not
  structural exclusions; immutable hard blocks still run before review. Security
  keeps Command Safety separate. Never add toggles for hard blocks, human-only
  grants, or mandatory restore safeguards; explain those constraints beside the
  controls they govern. A rejected reviewer result never becomes an Always
  allow grant.
- `ui.FormField` paints multiline labels (textarea and key/value grid) on the
  containing surface even on hover/focus. Single-line labels may use the row
  highlight; multiline editor bodies retain their own fill and interaction.
- Agent model discovery must refresh retained dropdown items AND geometry on
  the first result, without rebuilding the draft or closing its open selector.
  Fill inherited capabilities without replacing manual overrides. Preserve the
  Model / Instructions / Tool Access presentation and Main Agent's Multi Agent
  delegation toggle; do not replace the model selector with a free-text redesign.
- Breadcrumb labels are separate navigation targets measured with shared
  terminal widths. Settings parents use existing back/discard ownership,
  session parents preserve staged edits, and file-tree parents reveal their
  row. Separators never navigate.
- `/settings` persists a session-only `SessionMainAgent`. A named profile or
  built-in role resolves the root connection/model/instructions at the next
  request boundary; its account pin lives under a distinct `root/<choice>`
  session override. Never collapse those overrides into workspace Main Agent
  or silently fall back if a selected agent is removed or disabled. The root
  selector excludes Memory. Save as Workspace must not promote this session-only
  choice into workspace defaults.
  Default agent uses the same compact navigation row and filtered picker page
  as Account/Model, never an inline dropdown or a different chevron dialect.

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
- **UI shutdown owns UI resources.** `App.StopAsyncIO` flushes drafts, rejects
  new tree-watcher starts, joins accepted startup work, then closes the tree and
  every distinct editor watcher. It is idempotent. Closing `Workspace` cannot
  release TUI-owned editors; real-shell test fixtures must invoke the same UI
  teardown and standalone editor fixtures must register `Editor.Close`. Leaked
  fsnotify instances exhaust Linux inotify limits and make later watcher tests
  fail with EMFILE even though they pass in isolation; never hide that with
  retries, sleeps, skipped assertions or raised OS limits.
- **Collaboration shares the current workspace database lifetime.** Prepare the
  target collaboration attachment before switch teardown. Detach old sessions and
  release their store's runtime locks before closing its borrowed SQL handle;
  publish the new store with the target DB. Never carry a non-nil old store through
  `SwitchProject`. Verify launcher agent registration and passive resume, not just
  backend session construction.
- **The header Git identity reconciles itself from watcher events.** The
  branch, changed-file count, and tracking on the header segment come from the
  Git panel snapshot, so that snapshot must not depend on the user opening
  the Git dock panel: file-watcher and repository-metadata events arm a
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
- **Account timeouts are one shared transport policy.** Accounts
  expose Request/Connect timeout, initial/minimum/maximum progress timeout, and
  attempt/backoff budgets directly in Connection tuning. There is no template
  inheritance or Override dialer switch. Progress defaults
  are 60/45/300 seconds and require minimum <= initial <= maximum. Streaming and
  non-streaming requests honor the overall deadline across retries, even when
  the caller already has a longer deadline; close/completion/error releases the
  stream timer. Unlimited overall duration does not disable progress detection.
  An explicit provider/account connect setting also owns its TLS handshake
  budget; unset values inherit the separate Network TCP/TLS defaults. Neither
  is a competing response-header timeout.

### Unfinished run recovery and network transport
- `agent.Supervisor` is the one root/child unfinished-run lifecycle. Request
  exhaustion, deadlines, truncation, loop guards, missing reports and panics are
  not natural completion or permission to kill a task. Recover transient outages;
  park configuration, guidance, storage and uncertain-effect failures with an
  actionable Needs attention state. Only natural finish or explicit user/parent
  lifecycle may settle work. Preserve safe canonical boundaries; never replay
  accepted stochastic output, historical tool effects or existing commands.
- `sessiondb` retains exact pending input, owner/generation, archive and active
  context through one durable recovery checkpoint. Transcript, turn ledger and
  collaboration settlement must succeed before acknowledging natural completion.
  Finished parallel leaves checkpoint while siblings run, including native batches;
  completed results receive their stable message IDs before their first checkpoint.
  Evolving native-batch evidence lives in `RecoveryCheckpoint.InFlightTools`, not
  the immutable active-message prefix. Completion clears only its exact evidence;
  passive interruption preserves finished leaves and marks the remainder unknown.
  missing crash outcomes are unknown, never fabricated user cancellations.
  Preflight-blocked inputs keep their DB even without transcript rows. Pending
  children restore as actionable review surfaces, not completed or invisible work.
  Resume remains passive; explicit
  Continue run or `/retry` fences the exact incident/generation and must not
  consume drafts, switch another tab or restart old effects. Stop must also work
  for parked/interrupted work without an active collaboration run.
- `sessiondb.PublishContext` is the revision-fenced runtime publication owner.
  Root writers serialize durable publication and live installation under
  `msgsMu`; checkpoints retain every committed source message, finalization
  cannot roll back a newer admission, and complete tool rounds never regress
  partial checkpoints. No root write buffer or child whole-transcript replacement
  writer remains. Original input, successful immutable preparation and the
  initial supervisor checkpoint commit before a receipt. Closing rejects new
  reservations and preserves prepared input without enqueueing execution.
  Stopped children use current authoritative context and retain newer facts;
  parent mirrors/results are separately checked, never silently acknowledged.
  Root and child checkpoints both reconcile exact late lifecycle controls.
  Runtime-note receipts commit before one complete batch checkpoint, never after
  checkpointing reserved notes that a failed admission would try to remove.
- `publish_report` is the mandatory child return channel, not an optional task-tool
  allowlist member. Explicit denial is rejected before launch; schema projection,
  dispatch and prompt permissions agree. Missing reporting infrastructure parks as
  configuration failure before provider execution, never a model reminder loop.
- Native file-ownership rejection is structural Needs attention and parks the
  unfinished child. Public `agent_narrow_claims` changes the caller's exact active
  claims; `agent_update_claims` changes only a stopped unfinished child at
  the expected claim version. Neither wakes execution. Explicit incident-fenced
  Continue starts a new run. Numeric preimages are exact positive integers,
  widening and foreign/stale owners fail closed, and ownership read leases span
  actual native effects so claim replacement cannot overtake an in-flight write.
- `retry/` owns every Crude-managed automatic cooldown. Exponential math
  saturates safely and cancellation is prompt; **five minutes is the absolute
  per-wait maximum**, including Retry-After and millisecond/date hints. A server's
  24-hour hint must never cause an hours-long local retry sleep. Request/job
  attempt budgets and successful maintenance cadence remain separate.
- Shared HTTP recovery uses status evidence, not generic error-envelope labels:
  `invalid_request_error` must not turn 408/429/5xx into a permanent settings
  fault. Specific auth/quota/validation evidence and explicit retry vetoes remain
  non-retryable. Test every registered protocol plus real root/child timer expiry;
  an SSE-only adapter test does not cover a gateway's HTTP JSON rejection.
- Established-stream errors use the shared `provider.streamErrorDecoder` for
  Chat Completions, Responses/Codex, Anthropic SSE and Ollama NDJSON. A gateway
  may translate an upstream disconnect into any of these wires, including a
  string error. Recognize narrow typed/observed disconnect evidence at the stream
  boundary, not arbitrary diagnostic prose in global retry policy. Preserve
  auth/quota/validation and retry vetoes; accepted output requires semantic
  continuation, never stochastic replay or execution of partial tool arguments.
- Recovery uses one compact, expandable incident row. Automatic transport/DNS
  recovery needs no `/retry`; a real countdown and exact-owner `(Retry now)`
  action can shorten its cooldown. A red square blinks on the shared activity
  clock only during recovery, with stable measured width. Expansion shows the
  full neutral diagnostic and muted guidance without rewrapping on clock ticks.
  Waiting for attention never animates fake work. Live and hydrated projection,
  copy/selection, timestamps and explicit cancellation must agree.
- A deadline-free recovering acknowledgement is an active replacement attempt,
  not another cooldown. Its retained incident must never overwrite subsequent
  upload/wait/thinking/response phases during inventory or activity projection.
  Paint it as a static, muted Run resumed boundary, not a blinking red Recovering
  row above tools already executing; keep its unfinished checkpoint and evidence.
  Resolve the live attention issue on acknowledgement, retain Notices history,
  remove duplicate Continue actions, and let explicit Stop outrank the incident.
- `network/` owns the shared outbound HTTP transport for providers, OAuth,
  catalogs, web tools, capability downloads and MCP HTTP/OAuth. Network settings
  are application-only Connection / Proxy / Certificates pages; workspaces
  cannot grant trust or client credentials. Secure system trust, exact-host leaf
  exceptions and exact-host mTLS stay isolated from proxies/unrelated origins.
  Proxy failure never falls back direct, and invalid material fails closed.
  Certificate reads and preference persistence run off Update with retained
  form/workspace ownership; an unchanged request keeps its captured policy.
  See `docs/reference/run-recovery.md` and `docs/reference/network.md` for the
  authoritative contracts, coverage and explicit limits.

### Editor motion
- **`Editor.moveWordLeft` must keep climbing across blank lines**: word-left
  from a line start must skip whitespace-only lines and land on the previous
  line's last word start (guarded by `TestMoveWordLeft_CrossesLines`). A
  b323a3b rewrite once replaced the climb loop's `continue` with `return`,
  parking the cursor on blank lines — and SA4004 flagged it as a dead loop.
  When a "dead loop" warning appears, check for a swallowed `continue` first.

### ANSI & Width
- **Full process:** see [`TERMINAL_WIDTH.md`](./docs/reference/terminal-width.md).
- **Markdown keeps its established hierarchy:** use Glamour's differentiated
  headings, links and inline code, with theme-bound document/fenced-code ink and
  a readable light palette. Never force `StyleConfig.Text.Color` globally; it
  overrides inherited heading colours. Native bridges tokenize through the
  shared ANSI decoder, suppress OSC/control payloads, and carry SGR state across
  a rendered block (`BridgeAnsiLines`), not only within individual rows.
  `rendering.MarkdownRenderer` is Glamour's ANSI renderer over goldmark with
  only the raw-HTML parsers removed: chat text is not HTML, so `<context>` or
  `Vec<T>` stays literal instead of being sanitized away. Never add an escaping
  pre-pass or restore `glamour.NewTermRenderer` for chat.
- **Child assignments display typed fields, never their envelope.** Providers
  receive `prompt.BuildChild`'s escaped `<delegated_assignment>`; live events,
  persisted snapshots and recovery checkpoints carry `childAssignmentDisplay`
  (description + verbatim instructions as Markdown). Never parse the envelope
  or model-authored structure back out of text.
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
  passes a wrapper that installs `lspPipeMonitor` (`lspclient/lsp_pipe.go`).
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
- Dirty means byte identity, not revision identity. `Buffer` retains an immutable
  piece-span savepoint; normal edits, undo/redo and save acknowledgements use the
  same exact comparison. Never hash/export the whole file per keystroke or let
  a late acknowledgement mark identical newer text dirty.
- The gutter has one exact live HEAD-to-buffer projection. Immutable piece
  snapshots bound each changed line window, which is re-diffed through shared
  `textdiff` logic. Paint, deletion markers, inspection and navigation all use
  those hunks immediately; never reintroduce paint-only local claims. Same-HEAD
  async refreshes update blame without replacing the live projection.
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
- Atomic publication has one shared low-level owner: `fileio.CommitAtomicFile`.
  Local History stages and syncs its own files, then delegates replacement and
  platform durability. Unix keeps rename plus parent-directory fsync; Windows
  uses write-through replacement, never `Sync` on a read-only directory handle
  or a delete-then-rename fallback. Real permission/write/commit errors propagate
  and still abort tracked turns before provider or tool execution.
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
- Composer height is a viewport, never the draft's total row count: grow from
  one to six text rows, apply the host's tighter one-third-column budget on short
  grids, and scroll internally without trimming text. Paint a quiet visible-row
  range when clipped. Unchanged layout must not reset wheel scroll; cursor
  navigation/edits and real geometry changes alone reveal the caret.
- Composer pastes attach at 4 KiB, or eight logical lines with at least 512 bytes
  of trimmed payload, or ten wrapped rows with at least 1 KiB of trimmed payload.
  Wrapped checks use the actual text column with a 40-column evaluation floor so
  a tiny terminal cannot promote a little text. Short prose and small lists stay
  literal; typing/resizing/restoring a draft never promotes it automatically.
  Bracketed paste and image-first Ctrl+V share this policy.
  Persist large pastes off Update, preserve gesture order, block Send until
  persistence completes, and fence results to session/agent/workspace/draft.
  A failed or unbound store never falls back to dumping the wall into the input.
  Inline and file-backed pasted text share Pasted Text labels and native
  click-to-view/edit; keep the composer mounted underneath. Reference loads and
  saves are off-UI, complete within the 64 MiB read bound, epoch/preimage-checked,
  and keep edits visible on failure. Projection locks never cover attachment I/O.
- Attachment paint and hit testing share `ReferenceChips` geometry. Filename
  bodies open; separate three-cell remove targets only detach the draft.
  External opens run off the UI thread through `fileio.OpenDesktopPath` (native
  Windows associations, macOS `open`, Linux `xdg-open`). History media is copied
  into the session before external opening, so viewers cannot edit snapshots.
- Prompt history entries retain exact chip spans and immutable attachment
  snapshots, not session-local labels alone. `history.Store` is the sole recall
  source; input emits owner/generation-keyed recall events, and
  `workspace.ReferenceStore.RestorePrompt` imports session-owned media off the UI
  thread. A late result never overwrites a newer draft. Missing media fails
  closed; history eviction must not delete a session's restored attachments.
- Composer session activation owns reference binding and draft restoration,
  never Paint. Warm sessions retain their complete editor; cold loads are
  activation/revision-fenced and cannot overwrite explicit history recall.
  Bind the destination store before recalled IDs, preserve literal bracketed
  prose, flush inactive queued drafts, and never let a stale paste completion
  consume another session's numeric paste ID or release its Send barrier.
- Permission, question and tool-projection lifecycle events never own the chat
  composer. Opening or completing a blocking deck/overlay may move focus but
  must preserve the exact draft text, attachment references, cursor, selection,
  undo state and history position. Only explicit send/clear/cut actions consume
  those values; `blocking_complete` must never reset the input model.
- Submitted-input receipts also fence draft/editor state, owner epoch, generation
  and workspace. Cursor, selection, scroll, references and undo/edit ABA changes
  protect a newer draft from consumption. Stop preserves accepted input in chat
  and context, never in the composer. Exact-owner late semantic evidence remains
  static after terminal acknowledgement; only a new runtime generation reactivates
  work. See `docs/reference/interaction-matrix.md`.
- Workspace and Open Tabs collapsed resource rows occupy exactly one line.
  Keep explicit Workspace section headings even for a single group. Click,
  Enter/Space, and Left/Right own disclosure; Open/Resume is an explicit action
  inside expanded details, not an ambiguous first click.
- `ui.TextEdit`, `ui.TextField`, and `ui.TextArea` own the non-document editing
  contract: selection replacement, undo/redo, word/document/page movement,
  Ctrl/Super shortcuts, copy/cut, bracketed paste, owner-keyed async Ctrl+V,
  multi-click selection, and captured outside drag with autoscroll. Add behavior
  there, never to one popup.
- TextArea cursor geometry and paint share one logical-to-wrapped-segment mapping.
  A cursor inside continuation whitespace omitted by WordWrap maps to the next
  segment's column zero without changing the draft or cursor offset; never slice
  from a visible segment start past a logical cursor in that omitted gap.
- `Form` is a composite pointer owner because its retained fields are not Host
  children. Once an inner input starts a drag, `Form.pointerField` must route
  every captured motion/release to that same field before hover-focusing.
- Child bounds stay parent-local during paint as well as layout. A FormField
  passes its own origin unchanged to TextArea; pre-adding the textarea's Y=1
  shifts its background into the description row.
- Source-code projections expand tabs at tab stops before width, wrapping,
  syntax coloring, and paint. Raw tab cells have no stable terminal geometry.
- Editor and comparison syntax share `rendering.SourceStyle`; never add a second
  token-to-theme map. Git/local diffs use one `screens/diffview` reader with
  source-aligned rows, native draggable split, one vertical scroll owner and
  exact source copy. Local comparisons are immutable, generation-fenced main
  tabs; Refresh is explicit and closing never consumes the source draft.
- Text drag selection uses theme `TextSelection`/`TextSelectionFg`, distinct from
  list/chip Selection. Apply readable ink across input/chat/editor/diff/terminal
  consumers. Programmatic editor jumps reveal only when necessary after actual
  viewport geometry; landing highlights are not destructive edit selections.

### Activity-dot animation
- `ui.ActivityDot(spinFrame)` is the only product activity-dot animation:
  synchronized Unix-millisecond clock, `●` for 400ms then `○` for 400ms.
- Every active fixed-strip page uses `ui.TextTide`, a paint-only ink sweep that
  fades off both ends before wrapping in forward mode. `ui.TextTideMotion` keeps
  the crest in cell columns on the shared clock, so text growth and resize do
  not rephase a visible sweep. Entry and exit share its travel speed; the full
  highlight clears the last character before reentering from the left.
  Explicit reasoning kind and semantic blocks come from provider wire contracts.
  Flattened unlabelled text gets a neutral provisional strip preview and
  expandable full train, not a guessed
  raw pane or confirmed summary. A strong provider-reported usage discrepancy
  can settle the accepted request and, with repeated connection-scoped evidence,
  calibrate subsequent streams. Explicit wire raw always keeps its pane. Never
  classify by model name or infer from current-request usage before it arrives.
- Active collapsed compact thoughts paint no stray transcript header. The fixed
  strip owns a clickable chevron and the normal disclosure shortcut reveals the
  complete retained trace in the transcript. `ColorActivityText` blends muted
  ink 30% toward the theme foreground, keeping the strip distinct from metadata.
  Provisional thinking tides reversibly; a confirmed summary tides left-to-right
  through the remaining active turn for that serving
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
  history and compaction boundaries remain visible. Ordinary stopped boundaries
  and cancelled recovery notices remain canonical/context evidence but contribute
  no chat rows, spacers, grouping boundaries or height. One quiet `Stopped` strip
  remains; interrupted tools and genuine actionable recovery keep their evidence.
  Memory-summary titles
  load through session-owned cancellable jobs, never from a screen or Update.
- The strip prioritizes upload bytes/percent, provider waiting, retry and required
  action over optional rotation. Tidal motion applies to every active strip
  state; provisional and summarized thinking get the compact preview. Do not
  echo the user prompt there. Ordinary planned activity is only `a/n Task name`;
  no Goal/Next/progress-count pages or call-count suffix. Thinking can share or
  rotate with that compact task. Stopping, cancelled, completed and idle strips
  are static; cancellation says only `Stopped`. Stopping never changes the
  persisted plan lifecycle.
  The session's one `plan.Store` publishes its committed immutable activity view;
  the strip never becomes another plan owner. Hovering the strip inspects all
  ordered steps in a bounded, scrollable passive WindowFrame popover, owned by
  that exact pane without taking composer focus. Leave/Esc/outside/owner removal
  dismiss it. `Latest` is a centered native button in the existing reserved gap
  while scrolled up; it adds no transcript height and re-arms exact-pane follow.
- Plans are clean desired snapshots, not execution logs. Omitted obsolete steps
  remain only in immutable revision history. Both plan tool interfaces expose
  `replace=true` to atomically supersede a changed goal and reset step provenance;
  neither silently appends cancelled steps. Prompts require latest-intent review,
  updates at milestones and honest closure before the final response. Explicit
  revision fences and transaction rollback preserve history and current activity.
- Command safety assessments belong to owner-scoped Notices, not retained
  `AgentState` alerts or live `NeedsAction` status. Only pending permissions and
  questions need input. Completed, cancelled and failed turns keep their own
  phase while safety notification history stays unread until acknowledged.
- Active chat lifecycle/thinking/tool/sub-agent/session rows use it with one
  stable semantic color. Never add local speed formulas, brightness/color
  pulses, braille frames, or `AttrBlink`; see `docs/guides/component-authoring.md`.
- Activity groups and parallel batches share one staged tool-leaf projection.
  Expansion reveals hidden branches in place; it never replaces the tree with
  standalone legacy rows. Execution events carry exact tool arguments, not
  formatted/truncated summaries. Native command JSON envelopes are decoded only
  at presentation; file edits/writes/patches stay separate and default-open.
  **Never print a raw JSON tool-argument envelope in any tool title, status,
  compact pane, or batch leaf.** `tools.SummarizeArgs` is the single semantic
  operand authority for live events, child projections, and resumed snapshots.
  Missing, malformed, nested, and unfamiliar argument shapes fail closed to a
  name-only title, not a truncated JSON fallback; a real command/query/path
  extracted from a field remains readable. The shared title painter must also
  guard raw structured progress labels, unsafe tool identifiers, and nested
  structured error reasons. Every new tool schema needs live,
  parallel (when applicable), and hydrated tests asserting the same title,
  row geometry, disclosure, and full evidence access across both paths.
  Sub-agent tool identities include allocated nickname, exact category and exact
  handle whenever those facts are available: running panes, launch outcomes,
  polls, cancellations, list entries and authored reports. A call awaiting
  allocation shows requested category + assignment; once allocated it never
  guesses a nickname from the requested role/task, or attaches a name from a
  mismatched result. Lists keep a bounded title and expose each name+handle pair
  in expanded evidence. Live, batch and hydrated rows share the same title/
  progress snapshot and must paint identically.
  Batch preview names come from closed strings in the original input, never
  JSON-repaired partial names. Unnamed children and empty live envelopes occupy
  no transcript row; named children stream their arguments immediately in the
  correct tree/standalone placement. A batch's first groupable child starts in
  its tree rather than reparenting when its siblings arrive.
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
- A settled sub-agent publishes one short owner-scoped footer transient with
  nickname, category and explicit outcome (completed, cancelled, crashed, timed
  out or failed). It enters through Update and forces the presentation deadline;
  cached output must never hide it. Anonymous status such as `✓ done` is invalid.
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
- Async output length and cursors are owner facts, not permission to read the
  bytes during continuity or compaction. Snapshotting never drains output.

### Context compaction and runtime continuity
- Wire/model/account/config switches are ordinary request boundaries. Preserve
  the canonical transcript and exact tool identities; the destination adapter
  owns schema-valid projection and only outgoing opaque replay is filtered.
  Never clear history, force a producing protocol, or add transport-level tool
  vetoes for forms an adapter can encode. Cold transition requests are expected;
  unchanged subsequent projections must preserve their warm prefix. Guard every
  registered protocol pair through resume, real tool loops and compound settings
  changes, not only empty-session protocol switches.
- Responses delta uploads use typed completed-response references in the durable
  active context, bound to exact model/account/endpoint/configuration, input and
  output. Full local history remains authoritative. Earlier handoffs stay in the
  accepted prefix; append fresh superseding facts rather than removing them.
  Never adopt partial responses, guess equivalent context, change storage/ZDR
  preferences, or retry after accepted output to recover a server reference.
  A structured rejected-reference lookup may rebase once before output within
  the existing deadline. Model/protocol/context changes require a fresh chain.
  Stream identity comes from the official Response objects on created,
  in-progress and completed events; every observed ID must agree. `store:true`
  is a request, not proof of endpoint capability. When continuation was enabled,
  an explicit returned `store:false` or missing response ID keeps full uploads
  and must surface a typed reason, never a guessed ID. Disabled continuation
  logs `disabled`, retains no new reference and emits no endpoint-limitation
  notice: expected non-storage is not capability evidence.
  HTTP continuation cannot be guaranteed for
  a gateway that declines storage; WebSocket store-false continuation and the
  Conversations resource are distinct APIs, not fallback JSON shapes.
- Responses inline images remain eligible for normal `previous_response_id`
  deltas. The default-on, provider/account-configurable media replay safeguard
  may rebase once before output only when a 400/422 identifies invalid Base64
  `image_url` replay and the inherited local image validates. Record that exact
  connection-scope limitation in `model-catalog.db` for seven days; matching
  media prefixes then use full uploads, while other connections keep probing the
  standard path. Turning the safeguard off disables recovery and learned routing.
  Generic errors, malformed local media, cancellation and accepted output never
  trigger it.
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
  four no-tools extraction stages over one immutable durable source (intent,
  work, state, continuation). Fitting sources share the cached prefix; oversized
  histories or individual tool inputs/results use complete chronological,
  origin-attributed text fragments and bounded model-backed chunk-and-reduce,
  never oldest-history deletion. Each fragment repeats its source role/origin
  and tool-result identity instead of sending orphan protocol calls.
  The retained boundary records the actual model and real chunk progress.
  Context windows are connection-scoped when explicitly configured; ordinary
  pressure reserves normal output and includes runtime notices before admission.
  Every extraction request plans against its own model's available context.
  Provider-confirmed overflow retries only after checkpoint reduction; extraction
  shrinks its input budget on provider evidence. Preserve the configured thinking
  mode during extraction: hidden reasoning counts toward the response allowance,
  so checkpoint text size must never cap generation. Use the actual available
  continuation space for checkpoint size; refine complete sections if necessary.
  A truncated extraction reduces and reprocesses the complete source in smaller
  regions; never replay the identical exhausted request or install a partial
  checkpoint. Bounded genuine-input retention and continuation capacity are
  validated before installation.
  Models emit terse plain text; Crude escapes and wraps the four bodies in one
  deterministic XML checkpoint. Retry only the current stage, at most three
  recoveries; cancellation never retries. Previous-generation archival,
  checkpoint installation, and terminal operation state commit atomically.
  The exact source context is durable before extraction starts, including
  in-flight turn state. Failure, cancellation, or pre-commit crash leaves that
  source authoritative.
- Provider-reported output-parameter ceilings are distinct from context-window
  errors. A typed 400/422 naming the exact sent output token value and valid
  upper bound reduces that stage's allowance, not its source or thinking mode.
  App-wide `model-catalog.db` retains the lowest confirmed ceiling keyed by
  Aeron agent profile, provider endpoint/protocol, account, and model across
  new sessions/workspaces and restarts, expiring after seven days. Never tie
  this learned transport capacity to one session DB or retry a known-invalid
  output request for every stage. Recursive source reduction still recomputes
  the response room for each smaller request under the learned ceiling.
- Compaction presentation is one retained, operation-ID-keyed context-boundary
  table on the normal chat background with full-bleed top/bottom rules using
  the active-rail accent color. It shows
  real stage units, never fabricated completion percentages. Active, retrying,
  completed, cancelled, failed, and resumed-interrupted forms share geometry;
  generic turn cleanup must never consume these durable operation rows.
- Manual compaction is a separate exact-ID operation after a completed/stopped
  model turn, never permission to reactivate old output. Connected composers
  queue input durably with replicated pending rows; dequeue, context and recovery
  commit atomically before execution. Stop preserves the queue, cold resume is
  passive, and queue-only sessions are not empty. Core capacity/receipt checks
  precede owner preparation; shutdown cancels extraction before joining workers
  and mirrors final session metadata without reading a closed SQL handle.
- Context-boundary tables are ordinary chat content for interaction: native
  rows, full-bleed rules, and body cells all participate in drag selection,
  clipboard copy, hover/hit testing, and row context menus. Their distinct
  visual treatment must never create a second opaque interaction path.
- Message origin and visibility, not provider role or string prefixes,
  distinguish human/BTW input, runtime context, continuation controls, child
  wakes, assignments, and checkpoints. Runtime or memory data is never
  attributed as human intent.
- A compacted context ends with one fresh code-authored `runtime_context`
  handoff. It states that compaction is not a new user turn, carries the latest
  durable human-turn state, current plan, session/model/account/capability
  selection, child identities/statuses, and command-owner snapshots. Extraction
  may describe those facts but never owns them. Refresh the handoff after all
  extraction stages because commands and children may settle during compaction.
- Retain a verbatim human tail only for a request that is still pending. If a
  later terminal assistant answer already completed the latest human request,
  do not append that request after the checkpoint: doing so makes the next
  provider invocation answer it again. Older prompts remain immutable in the
  transcript and are represented by the checkpoint, not replayed as fresh input.
- Async continuity uses exact owner data: handle, command, cwd, PID, start time,
  running/terminal status, terminal exit only when known, total output bytes,
  and the last observed byte cursor. Taking a continuity snapshot must not read
  output, advance a cursor, signal a process, restart a command, or treat a
  running command as exit zero. Compaction preserves and observes existing
  handles; it never relaunches work to rediscover state.
- New-format resume is passive: restore exact per-agent active context and turn
  state, mark interrupted work, issue zero provider calls, and restart zero
  children. Existing development sessions have no compatibility path.
- Root checkpoints preserve human intent and child-result ownership; child
  checkpoints preserve assignment, policy, file ownership, evidence, and the
  `publish_report` obligation. Compaction requests never enter memory extraction.

### Prompt assembly and project guidance
- Canonical contract and development checklist:
  [`docs/reference/prompt-architecture.md`](docs/reference/prompt-architecture.md).
  Read it before changing model instruction text, request assembly, guidance
  intake, compaction, or a background completion job. Classify each change by
  owner; do not put new policy in a caller's arbitrary runtime-context string.
- `prompt/` is the sole owner of every code-authored model instruction template
  outside the exact tool schemas/results that define an execution interface:
  canonical root/child policy, runtime-capability awareness, bounded command/temp
  policy, compaction, guidance compilation, memory jobs, permission review, and
  runtime context/control projections. `agent/`,
  `session/`, and `memory/` provide typed state, select source data, validate
  results, persist outcomes, and execute requests; they do not retain prompt
  constants or forwarding prompt APIs. Tool-specific validation, denial and
  result text stays with the executing tool contract. `prompt/ownership_test.go`
  guards top-level production files in `agent/`, `app/`, `session/`, `workspace/`,
  `memory/`, and `codexio/` for prompt constants and known templates. Extend the
  guard for new paths; it is not a substitute for reviewing complete request
  assembly or a loophole for instructions hidden in differently named strings.
  GPT, Claude, Gemini, routed, and local models receive the same Crude policy.
  User-editable profile instructions are an escaped lower-priority overlay, not
  a replacement system prompt. Agent/session code may provide typed dynamic
  state but must not grow another model-family prompt fork.
- Provider and model differences belong to the narrowest real adapter: wire
  encoding/replay in `provider/`, exact offered schemas in `tools/`, and native
  interfaces such as OpenAI/Codex freeform `apply_patch` in `codexio/`. A native
  tool does not justify replacing Crude's behavioral prompt. Tool descriptions
  and schemas are authoritative for every provider.
- Before the first project request, Crude reads every byte of applicable
  `SOUL.md`, `CLAUDE.md`, and `AGENTS.md` files in deterministic order (persona,
  model guidance, repository policy; broader directories before narrower ones).
  Small sets are injected verbatim. Sets too large for their bounded share of
  the target context are sent in complete source-hashed chunks to a no-tools
  instruction compiler, then verified chunk-by-chunk against every source;
  corrections restart the bounded verification sweep. Normal work starts only
  after a stable full ledger is available. Never truncate, silently skip, or put
  an unlimited guidance blob in the ordinary request. Fail closed if complete
  intake or a fitting ledger
  cannot be established. This guarantee applies only to accepted sets: the
  aggregate source ceiling is 64 MiB, and larger sets are rejected, not partially
  consumed. Small/large refers to estimated rendered tokens within the reserved
  context budget, not a fixed file size. A task model may receive a compiled
  ledger rather than raw source; model-backed verification is not mathematical
  proof of semantic completeness. Cache only by exact source, compiler/version,
  connection, target model and budget identity; current caches are session-local,
  not durable across restart. Do not claim a context bound is also a cost bound.
- README/design/reference files are not automatic instruction cargo. Read only
  the documents or sections relevant to the current decision, including a
  reference explicitly required by applicable guidance. Do not recursively load
  every linked document. Entering a newly discovered directory scope defers the
  whole tool-call batch before effects, loads/compiles its named guidance, sends
  a superseding scoped projection, and requires the model to reassess the call.
- Guidance snapshots have deterministic source order, a format version and
  content digest, and an intake-complete notice after their bodies/ledger.
  Supplied content satisfies its own startup-read requirement. Unchanged
  rereads remain legal, never republish guidance and never rewrite an accepted
  cache prefix. Recheck known scopes before and after tool batches; changed or
  deleted sources publish one complete replacement as hidden runtime context.
  Native patch operands participate in the same guard. Model/protocol changes
  preserve source identity and use the existing budget/connection-keyed cache;
  do not add timestamp/counter churn or raw JSON tool-name rewriting.
- For changes to these contracts, verify actual root/child and background-job
  request paths, model/profile overlays, complete guidance coverage and bounds,
  zero task effects on failed intake, new-scope batch deferral, answered-request
  non-replay, current owner facts, cancellation and passive resume as relevant.
  Run `go test ./prompt`, complete affected packages, repository build/test/lint
  gates, relevant race/cross-platform checks, and `make docs-check`. Update the
  canonical reference with changed behavior and explicit remaining limits.

### Sidebar list rows: resource vs command
`ui.ListItem.Command` marks a button-like row (e.g. "New Terminal").
`activitylist` arms resource rows on the first click and activates on the
second (matching the pickers), but command rows fire on a single click — a
button that needs a double click reads as broken.

### Capabilities And Workspace Tools (2026-10-01)
- Memory and session capability selections require the current schema; there is
  no automatic compatibility upgrade. Known local data must be manually aligned
  outside product code with backups and exact preservation checks. Preserve IDs,
  assertions, provenance, lifecycle, history, transcripts and account/model pins;
  never fabricate evidence or earlier revisions. Startup and all reads/writes
  use only the current contract.
- Skills, MCP, Sources and Acquisition follow Memory in the existing AI settings
  group. Navigation and painting share the same grouped order; never append a
  second AI heading after Security or IDE.
- Rail destinations are Files, Agents, Skills and MCP; Terminals and Git have no
  rail identity. Git is the `gitpanel` bottom-dock destination (footer pill,
  `Ctrl+Alt+G`); its multi-file `gitreview` main tab and the panel preview
  share one `gitdiff.Document` and one prepared change set
  (`prepareGitChanges`, re-derived only while a Git view is visible). Each
  review is its own main tab (`gitReviews`, keyed by tab, per-instance
  component IDs); never return to a single replaced review screen. Commit
  messages are drafted by the Aeron role via `Workspace.PrepareCommitMessageWriter`
  (configuration read on the update goroutine, streaming I/O in the background). Workspace Memory is a retained footer panel shared with
  `/memory`/palette, never a second floating overlay or session PanelDeck.
- Skills/MCP settings deep-links retain the Settings frame and explicitly mark
  its Host layout. Async capability results target the owning Model directly,
  not the focused text control, which can consume unrelated custom events.
- Managed install/update and agent acquisition approval carry an immutable
  source/revision/binding preimage. A boolean approval must never widen into
  changed bytes or another source. Dynamic MCP definitions and execution both
  enforce current effect/scope/fingerprint grants and root/child ceilings.
- **Skills and MCP are application-bound by default; workspace binding is an
  explicit choice.** `capability_activate` publishes user-owned registrations into
  config.json unless the task or user asks for `scope: "workspace"`, so deleting a
  workspace never destroys an install. `mcp_request_access` grants inherit the
  connection's own binding and must never rewrite it. Downloaded/verified artifacts
  and MCP credentials live under the app data directory, outside the workspace
  store; a workspace registration is only an overlay row pointing at that
  app-global path. `types/capability.ApplyRegistration` is the single publication
  owner shared by the workspace overlay store and the app config owner.
- Agent acquisitions and exact MCP access changes publish through the normal
  settings registration maps (config.json for application scope, the sparse
  workspace overlay for workspace scope), never a second shadow install table.
  Preserve neighboring installs/preferences and fence the exact source, policy,
  selected preimage and workspace lifetime. MCP activation exposes observed
  fingerprints separately from usable tools; separate `mcp_request_access` grants require
  immutable explicit approval, never source annotations or generic Always-read
  permission. Skill activation includes its installed path. Passive restart
  reconnects/downloads/replays nothing; explicit activation rechecks tool schemas.
  Live capability observation refreshes Settings/sidebar/home without configuring
  transports or inspecting skill files on Update. It must keep running with
  Settings/sidebar visible after an agent removes the launcher screen. Inspected
  MCP details retain their exact tool identity and refresh schema/grant evidence
  without replacing filters or editable drafts; removed tools clear old claims.
  Local/installed/pinned-repository discovery ranks normalized keywords before
  applying limits. Agent guidance uses concise queries and exact returned IDs,
  not catalog paths as a substitute for durable installation. Live acceptance
  submits through the actual composer and keeps chat visible; screenshot-driver
  exit alone is not proof that the model/persistence test completed.
- Application settings contain three disableable built-ins:
  `anthropic-skills`, pinned to the complete commit recorded in
  `types/capability`, `skills-directory` community search, and
  `official-mcp-registry`, including remote HTTP and pinned npm/PyPI packages.
  Skills/MCP use readable store cards with inline Install/Manage, an explicit
  Installed chip, live search over the shared progress rule and Source/Sort
  choice lists, not administration tables.
  All-source search interleaves ranked results; unsupported variants and partial
  source failures stay visible. Built-ins can be edited or disabled but not deleted; source
  trust never bypasses exact acquisition preview or MCP grants.
- Capability snapshot contexts cancel before workspace teardown. Workspace
  capability lifetime locking protects persistence/service replacement; a stale
  read cannot inspect a nil/new WorkspaceDB during Close/SwitchProject.
- Git comparisons are typed main tabs, prepared off the UI goroutine. Index
  mutations require reviewed bytes; sidebar context actions open review instead
  of bypassing it. Discard requires exact preview + mandatory Local History
  safety recovery and never deletes untracked data.
- Composer + Capability/palette selection is staging only. Exact source-qualified
  skill invocation, tool reference and external context must retain the owning
  session/agent/draft; old arguments or async results cannot attach elsewhere.
- Directed collaboration events and report/outcome publications are delivered
  automatically at safe boundaries; Board reads are history/search, not polling
  transport. Peer data is never approval or permission. `agent_send` broadcast
  is peer-note-only and snapshots only other available, attached agents with a
  current nonterminal run; it excludes idle/suspended/offline/finished history
  and is never routine progress or task fan-out. Human unread and durable agent
  context admission remain independent. Memory revisions use expected-version
  correction; exposure never reinforces usefulness.
- Collaboration work requests are journal-owned: initial revision 1, exact next
  author-issued revision with unchanged recipients, and typed latest-revision
  accept/defer/decline. Never infer agreement from prose or allow duplicate
  acknowledgement loops. Roots select reusable children with `agent_start.profile`;
  bounded leaves coordinate with peers but cannot recursively delegate.
- Controls fence exact run ID/version or availability epoch; a replacement run
  never inherits an earlier cancel. Pause cancels execution and suspends admission;
  resume restores availability, not a stack. Human outcomes are durable audit
  facts, not fake model deliveries. Publication is never proof of application.
- Report author/run/request provenance is immutable across revisions. Only an
  actual child-owned final addressed to its parent satisfies the deliverable;
  interim reports and missing-final terminal reasons cannot become completed
  work. Notifications expose exact artifact references; read the referenced
  revision before relying on its summary.
- Runtime takeover first loads local context receipts, then atomically reconciles
  admissions, requeues only uncommitted reservations, interrupts orphaned runs and
  releases their claims under the exact process lock/epoch. Root admission,
  compaction and normal turns share one authoritative active context. Resume
  admits old evidence passively with zero provider/tool/child replay; genuinely
  new peer input may wake the attached idle root. See
  `docs/reference/workspace-collaboration.md`.

See `docs/guides/capability-workflows.md` for actual routes, protocol limits,
manual workflows and acceptance boundaries.

### File Tree Filter And Guide Geometry
- Connected trees project the header/panel's one core Git snapshot and retained
  diagnostics, including unloaded descendants and filtered search matches. Both
  ordinary file and Git metadata changes arm reconciliation. Directory response
  order cannot erase status; clean snapshots clear it. Never add client resolvers
  or project watchers to repair missing replica metadata.
- `shelltree.FilterField` is the single text owner for clicked input AND
  type-to-filter while the tree has focus. `FileTreeModel` may derive its
  in-memory projection when no async path-search service is installed; the
  product's async search never writes legacy model filter text over the input.
  Clearing the final character cancels the old generation, so late results
  cannot resurrect a removed query.
- The shared `filetree.NewTreeRowPainter` adjusts the preferred whitespace
  between ancestor rails using the visible tree's cached maximum depth and
  viewport width. The shell computes the deepest offscreen ancestor shared by
  every painted row. Its clickable breadcrumb owns that common prefix, and the
  row painter omits ONLY those repeated ancestor guide columns. A viewport
  crossing branches rebases to their common ancestor. When it crosses the
  workspace root, the first visible row keeps its normal breadcrumb and ALL
  absolute guides return; do not hide the breadcrumb in that transition.
  narrow breadcrumbs keep the nearest parents and a non-clickable `…` prefix.
  Local guides keep one column per remaining ancestor even on narrow grids;
  directory rows, disclosure, scroll and path ownership stay intact. Focus and
  selection alone never change guide alignment. Do not replace this with
  single-child path flattening or row-local indent math that makes rails jump.

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
- Packages whose tests construct workspaces, sessions, history or a core use
  `TestMain` → `internal/testhome.Run`, which points HOME, USERPROFILE and the
  platform data dirs at one temp profile. Setting only `HOME` is not isolation
  on Windows (`os.UserHomeDir` reads USERPROFILE); it wrote >1000 test
  workspaces and sessions into a real profile. Add the TestMain to any new
  package that can reach `~/.crude`.

---

## Validation Baseline
- Tree fixtures select fallback fonts through `Theme.Tree.Config.UseNerdFont`,
  the current configuration owner. The dark 40x8 golden and directory glyph
  contracts pass without changing golden output.
- Warm agent switching reuses the current list's storage and indexed row payloads
  while retaining source-generation checks. Its existing 15,000 B / 315 allocation
  ceiling remains unchanged; measurements are recorded in
  `docs/reference/performance-baselines.md`.
- Do not regenerate goldens or loosen allocation gates to hide a failure. Run the
  current gates; historical failures are not exceptions to release acceptance.

## Test Commands
```bash
go build ./...
go test ./tui/editor/... ./tui/viewmodel/... ./workspace/...
go test ./... -count=1 -timeout=180s
make docs-check   # documentation acceptance gate
```

## Documentation Gate

Documentation layout, naming, status headers, and cross-references are
enforced by `scripts/docs_check.sh` (`make docs-check`, also a CI step) —
rules and index live in `docs/README.md`. Doc filenames are kebab-case under
`docs/{architecture,reference,guides,plans}/`; every document carries a
`> Status:` header. Keep only actionable plans: after implementation lands and
the current architecture/reference contract is updated, delete the obsolete plan
rather than retaining duplicate archaeological truth.

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
