# Code Map

> Status: Current reference — where each responsibility lives in the repository.

Use this map to find the owner of a responsibility before editing. The layer
rules in the root [`AGENTS.md`](../../AGENTS.md) decide what each owner may
import; the topic references under [`docs/reference/`](../README.md) hold the
detailed contracts.

## Application and runtime

| Package | Owns |
|---|---|
| `main.go`, `app/` | CLI parsing, application config, core spawning (`core_image.go` staged executable, detach), MCP control entry. No session or orchestrator knowledge. |
| `buildinfo/` | Build identity and `buildinfo.Home`, the single Crude data root (`CRUDE_HOME`). |
| `core/` | Retained workspace actors, serialized ingress lanes, snapshot/subscription admission, mutation receipts. |
| `coreprotocol/` | Versioned, presentation-neutral core API types. |
| `projectpath/` | Core-host directory identity and navigation-only home, personal-folder, and volume-root classification. Bounded directory browsing creates no project state. |
| `corewire/` | Bounded framed transport (local socket, SSH stdio bridge). |
| `coreclient/` | Client multiplexer and detached read mirrors (`WorkspaceView`). |
| `shellport/` | The `shellport.Workspace` seam the TUI consumes; `shellport/remote` is the CLI runtime path. |
| `clientstate/` | Client-private composer and unresolved-editor persistence with profile locks and epochs. |
| `workspace/` | Per-project state machine: sessions, agent inventory, conversation, LSP manager, documents, file ops, search, terminals, Git, Local History lifecycle. Never imports `tui/`. |
| `types/` | Neutral contracts shared by every layer (settings, reasoning, accounting, activity, capability registration). |

## Agents and models

| Package | Owns |
|---|---|
| `agent/` | Agent turn loop, supervisor and recovery, request boundaries, guidance intake, tool dispatch. |
| `session/` | Session runtime: tools execution, memory pipeline, children (live sub-agent limit), collaboration wiring, compaction triggers, agent code intelligence (`code_intelligence.go`) and plan discipline (`plan_discipline.go`). |
| `sessiondb/` | Session SQLite schema: transcript, recovery checkpoints, context publication and their row-stored message lists, queue. |
| `prompt/` | Every code-authored model instruction template ([prompt architecture](../reference/prompt-architecture.md)). |
| `provider/` | Wire protocols, streaming, failure classification, connection accounting. |
| `codexio/` | Native Codex/OpenAI interfaces such as freeform `apply_patch`. |
| `tools/` | Tool schemas, argument summaries, parallel dispatch, effects. |
| `plan/` | Revisioned declarative plan store and activity view. |
| `memory/` | Workspace memory records, evidence grounding, history search, audit journal. |
| `collaboration/` | Durable agents/runs, directed delivery, reports, claims and controls. |
| `capability/`, `skills/`, `mcpclient/`, `cranemcp/` | Capability discovery/acquisition/grants, skill loading, MCP client transport, Crude's own MCP control server. |
| `safety/` | Command safety assessment and permission policy. |
| `retry/` | The single automatic-cooldown contract (five-minute ceiling). |
| `metrics/` | Bounded per-process performance history (latency, counters, gauges, keyed tables, stall events) and, per snapshot, OS resource usage (memory, CPU, started processes, disk via a registered source) read by the Performance surface ([performance baselines](../reference/performance-baselines.md)). |
| `perfdata/` | Neutral canonical performance findings, headlines and host-qualified resource projection, shared by dashboard and semantic inspection; no sampling or state owner. |
| `types/inspect/` | Owner-bound immutable diagnostic pages with encoded-byte/range/retention bounds; no telemetry registry, authorization or background collector. |
| `network/` | Shared outbound HTTP transport, proxy, trust and client identity. |
| `search/`, `docconv/` | Web/arXiv search engines and binary document conversion for read/fetch tools. |
| `worktree/` | Managed execution worktrees and integration plans. |

## Files, documents and history

| Package | Owns |
|---|---|
| `fileio/` | Bounded reads, atomic commit (`CommitAtomicFile`), mutation leases, command execution, desktop open. |
| `document/` | Presentation-neutral shared text commands. |
| `syntax/` | Bounded native syntax sessions, selected grammar/query pins, byte spans and syntax findings; not the canonical text owner or a semantic engine ([native syntax](../reference/native-syntax.md)). |
| `insight/project/` | Bounded passive compilation-database artifact observations with exact byte identity and detached argv/opaque command projections; not a project model, execution authority or semantic coverage. |
| `textsearch/` | Shared exact/regex source-span matching and replacement expansion; established fzf ranking for project discovery only. |
| `localhistory/` | Immutable snapshots, restore plans, verification, retention ([local history](../reference/local-history.md)). |
| `textdiff/`, `diff/` | Exact line changes from immutable text; unified diff helpers. |
| `history/` | Prompt history with score-based eviction. |
| `lspclient/` | Nonvisual LSP subprocess transport and pipe monitor ([editor](../reference/editor.md)). |
| `terminalvt/` | libghostty-vt parsing and protocol encoding for in-app terminals. |
| `clipboard/`, `logger/`, `jsonrepair/` | Platform clipboard, structured logging, streamed-JSON repair. |

## Terminal UI (`tui/`)

