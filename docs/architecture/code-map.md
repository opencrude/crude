# Code Map

> Status: Current reference — where each responsibility lives in the repository.

Use this map to find the owner of a responsibility before editing. The layer
rules in the root [`AGENTS.md`](../../AGENTS.md) decide what each owner may
import; the topic references under [`docs/reference/`](../README.md) hold the
detailed contracts.

## Repository layout

A folder is one Go package. Group folders hold only packages; a package folder
holds only its own parts and extensions. The rules, and the gate that enforces
them, are in [code layout](../reference/code-layout.md).

| Folder | Members |
|---|---|
| `main.go` | The `crude` command (`go run .`). |
| `app/` | IDE shell entry, config, core spawning. |
| `core/` | `core/` itself is the retained actor; its parts `coreclient/`, `coreprotocol/`, `corewire/`, `shellport/` (with `shellport/remote/`), `projectpath/`, `remoteconn/`, and the test extensions `wiretest/`, `wirefault/`. |
| `workspace/` | Per-project state machine (`workspace/` itself) plus `document/`, `fileio/`, `localhistory/`. |
| `agent/` | Group: `turnloop/` (the turn loop), `session/`, `sessiondb/`, `provider/`, `prompt/`, `tools/`, `codexio/`, `capability/`, `collaboration/`, `cranemcp/`, `docconv/`, `hostshell/`, `mcpclient/`, `memory/`, `plan/`, `prompthistory/`, `safety/`, `skills/`, `toolstore/`, `webmcp/`, `websearch/`, `worktree/`. |
| `tui/` | `tui/` itself hosts the Bubble Tea program; its parts are listed under Terminal UI below. |
| `insight/` | Group: `catalog/`, `facts/`, `governor/`, `predictionbudget/`, `predictioncontext/`, `project/`, `syntax/`, `textindex/`. |
| `lsp/` | Group: `lspaction/`, `lspclient/`, `lspcompletion/`, `lspedit/`, `lspinlay/`, `lspnav/`, `lspsemantic/`. |
| `text/` | Group: `anchor/`, `indentation/`, `jsonrepair/`, `snippet/`, `textdiff/`, `textencoding/`, `textsearch/`, `unidiff/`. |
| `types/` | Group of neutral contracts by subject: `setting/`, `conversation/`, `insight/`, `statuscenter/`, `syntaxdata/`, `accounting/`, `activity/`, `attachment/`, `capability/`, `command/`, `control/`, `handover/`, `inspect/`, `jsondoc/`, `reasoning/`, `recovery/`, `toolresult/`. |
| `internal/` | Group of shared support with no product rules: `buildinfo/`, `clientstate/`, `clipboard/`, `console/`, `crashlog/`, `filelink/`, `fileuri/`, `hostenv/`, `logger/`, `metrics/`, `network/`, `perfdata/`, `retry/`, `sysload/`, `terminalvt/`, `testhome/`, `testsymlink/`, `testterminal/`. |
| `tools/` | Group of development programs: `wiregen/`, `namesgate/`, `layoutcheck/`, `catalogimport/`, `coreshellobserver/`. |

## Application and runtime