| Package | Owns |
|---|---|
| `tui/viewmodel/` | The Bubble Tea `App`: presentation state, root Host, key/mouse dispatch, shell geometry ([shell host](shell-host.md)). |
| `tui/startup/` | Detached native introduction and project selection before workspace attachment. Uses the shared folder picker and request-owned startup services. |
| `tui/ui/` | The native component system: Host, Form, List, Tabs, TextArea/TextField, ScrollbarControl, WindowFrame. |
| `tui/screens/` | One package per retained screen (`shellchat`, `shelleditor`, `shelltree`, `shellheader`, `shellfooter`, pickers, decks). |
| `tui/chatprojection/` | Generation-keyed chat row snapshots, activity strip, tool rows. |
| `tui/editor/` | Editor engine: PieceTable buffer, cursor, selection, undo, rendering, search. |
| `tui/input/` | Chat composer: attachments, large pastes, history recall. |
| `tui/filetree/` | File tree model and row painter. |
| `tui/settings/` | Settings pages. |
| `tui/window/`, `tui/wm/` | Main-pane tab groups and split tree; floating window manager. |
| `tui/cell/`, `tui/surface/`, `tui/compositor/` | Cells, surfaces and compositing ([compositing engine](compositing-engine.md)). |
| `tui/rendering/` | Width profile, Markdown, syntax styles, ANSI bridging. |
| `tui/theme/` | Themes and design tokens; the only place for hard-coded hex. |
| `tui/termview/` | Terminal scrollback, selection and copy projection. |
| `tui/tools/` | Tool-result presentation helpers. |
| `tui/screens/perfdash/` | Performance presentation: sections (including Resources), charts, owner tables and copyable report, consuming canonical `perfdata/` findings. |
| `tui/perf/`, `tui/uitest/`, `tui/platform/`, `tui/xchroma/` | Perf logging, UI test harness, console platform shims, Chroma adapter. |

## Vendored replacements (`third_party/`)

`bubbletea`, `xansi`, `ultraviolet` carry the shared width profile
([terminal width](../reference/terminal-width.md)); `powernap` carries the
non-blocking LSP transport hook; `pathwalk` is the internal directory walker.
Do not drop a `go.mod` replace without reading the owning reference.

## Key files

### `tui/viewmodel/`

| File | Role |
|---|---|
| `app.go` | App struct and construction |
| `update.go` | `tea.Model.Update` and message dispatch |
| `view.go` | `tea.Model.View` and the frame pipeline |
| `shell_root.go`, `layout.go` | Root shell composite and geometry |
| `keyboard.go`, `mouse.go`, `popupmouse.go` | Key and pointer dispatch |
| `main_panes.go`, `tab_bar.go`, `agent_tabs.go`, `editor_tabs.go` | Main-pane groups and tab lifecycle |
| `floating_panes.go`, `panel_placement.go` | Pane windows (floating tab groups) and dock/tab/window placement of workspace surfaces ([main view panes](../reference/main-view-panes.md#pane-windows)) |
| `git_sync.go` | Git fetch/pull/push/merge commands, results and the merge resolver lifecycle |
| `agent_events.go`, `agent_view.go` | Agent event presentation and shell-local agent visibility |
| `focus.go`, `focus_stack.go` | Shell-owned focus targets and LIFO |
| `session_selection.go` | Shell-selected session/model and owner-fenced settings results |
| `workspace_switch_lifecycle.go` | Workspace-switch generation boundary |
| `control_diagnostics.go` | Exact-shell on-demand metric/connection reads with joined client-I/O work and stale completion rejection |
| `frame_scheduler.go` | Presentation coalescing (16 ms deadline) |
| `git_snapshot.go` | Silent Git snapshot reconciliation |
| `slash_commands.go`, `command_palette.go` | Slash commands and palette |

### `workspace/`
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
| `lsp_manager.go` | LSPManager (connections, diagnostics cache, editor/probe document holders) |
| `lsp_probe.go` | Agent code-intelligence probes (diagnostics, definition, references, hover, symbols) |
| `git_status.go` | Git executable boundary: porcelain status, checkout-form reads, clean-filtered blob writes, `ErrGitNotInstalled` |
| `git_remote.go`, `git_conflicts.go` | Remote Git operations, merge state, conflict parsing and Local-History-backed resolution |
| `references.go` | ReferenceStore (SQLite-backed @references) |
| `file_ops.go` | FileOpsService (filesystem mutations) |
| `project_search.go` | ProjectSearchService (Find in Files execution) |
| `view_data.go` | Presentation-neutral view data seams (TreeRowView, TabView, StatusView) |
| `bootstrap.go` | Session create/resume/delete, launcher flows |
| `db.go` / `paths.go` | Workspace DB + `<CRUDE_HOME>/.crude/workspaces/<hash>/` paths |

### `tui/editor/`
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

### `prompt/`
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
| `runtime.go` | Turn context, continuity, plan (including drift/reconcile notices), memory recall, diagnostics, delegation, and wake projections |

## Data locations

All Crude data lives under `buildinfo.Home` (the user's home directory unless
`CRUDE_HOME` is set), in `buildinfo.DataDir()` — `.crude` for normal builds,
`.crude-beta` for beta builds:

| Path | Contents |
|---|---|
| `.crude/config.json` | Application settings and app-global catalogs |
| `.crude/workspaces/<hash>/workspace.db` | Workspace settings overlay, memory, collaboration, references |
| `.crude/workspaces/<hash>/local-history/` | Immutable Local History objects, manifests and plans |
| `.crude/workspaces/<hash>/sessions/<id>.db` | One SQLite database per session |
| `.crude/workspaces/<hash>/sessions/<id>/` | Session-owned files (temporary directory, attachments, child-agent data) |
| `.crude/corewire/core.sock` | The per-user core socket |
| `.crude/bin/crude-core-<build-id>` | The staged, immutable core executable |