| Package | Owns |
|---|---|
| `main.go`, `app/` | CLI parsing, application config, core spawning (`core_image.go` staged executable, detach), MCP control entry. No session or orchestrator knowledge. |
| `internal/buildinfo/` | Build identity and `buildinfo.Home`, the single Crude data root (`CRUDE_HOME`). |
| `internal/crashlog/` | Process-local synchronous fatal output and recovered TUI panic reports; no event-loop or normal-log dependency. |
| `core/` | Retained workspace actors, serialized ingress lanes, snapshot/subscription admission, mutation receipts. `core/attention.go` sends the workspace Errand one notice per Investigate issue; `core/presence.go` elects the shell that shows desktop notifications. |
| `core/coreprotocol/` | Versioned, presentation-neutral core API types. |
| `core/projectpath/` | Core-host directory identity and navigation-only home, personal-folder, and volume-root classification. Bounded directory browsing creates no project state. |
| `core/corewire/` | Bounded framed transport (local socket, SSH stdio bridge). |
| `core/coreclient/` | Client multiplexer and detached read mirrors (`WorkspaceView`). |
| `core/shellport/` | The `shellport.Workspace` seam the TUI consumes; `core/shellport/remote` is the CLI runtime path. |
| `tui/desktopnotify/` | Native desktop notifications for one shell process: `backend_windows.go` (WinRT toasts), `backend_linux.go` (freedesktop D-Bus), `backend_darwin.go` (not yet implemented), `backend_other.go`. Delivery only; policy is in `tui/viewmodel/desktop_notices.go` ([desktop notifications](../reference/desktop-notifications.md)). |
| `assets/` | Brand images; `assets.IconPNG` is the notification icon. `assets/fonts/` holds Crude RTL, the right-to-left fallback font the installers register (built by `scripts/build_rtl_font.py`, [bidi](../reference/bidi.md#fonts)). |
| `internal/clientstate/` | Client-private composer and unresolved-editor persistence with profile locks and epochs. |
| `workspace/` | Per-project state machine: sessions, agent inventory, conversation, LSP manager, documents, file ops, search, terminals, Git, Local History lifecycle. Never imports `tui/`. |
| `types/setting/` | Application and workspace settings: the stored shape, defaults, sparse edits and validation ([settings](../reference/settings.md)). |
| `types/conversation/` | Conversation messages, turn identity, roles, goals, loops, plan review and runtime events. |
| `types/insight/` | Code-intelligence contracts: insight states, LSP results, assembly, project tools and editor prediction. |
| `types/statuscenter/` | Status, activity, usage and operation-issue snapshots for the status center. |
| `types/syntaxdata/` | Neutral native-syntax spans, problems and results shared by `insight/syntax` and the editor. |

## Agent and model (`agent/`)

| Package | Owns |
|---|---|
| `agent/turnloop/` | Agent turn loop, supervisor and recovery, request boundaries, guidance intake, tool dispatch. |
| `agent/session/` | Session runtime: tools execution, memory pipeline, children (live sub-agent limit), collaboration wiring, compaction triggers, agent code intelligence (`code_intelligence.go`) and plan discipline (`plan_discipline.go`). |
| `agent/sessiondb/` | Session SQLite schema: transcript, recovery checkpoints, context publication and their row-stored message lists, queue. |
| `agent/prompt/` | Every code-authored model instruction template ([prompt architecture](../reference/prompt-architecture.md)). |
| `agent/provider/` | Wire protocols, streaming, failure classification, connection accounting. |
| `agent/provider/text_once.go`, `agent/provider/fim_once.go` | Bounded one-attempt no-tools text/FIM operations with original-wire usage-presence evidence; no prediction coordinator, consent authority, retry or adapter fallback. |
| `agent/session/editor_prediction.go` | Captured Errand account/role binding for usage-preserving one-shot requests; live connection preflight, no consent, endpoint probing or chat-session creation. |
| `agent/codexio/` | Native Codex/OpenAI interfaces such as freeform `apply_patch`. |
| `agent/tools/` | Tool schemas, argument summaries, parallel dispatch, effects. |
| `agent/plan/` | Revisioned declarative plan store and activity view. |
| `agent/memory/` | Workspace memory records, evidence grounding, history search, audit journal. |
| `agent/collaboration/` | Durable agents/runs, directed delivery, reports, claims and controls. |
| `agent/capability/`, `agent/skills/`, `agent/mcpclient/`, `agent/cranemcp/` | Capability discovery/acquisition/grants, skill loading, MCP client transport, Crude's own MCP control server. |
| `agent/safety/` | Command safety assessment and permission policy. |
| `agent/hostshell/` | Installed-shell inventory on the core's machine and the agent command shell resolver. |
| `internal/retry/` | The single automatic-cooldown contract (five-minute ceiling). |
| `internal/metrics/` | Bounded per-process performance history (latency, counters, gauges, keyed tables, stall events) and, per snapshot, OS resource usage (memory, CPU, started processes, disk via a registered source) read by the Performance surface ([performance baselines](../reference/performance-baselines.md)). |
| `internal/sysload/` | Observes the computer for background work: power source, CPU/memory/I/O pressure (Linux PSI) or idle cores, and available memory. Read off the UI goroutine by the closed-file check ([editor](../reference/editor.md#project-check-of-closed-files)). |
| `internal/perfdata/` | Neutral canonical performance findings, headlines and host-qualified resource projection, shared by dashboard and semantic inspection. Owns the host-local durable performance archive, its history reads and the shareable Markdown performance report, not the live registry. |
| `types/inspect/` | Owner-bound immutable diagnostic pages with encoded-byte/range/retention bounds; no telemetry registry, authorization or background collector. |
| `internal/network/` | Shared outbound HTTP transport, proxy, trust and client identity. |
| `agent/toolstore/` | Read-only folders for acquired code-intelligence tools under `CRUDE_HOME`: bounded downloads, safe extraction, manifests, usage, history and previewed removal ([tool store](../reference/tool-store.md)). Records the caller's approval; owns no approval or language knowledge. |
| `agent/websearch/`, `agent/docconv/` | Web/arXiv search engines and binary document conversion for read/fetch tools. |
| `agent/worktree/` | Managed execution worktrees and integration plans. |

## Files, documents and history (`workspace/`, `text/`, `lsp/`)

| Package | Owns |
|---|---|
| `workspace/fileio/` | Bounded reads, atomic commit (`CommitAtomicFile`), mutation leases, command execution, desktop open. |
| `internal/fileuri/` | Native path, local file URI and SQLite file DSN conversion. |
| `workspace/document/` | Presentation-neutral shared text commands. |
| `insight/syntax/` | Bounded native syntax sessions, selected grammar/query pins, byte spans and syntax findings; not the canonical text owner or a semantic engine ([native syntax](../reference/native-syntax.md)). |
| `insight/catalog/`, `tools/catalogimport/` | Languages, language servers and installable tools as validated data with provenance, detection through Linguist data, resource hints and release assets; generated from pinned sources by the development importer, plus editor data (comment tokens, formatters, grammar names) and runtime packs ([catalog](../reference/code-intelligence-catalog.md)). |
| `tools/namesgate/` | Development gate: fails when product Go code names a language, tool or server that the catalog owns; reviewed exceptions in `allow.txt` ([names gate](../reference/code-intelligence-catalog.md#names-gate)). |
| `insight/project/` | Bounded passive compilation-database artifact observations with exact byte identity and detached argv/opaque command projections; not a project model, execution authority or semantic coverage. |
| `insight/project/cmake/` | Bounded passive CMake File API artifact observations with exact byte identity and detached configuration/target projections; native-shell fragments stay opaque, and parsing grants no read/execute authority, freshness or semantic coverage. |
| `insight/predictionbudget/` | Sessionless durable token reservations, single-attempt tickets and conservative unknown-usage settlement; library-only, not egress consent or an enabled editor request path. |
| `insight/predictioncontext/`, `agent/prompt/editor_prediction.go` | Bounded exact-source diagnostic subset, source window, digest and explicit missing-input coverage; narrow escaped insertion prompt, not complete semantic/project context. |
| `types/insight/editor_prediction*.go`, `types/setting/editor_prediction*.go`, `workspace/editor_prediction_preferences.go`, `tui/screens/settingspages/prediction.go` | Workspace-only human consent, strict authority decoding, one shared literal insertion contract and native preference rows; application/session inheritance and caller-generated grants are refused. |
| `workspace/editor_prediction_service.go` | Internal bounded invocation/cancellation/cache and sessionless accounting composition; not yet constructed by the live workspace or connected to an editor RPC, source intent or visible meter. |
| `workspace/project_context.go`, `workspace/project_artifact.go` | Retained explicit artifact-selection/observation owner, native root/file identity, coalesced worker, stale evidence and joined lifecycle ([project context](../reference/project-context.md)); not autonomous selection, provider configuration or complete semantic context. |
| `text/textsearch/` | Shared exact/regex source-span matching and replacement expansion; established fzf ranking for project discovery only. |
| `workspace/localhistory/` | Immutable snapshots, restore plans, verification, retention ([local history](../reference/local-history.md)). |
| `text/textdiff/`, `text/unidiff/` | Exact line changes from immutable text; unified diff helpers. |
| `agent/prompthistory/` | Prompt history with score-based eviction. |
| `lsp/lspclient/` | Nonvisual LSP subprocess transport and pipe monitor ([editor](../reference/editor.md)). |
| `lsp/lspedit/` | Passive bounded original-JSON text-edit normalization and strict bidirectional UTF-8-byte/UTF-16 source positions; preserves nullable wire versions without filesystem or execution authority ([reviewed rename](../reference/editor.md#reviewed-symbol-rename)). |
| `lsp/lspaction/` | Passive bounded original-JSON literal-action parsing and edit-only resolve comparison; preserves opaque data and unsupported rows without provider, URI, source or mutation authority ([manual code actions](../reference/editor.md#manual-code-actions)). |
| `lsp/lspnav/` | Passive bounded original-JSON Location/LocationLink and DocumentHighlight normalization with distinct null/empty answers; preserves ranges/order/URI data without file-read or target-freshness authority ([qualified navigation](../reference/editor.md#qualified-location-navigation), [occurrences](../reference/editor.md#occurrences-of-the-symbol-at-the-caret)). |
| `lsp/lspinlay/` | Passive bounded original-JSON inlay parsing and tooltip-only resolve comparison; original opaque data and inert adjuncts carry no source, navigation, mutation or execution authority ([inlay hints](../reference/editor.md#read-only-inlay-hints)). |
| `lsp/lspcompletion/` | Passive bounded original completion parsing, documentation/detail-only resolve comparison and one strict initial-primary-edit/snippet normalizer; no provider, source mutation or execution authority ([completion documentation](../reference/editor.md#completion-documentation-and-original-insertion)). |
| `workspace/reviewed_edit.go`, `workspace/lsp_reviewed_edit.go` | Immutable canonical language-edit review, source/provider/context/native preconditions, non-agent safety reference and durable original mutation outcome; FileOps remains the only disk writer. |
| `workspace/lsp_code_action.go`, `workspace/lsp_code_action_request.go` | Bounded original action-set ownership, source-byte selection, synchronized provider/context fencing and edit-only review composition; no action execution or second mutation writer. |
| `workspace/lsp_highlight.go`, `lsp/lspclient/lsp_highlight.go`, `core/lsp_highlight.go` | Occurrences of the symbol at a caret: static-provider bridge, strict source-fitted ranges and the same semantic fence as navigation; presentation evidence only. |
| `workspace/lsp_navigation*.go`, `lsp/lspclient/lsp_navigation.go`, `core/lsp_navigation.go` | One qualified navigation request/negative-answer family, raw synchronized-provider bridge, strict URI/range policy and explicit selected-source observation; shell-port commands never confer target-version, execution or edit authority. |
| `workspace/lsp_inlay*.go`, `lsp/lspclient/lsp_inlay.go`, `core/lsp_inlay.go` | Canonical visible-range read evidence, exact owner/view/sequence retention and original tooltip-only observation; separate feature epoch, cancellation and bounded expiry. No hint edit, command or label-location execution. |
| `workspace/lsp_completion*.go`, `lsp/lspclient/lsp_completion.go`, `core/lsp_completion.go` | Original completion-set authority, source/provider/context/feature fencing, passive selected-row resolve and exact initial-edit acceptance preflight; view release, bounds and expiry, never a second text writer. |
| `workspace/reviewed_edit_safety.go`, `core/history_safety.go` | Original-journal and selected-preimage evidence verification for native human retention decisions; core receipts and Store-owned atomic append-only audit/pin transitions, not a second file writer. |
| `text/snippet/` | Bounded passive numeric completion parser/validator; neutral expanded-primary byte plans, no editor state, environment or execution authority ([numeric snippets](../reference/editor.md#numeric-completion-snippets)). |
| `internal/terminalvt/` | libghostty-vt parsing and protocol encoding for in-app terminals. |
| `internal/clipboard/`, `internal/logger/`, `text/jsonrepair/` | Platform clipboard, structured logging, streamed-JSON repair. |

## Terminal UI (`tui/`)

| Package | Owns |
|---|---|
| `tui/viewmodel/` | The Bubble Tea `App`: presentation state, root Host, key/mouse dispatch, shell geometry ([shell host](shell-host.md)). |
| `tui/startup/` | Detached native introduction and project selection before workspace attachment. Uses the shared folder picker and request-owned startup services. |
| `tui/ui/` | The native component system: Host, Form, List, Tabs, TextArea/TextField, ScrollbarControl, WindowFrame. |
| `tui/screens/` | One package per retained screen (`shellchat`, `shelleditor`, `shelltree`, `shellheader`, `shellfooter`, pickers, decks). |
| `tui/screens/codeactions/` | Read-only manual action list/detail, complete literal/provenance reading, native safe-default buttons and explicit preview intents; no RPC or mutation authority. |
| `tui/screens/inlay/` | Native read-only hint list/detail with escaped bounded original-JSON previews and explicit tooltip Resolve/Back/Close intents; no provider, file or editor I/O. |
| `tui/screens/completion/` | One native List/Scrollbar and passive read-only selected-row TextArea, responsive adjacent/stacked layout, exact row intents and inert text; no provider, file or editor effects. |
| `tui/chatprojection/` | Generation-keyed chat row snapshots, activity strip, tool rows. |
| `tui/editor/` | Editor engine: PieceTable buffer, cursor, selection, undo, rendering, search. |
| `tui/input/` | Chat composer: attachments, large pastes, history recall. |
| `tui/filetree/` | File tree model and row painter. |
| `tui/screens/settingspages/` | Settings pages. |
| `tui/window/`, `tui/wm/` | Main-pane tab groups and split tree; floating window manager. |
| `tui/cell/`, `tui/surface/`, `tui/compositor/` | Cells, surfaces and compositing ([compositing engine](compositing-engine.md)). |
| `tui/rendering/` | Width profile, Markdown, syntax styles, ANSI bridging. |
| `tui/bidi/` | Right-to-left arrangement: UAX #9 paragraphs and display lines, Arabic joined forms, column maps, caret stepping and the process-wide policy ([bidi](../reference/bidi.md)). |
| `tui/theme/` | Themes and design tokens; the only place for hard-coded hex. |
| `tui/termview/` | Terminal scrollback, selection and copy projection. |
| `tui/tools/` | Tool-result presentation helpers. |
| `tui/screens/perfdash/` | Performance presentation: sections (including Resources), charts, owner tables and Save report, consuming canonical `internal/perfdata/` findings and its Markdown report writer. |
| `tui/screens/markdownview/` | Markdown file tab: Preview / Side by side / Source strip, block-wise off-UI rendering with the chat's Markdown renderer, source-line scroll sync. |
| `tui/perf/`, `tui/uitest/` | Perf logging and the UI test harness. |
| `internal/console/` | Console setup before the program starts (the UTF-8 code page on Windows). |

## Vendored replacements (`third_party/`)

`bubbletea`, `xansi`, `ultraviolet` carry the shared width profile
([terminal width](../reference/terminal-width.md)); `bubbletea` also owns the
host BiDi mode handshake and `typesetting` pins go-text's UAX #9 and HarfBuzz's
Arabic joining data ([bidi](../reference/bidi.md)); `powernap` carries the
non-blocking LSP transport hook; `jsonrpc2` retains original request/notification
params and atomic envelope validation ([fork provenance](../../third_party/jsonrpc2/CRUDE.md));
`fastwalk` is the internal directory walker. Each folder's `CRUDE.md` states
its upstream, base version, local changes and update steps.
`bubbletea` also exposes a narrow `WithPanicHandler` observation hook in both
existing recovery paths. Crude writes the original panic and stack before
terminal cleanup; the framework keeps its normal restoration and error outcome.
`modernc-libc` and `modernc-memory` retain the complete pinned upstream modules
for the existing SQLite driver. Their narrow Windows call-adapter patches remove
temporary syscall argument slices without changing database formats, WAL,
durability, locking or allocator ownership. Versions and all non-Windows source
remain upstream-identical; provenance, local differences and pointer-lifetime
rules are in their [libc](../../third_party/modernc-libc/CRUDE.md) and
[memory](../../third_party/modernc-memory/CRUDE.md) notes.
Do not drop a `go.mod` replace without reading the owning reference.

## Key files

### `tui/viewmodel/`

| File | Role |
|---|---|
| `app.go` | App struct and construction |
| `update.go` | `tea.Model.Update` and message dispatch |
| `reviewed_edit_dialog.go`, `lsp_code_actions.go` | One shared immutable rename/action review and receipt owner, composed with an invocation-bound native manual action chooser |
| `lsp_navigation.go` | One qualified location invocation/chooser and strict selected-target async landing, shared by the editor's definition/type/implementation/reference actions |
| `lsp_highlight.go` | The focused editor's resting-caret occurrence question: one owned request, exact source/caret/provider fences, quiet failure |
| `lsp_inlay*.go` | Debounced per-visible-editor inlay owner, local show/hide/refresh and native original-row/tooltip inspector; source coordinates remain authoritative |
| `lsp_completion.go` | One canonical invocation/chooser and selected-row documentation reader; initial-edit preflight, original-context publication barrier and editor-owned acceptance |
| `view.go` | `tea.Model.View` and the frame pipeline |
| `shell_root.go`, `layout.go` | Root shell composite and geometry |
| `keyboard.go`, `mouse.go`, `popup_mouse.go` | Key and pointer dispatch |
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
| `desktop_notices.go` | Shell presence reports and the desktop-notification policy: news, settle, banner or quiet update, withdraw, click |

### `workspace/`
| File | Role |
|---|---|
| `workspace.go` | Workspace struct (composition root: managers + references) |
| `event_bus.go`, `view_data.go`, `subpane.go` | Neutral single-sink EventBus and its external messages; the Mode enum; the PaneType enum |
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

### `agent/prompt/`
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
| `.crude/notifications/icon.png` | The icon that desktop notifications show (written by the shell) |
| `.crude/bin/crude-core-<build-id>` | The staged, immutable core executable |
| `.crude/crashes/tui-<time>-<pid>-<nonce>.log` | Private synchronous TUI crash reports |
| `.crude/crashes/core-<time>-<pid>-<nonce>.log` | Private synchronous core crash reports on that core's host |
