# Shell port

> Status: Current — `shellport.Workspace` is the only runtime surface `tui/` consumes. `shellport/remote` is the connected CLI implementation; `shellport.Local` remains the in-process core/test adapter.

`tui/viewmodel.App` used to embed `*workspace.Workspace`, so ~290 files reached
runtime fields, services and methods directly. It now holds one
`shellport.Workspace` (`App.ws`) and every former promoted access is an explicit
call. The package `shellport/` imports `workspace/` and the other domain packages
for **data types only**; it never imports `tui/`. This document is the
implementation inventory for the connected (`core/` RPC) adapter and its explicit remaining limits.

## Layout

`shellport.Workspace` is a composition of cohesive sub-interfaces. Each file in
`shellport/` holds one cohesive group and the remote implementation follows these same service seams:

| File | Sub-interface(s) | Concern |
|------|------------------|---------|
| `runtime.go` | `Runtime`, `WorkspaceStore` | identity, lifecycle, project switch, event wiring, DB identity token |
| `startup.go` | `Startup`, `WorkspaceFolderCreator` | Detached path inspection, bounded folder browsing and discovery, explicit single-child folder creation, and sparse introduction/reminder saves. No workspace attachment is necessary. |
| `agents.go` | `Agents` | agent inventory replica, turn control, blocking prompts, input admission |
| `sessions.go` | `Sessions` | session registry replica, create/open, per-session settings |
| `settings.go` | `Settings` | app/workspace settings, accounts |
| `files.go` | `Files` | file mutations, project search; carries `Documents()` and `LSPMgr()` accessors |
| `documents.go` | `DocumentService` | checked snapshot save infrastructure |
| `live_documents.go`, `live_document_commands.go` | `LiveDocuments`, formatter/receipts/command interfaces | universal connected editor lifecycle, per-open topic, recovery sync, exact-owner commands |
| `insight.go` | `DocumentInsightMsg` | Independent canonical syntax projection, exact-open validity and applied ACK; not a text mutation or semantic guarantee |
| `lsp.go` | `LSPService` | language-server manager |
| `reviewed_edit.go` | `ReviewedEdits` | Connected canonical rename preview, opaque approval, pending release and original-result inspection |
| `code_actions.go` | `CodeActions` | Exact canonical action-list discovery, retained-row preview and pending-list release; reuses `ReviewedEdits` for Apply and inspection |
| `navigation.go` | `Navigation` | Read-only qualified definition/type/implementation/reference answers and explicit selected-target observation |
| `formatting.go` | optional `SelectionFormatter` | Exact canonical range-format plans from the selected language server; the original editor owns fenced native application and Undo, never Save. |
| `completion.go` | `Completions` | Canonical source/view-bound original completions, selected documentation-only resolve, acceptance preflight and ordered release |
| `semantic.go` | optional `SemanticTokens` | Source-qualified semantic color reads and private view release; delta bases stay in core |
| `history_safety.go` | `HistorySafety` | Native review-safety administration with bounded traversal, original evidence, exact human consent and original core receipt observation |
| `git.go` | `SourceControl`, `GitService` | Git reads and mutations |
| `history.go` | `History`, `LocalHistoryStore`, `LocalHistoryService` | Local History |
| `memory.go` | `Memory`, `MemoryStore` | memory browse and curation |
| `capabilities.go` | `Capabilities` | skills / MCP / acquisition |
| `code_tools.go` | optional `CodeTools` | shared code-tool inventory and native previewed removal |
| `project_tools.go` | optional `ProjectTools` | native owner-bound project setup review and decision |
| `collaboration.go` | `Collaboration` | agent Board |
| `terminals.go` | `TerminalHost`, `TerminalService`, `TerminalManager` | PTY services |
| `status.go` | `StatusHost`, `StatusService` | workspace status center |
| `remotes.go`, `remote_access.go` | `RemoteManager`, `BackendLink`, `RemoteAccessAdmin` | shell-local connection catalog/backend switching; host-local admission administration |
| `local.go`, `local_delegates.go` | `Local`, `Unwrap` | in-process adapter over `*workspace.Workspace` |

## Conventions

`tui/startup/` uses the separate `Startup` port before a project workspace exists.
The connected implementation is `shellport/remote/startup.go`.
Read [startup admission](core-client-protocol.md#startup-before-workspace-attachment) for ownership, operations, and limits.
Attached shells read committed setup state through the same selected core.

- **Accessors replace fields.** Every former `Workspace` field the shell read is
  a zero-argument method with the same name (`RootPath()`, `AgentMgr()`,
  `LSPMgr()`, ...). Services are returned as narrow interfaces listing only the
  methods `tui/` calls. `Local` returns a true nil interface when the underlying
  service is absent, so `if ws.LSPMgr() != nil` keeps its meaning.
- **Concrete data holders stay concrete.** `*workspace.AgentManager`,
  `*workspace.PaneManager`, `*workspace.Session`, `workspace.Logger`, events,
  messages, view-data and `types.*` structs appear in signatures. A remote
  implementation fills the same replica types (`coreclient.WorkspaceView` already
  mirrors `AgentMgr`, `PaneMgr`, `Sessions*`, `SessionForAgent`, `AgentActivity`,
  `AgentModelIdentity`, `AgentTurnActive`, `YoloForAgent`, `UsageSummary`,
  `LoadAppSettings`, `AppDefaults`).
- **Lazy construction is explicit.** The shell used to create missing services by
  assigning promoted fields (`m.Git = workspace.NewGitService(...)`). That is now
  `EnsureGit()`, `EnsureDocuments(bus)` and `EnsureTerminals()`. Remote
  implementations return core-owned service adapters without constructing runtime owners.
- **Owner tokens.** `App.ws` is compared by interface identity (stale async
  results are fenced by `owner != m.ws`). `Local` returns a pointer adapter, so
  one `Local(ws)` value is a stable token. `BindMemory()` and
  `WorkspaceDB()` return values pinned to the current workspace database so late
  work queued before `SwitchProject` cannot write into the replacement workspace.
- **Tests** use `shellport.Local` or connected real Apps over `net.Pipe`; `shellport.Unwrap(ws)` returns the
  concrete workspace for test setup and for assertions that reach internals
  (`wsOf(app)` in `tui/viewmodel` tests). Production `tui/` code never calls
  `Unwrap`.
- **Call-site counts** are exact selections of the member (via `go/types`) over
  non-test code in `tui/`, taken when this port landed (historical, not recounted for this checkpoint). `reads/mutates` says what
  the TUI does through the member; `RPC-able` judges the signature as-is.
- **Protocol** names the existing `coreprotocol` method, or `replica` when the
  state is installed from acknowledged workspace/state topics and read from
  `coreclient.WorkspaceView`, or `none` for local-only/core-internal behavior.
  Snapshot and change commands remain explicit inspection/recovery operations,
  not the normal replication loop. Closure/object signatures remain local port
  contracts; only detached typed payloads cross the wire.

## Inventory

### `Runtime`

Runtime is the identity, lifecycle and startup surface of one project workspace.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `RootPath` | 43 | reads | yes | workspace.attach result (replica field) |
| `Username` | 1 | reads | yes | replica field |
| `StartTime` | 4 | reads | yes | replica field |
| `InitialSettings` | 1 | reads | yes | replica field |
| `InitialModelName` | 1 | reads | yes | replica field |
| `Logger` | 65 | reads | no: client-local sink | none; client-local logging |
| `WorkspaceDB` | 4 | reads | no: database handle / identity token | none; identity token (see WorkspaceStore) |
| `SetEventBus` | 1 | mutates | no: passes event sink | none; sink wiring for acknowledged workspace/state topics |
| `SwitchProject` | 1 | mutates | yes | workspace.detach + workspace.attach (shell re-attaches to the target workspace) |
| `TakeStartupIssues` | 1 | reads | yes | workspace.startup |
| `LastRegistryError` | 2 | reads | yes | workspace.startup |
| `SetEventSender` | 1 | mutates | no: passes callback interface (session.EventSender) | none; replaced by workspace.attach subscription |

`SavedWorkspaceManagement` groups asynchronous `PreviewWorkspaceDeletion`,
`DeleteSavedWorkspace`, and `RestoreDeletedWorkspace` alongside Runtime. Connected
adapters use `workspace.delete_preview`, `.delete_saved`, and `.restore_deleted`
on the selected core, including SSH; they never inspect/move a shell-local data
directory. `SavedWorkspaceDeletionReceipts` provides exact receipt observation
after a lost acknowledgement. The core owns preview preimages/tokens, actor
retirement, activity refusal and recoverable moves; see
[deletion ownership](core-client-protocol.md#ownership-and-attachment).

### `WorkspaceStore`

WorkspaceStore is the workspace-level metadata database, reduced to what the shell reads.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `ListSessions` | 1 | reads | yes | session.list |

### `Agents`

Agents is the agent inventory, turn control and blocking-prompt surface.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `AgentMgr` | 87 | reads | no: live mutable state holder (replica type; clients own a local copy) | workspace topic (replica: coreclient.WorkspaceView.AgentMgr) |
| `PaneMgr` | 5 | reads | no: replica state holder | replica (coreclient.WorkspaceView.PaneMgr); pane state is shell-local derived data |
| `AdvanceAgentState` | 1 | mutates | no: returns AgentReduction effects consumed locally | none; runs on the core owner loop (replica receives workspace topic) |
| `AgentActivity` | 2 | reads | yes | replica: coreclient.WorkspaceView.AgentActivity |
| `AgentModelIdentity` | 2 | reads | yes | replica: coreclient.WorkspaceView.AgentModelIdentity |
| `AgentTurnActive` | 5 | reads | yes | replica: coreclient.WorkspaceView.AgentTurnActive |
| `PlanModeForAgent`, `GoalForAgent`, `LoopsForAgent` | session modes | reads | yes | agent snapshot replica |
| `SetPlanModeForAgent`, `GoalAction`, `LoopAction` | session modes | mutates | yes | `agent.plan_mode`, `agent.goal`, `agent.loop` |
| `UsageSummary` | 4 | reads | yes | replica: coreclient.WorkspaceView.UsageSummary |
| `YoloForAgent` | 6 | reads | yes | replica: coreclient.WorkspaceView.YoloForAgent |
| `SetYoloForAgent` | 1 | mutates | yes | agent.yolo |
| `ApplyAgentEvent` | 3 | mutates | no: core-side reducer with effects | none; core-side reducer. Replica preflights and installs workspace publications |
| `ApplyAgentStatusEvent` | 1 | mutates | no: core-side reducer | none; core-side reducer |
| `AppendAgentMessages` | 3 | mutates | partial: bulk message payload | agent.append_messages (explicit restore/control operation; ordinary ingress is core-owned) |
| `RegisterAgentRoot` | 1 | mutates | no: returns live *AgentNode | none; core-side inventory (session.create/open result) |
| `ForgetAgentInventory` | 1 | mutates | yes | none; core-side |
| `SyncAgentStatusInventory` | 1 | mutates | yes | none; core-side |
| `CancelAgent` | 1 | mutates | yes | agent.cancel |
| `CloseAgent` | 1 | mutates | yes | agent.terminate |
| `BeginAgentCancellation` | 2 | mutates | yes | agent.cancel |
| `SteerAgent` | 1 | mutates | yes | agent.steer |
| `ResolveQuestion` | 2 | mutates | yes | agent.decide |
| `RejectQuestion` | 1 | mutates | yes | agent.decide |
| `ResolveConfirmation` | 2 | mutates | yes | agent.decide |
| `PrepareAgentRecovery` | 1 | mutates | no: returns closure | agent.recover |
| `PrepareCancelAgentRecovery` | 1 | mutates | no: returns closure | agent.recover_cancel |
| `RejectAgentRecoveryCancellation` | 1 | mutates | yes | none: withdraws the shell's optimistic Stop; the core never entered one |
| `PrepareAsyncCommandTermination` | 1 | mutates | no: returns closure | agent.stop_command (exact AsyncCmdRef) |
| `PrepareCompactForAgent` | 1 | mutates | no: returns live PreparedCompaction | agent.compact (request progress, no live PreparedCompaction on wire) |
| `HydrateSubAgents` | 1 | mutates | yes | none (workspace topic delivers hydrated children) |
| `PrepareAgentInputAdmission` | 1 | mutates | no: returns *InputAdmissionIntent (opaque handle consumed by Commit/Apply) | agent.submit (intent/commit/apply collapse into one submit + request.receipt) |
| `CommitAgentInputAdmission` | 1 | mutates | partial: takes the opaque intent handle | agent.submit |
| `ApplyAgentInputAdmission` | 1 | mutates | partial: takes the opaque intent handle | agent.submit (client-side reducer on the receipt) |
| `PrepareSkillInput` | 1 | reads | yes | settings.capability.skill_input |

### `Sessions`

Sessions is the session registry, lifecycle and per-session settings surface.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `Sessions` | 1 | reads | no: returns live *Session | replica: coreclient.WorkspaceView.Sessions |
| `SessionsByID` | 2 | reads | no: live *Session map | replica: coreclient.WorkspaceView.SessionsByID |
| `SessionsByAgent` | 8 | reads | no: live *Session map | replica: coreclient.WorkspaceView.SessionsByAgent |
| `SessionForAgent` | 4 | reads | no: returns live *Session | replica: coreclient.WorkspaceView.SessionForAgent |
| `ListSessions` | 1 | reads | yes | session.list |
| `NewAgentSession` | 1 | mutates | partial: result is data, but the session it creates is a live object the replica must materialize | session.create |
| `NewAgentSessionAs` | 1 | mutates | partial: same as NewAgentSession, rooted in the requested role | session.create (role selection) |
| `EnsureAeronSession` | 1 | mutates | partial: the core reuses, resumes or creates the workspace's one Aeron; replica materializes it | session.ensure |
| `OpenAgentSession` | 1 | mutates | partial: same as NewAgentSession | session.open |
| `BackendSession` | 1 | reads | no: returns live *session.Session backend | none; live backend (replica exposes a settings snapshot instead: settings.session.read) |
| `SessionConnectionIssue` | 1 | reads | yes | registry/workspace topic replica |
| `PrepareMultiAgentToggle` | 1 | mutates | no: returns *SessionSettingsOperation (carries Run closure) | settings.session.toggle_multi_agent |
| `PrepareSessionCapabilityToggle` | 1 | mutates | no: returns *SessionSettingsOperation (carries Run closure) | settings.session.toggle_capability |
| `PrepareSessionSettingsSave` | 1 | mutates | no: returns *SessionSettingsOperation (carries Run closure) | settings.session.save |
| `PrepareSessionSettingsReset` | 1 | mutates | no: returns *SessionSettingsOperation capturing the current session layer, never a draft | settings.session.reset |
| `ApplySessionSettingsResult` | 2 | mutates | partial: result embeds owner pointers | settings.session.save (result applied to replica) |

### `Settings`

Settings loads, previews and persists application and workspace settings.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `LoadAppSettings` | 15 | reads | yes | settings.fetch (replica: WorkspaceView.LoadAppSettings) |
| `AppDefaults` | 3 | reads | yes | settings.fetch (replica: WorkspaceView.AppDefaults) |
| `LoadWorkspaceOverrides` | 3 | reads | yes | settings.fetch (overrides field) |
| `WorkspaceAgentDefaults` | 1 | reads | yes | settings.fetch (derived from overrides) |
| `SaveAccountChange` | 1 | mutates | yes | settings.account.change |
| `AllAccounts` | 6 | reads | yes | settings.fetch (accounts are redacted, no credentials) |
| `PreparePreferencesSave` | 1 | mutates | no: returns closure capturing scope and explicit field edits | settings.application.save / workspace.overrides_save |
| `ApplyPreferencesSave` | 1 | mutates | yes (data) | settings.application.save (result applied to replica) |
| `PrepareAccountModels` | 1 | mutates | no: returns closure | settings.account.models |
| `PrepareDraftAccountModels` | 1 | mutates | no: returns closure | settings.account.models |

### `CodeTools` and `ProjectTools`

These optional connected interfaces are discovered by type assertion, not added
as mandatory methods to every `Workspace`. Their blocking calls run on joined,
cancellable workers, never Update or paint. The remote adapters use the original
attachment's cancellable command exchange; detach revokes its native previews.

| Interface member | coreprotocol | Contract |
|---|---|---|
| `ReadCodeToolInventory(ctx, offset, limit)` | `code_tools.inventory` | Read managed details/files/accounting, current workspace unmanaged tools and a bounded history page. Also exposed as a semantic read-only command. |
| `PreviewCodeToolRemoval(ctx, ids, unused)` | `code_tools.remove_preview` | Retain an expiring owner/workspace-bound exact review; managed tools only. |
| `RemoveCodeTools(ctx, preview)` | `code_tools.remove` | Consume the unchanged native review once; recheck workspace references, store preimage and live executable leases. |
| `ReviewProjectTools(ctx, explicit)` | `code_tools.project_review` | Native review bound to current project/root, assembly, settings, catalog, decision history and shell owner. Automatic reads suppress remembered candidates; an explicit human action can freshly review still-missing refused/failed tools. Neither read installs anything. |
| `DecideProjectTools(ctx, previewID, selectedIDs)` | `code_tools.project_decide` | Consume the retained preview once; record the decision and run current-effect-fenced setup. Cannot supply replacement sources or an approval record. |

Setup decisions and removal preview/confirmation reject generic JSON/control
callers. Semantic control exposes inventory only: it cannot grant installation
or removal by calling a generic action or editing settings. Trust, stored setup
audit and live setup authority are distinct. See
[project setup](settings.md#project-tool-setup) and
[tool-store lifetimes](tool-store.md#executable-lifetimes).

Rail order, sidebar views, panel heights and the other presentation choices
are not a port: each shell keeps them in its private client profile (see
[client view memory](main-view-panes.md#client-view-memory)).

### `Files`

Files is the filesystem mutation and project-search surface.

`DeletePath(ctx, path, progress)` runs on the shell's joined I/O owner. Its
optional callback receives phases on that worker, never on Update. The connected
adapter uses the shared cancellation-and-join exchange, so switching workspaces
or shutting down retires the original request rather than abandoning its worker.
The core keeps the Local History safety-copy and shared-document checks.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `Documents` | 18 | reads | accessor (nil check) | document.* (live revision-fenced documents) |
| `EnsureDocuments` | 1 | mutates | no: takes event sink | none; core-owned (document.open creates) |
| `LSPMgr` | 75 | reads | accessor (nil check) | lsp.* (reads from detached snapshot) |
| `CreateFile` | 1 | mutates | yes | files.create |
| `CreateFolder` | 1 | mutates | yes | files.create_folder |
| `RenamePath` | 1 | mutates | yes | files.rename |
| `DeletePath` | 1 | mutates | yes | files.delete |
| `ApplyWorkspaceEdit` | 1 | mutates | yes | files.edit |
| `StartProjectSearch` | 1 | mutates | yes | files.search |
| `CancelProjectSearch` | 3 | mutates | yes | files.search (cancel via request cancellation) |

### `DocumentService`

DocumentService is the local/test and checked snapshot-save service seam. The
ordinary connected editor uses `LiveDocuments`, not these snapshot-save calls.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `AcceptDue` | 1 | reads | partial: consumes a shell timer message | none (shell timer message; pairs with document.snapshot_save) |
| `Cancel` | 6 | mutates | yes | none (shell-side autosave timer cancel) |
| `Save` | 1 | mutates | yes | document.snapshot_save (checked client buffer snapshot, not live document.save) |
| `Schedule` | 3 | mutates | yes (timer stays shell-side) | none (shell-side timer; fires document.snapshot_save) |

### `LSPService`

LSPService projects producer health and shared qualified diagnostic batches.
Only canonical live documents acquire/release source views and publish text;
the shell has no raw open/close/update/save or diagnostic-clear RPC path.
Generation/version cursors avoid unchanged-frame full snapshot copies.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `AllDiagnostics` | 3 | reads | yes | LSP state topic replica |
| `DocumentSymbols` | 1 | reads | yes | LSP state topic replica |
| `DiagnosticSnapshot` | 1 | reads | yes: non-destructive per-client cursor | LSP state topic replica + per-client consumer |
| `DiagnosticCursor` | current | reads | yes: generation/version only | LSP state topic replica |
| `ContextCursor` | current | reads | yes: retained owner/context generation, separate from progress/status | LSP state topic replica |
| `FileDiagnostics` | 2 | reads | yes | LSP state topic replica |
| `NotifyWatchedFilesChanged` | 1 | mutates | yes | lsp.notify_watched_files_changed |
| `ProjectInsights` | 3 | reads | yes | LSP state topic replica |
| `CompletionsState` | current | reads | yes: constant-size feature gate/generation | LSP state topic replica |
| `AssistanceCapabilities` | current | reads | yes: detached capabilities and synchronized source/context for the exact file | LSP state topic replica |
| `FileProducer` | current | reads | yes: exact open path and connected producer without copying trigger lists | LSP state topic replica |
| `FileSynchronized` | current | reads | yes: exact source/context admission hint without copying trigger lists | LSP state topic replica |
| `RequestDocumentSymbols` | 1 | reads | yes | lsp.document_symbols |
| `RequestHover` | 1 | reads | yes | lsp.hover |
| `RequestSignatureHelp` | current | reads | yes: detached qualified parameter information | lsp.signature_help |
| `Refresh` | 1 | mutates | yes | lsp.refresh (explicit Restart language servers / Problems Refresh only) |
| `RestartServer` | 1 | mutates | yes | lsp.restart_server |
| `ServerHealthSnapshots` | 3 | reads | yes | LSP state topic replica |
| `ServerReady` | current | reads | yes: selected status, not readiness of another file | LSP state topic replica |
| `SetActiveFile` | 1 | mutates | yes | lsp.set_active_file |
| `Settings` | 2 | reads | yes | LSP state topic replica |
| `ShowDiagnosticsInEditor` | 2 | reads | yes | LSP state topic replica |
| `ShowDiagnosticsInTree` | 1 | reads | yes | LSP state topic replica |
| `StartForWorkspace` | 1 | mutates | yes | lsp.start_for_workspace |
| `Status` | 6 | reads | accessor | LSP state topic replica |
| `Stop` | 1 | mutates | yes | lsp.stop (explicit action only, never detach) |
| `StopServer` | 1 | mutates | yes | lsp.stop_server |
| `Version` | 1 | reads | yes | LSP state topic replica |

Explicit semantic queries share the connected cancellable command exchange:
retiring a shell request cancels its exact enqueued core job and joins/ACKs the
original response, not merely the carrier stream. The cleanup lifetime remains
bound to that original client/workspace, including on switch or shutdown, and
reports retained uncertainty when its bounded join cannot be confirmed. It never
stops the shared server or retargets a replacement attachment. Reviewed edits share
that joined exchange but retain their own durable mutation intent and immutable
result; cancellation is not no-effect evidence. Other mutations keep their
ordinary receipt semantics. See
[command cancellation](core-client-protocol.md#mutation-receipts-and-uncertainty).

### `Navigation`

This optional connected capability replaces the old slice-only definition and
reference methods. The actor supplies attachment ownership. Both calls use the
joined cancellable command exchange and run off Update.

| Member | TUI | coreprotocol |
|---|---|---|
| `RequestLSPNavigation` | Exact acknowledged source, kind, UTF-16 position and references-declaration flag | `lsp.navigation` |
| `InspectLSPNavigationTarget` | Explicit original-boundary/wire/kind and selected target; observe one bounded current source | `lsp.navigation_target` |

Only qualified DTOs cross the port. Null, empty, unsupported and failed outcomes
are distinct. Target content is not fetched during listing. The shell verifies
an observation against its actual opened snapshot before landing; no missing
target version is inferred. See [qualified navigation](editor.md#qualified-location-navigation).

### `Completions`

This optional connected capability replaces the old slice-only completion method.
No shell-local provider, raw object or label-keyed resolve path remains.

| Member | TUI | coreprotocol |
|---|---|---|
| `RequestLSPCompletions` | Acknowledged source, private view/sequence, UTF-16 cursor and LSP trigger context | `lsp.completions` |
| `InspectLSPCompletion` | Exact original set ID/digest/item ID; documentation-only resolve or initial-edit validation | `lsp.completion_inspect` |
| `ReleaseLSPCompletions` | Fence the owner's private view through this sequence | `lsp.completion_release` |

All use the joined cancellable exchange off Update. `LSPService.CompletionsState`
reads the detached gate/feature generation, not a second settings owner. Only
initial core-normalized edits can reach the ordinary editor transaction;
documentation remains inert. See
[completion documentation](editor.md#completion-documentation-and-original-insertion).

### `SemanticTokens`

This optional read capability uses the joined cancellable exchange off Update.
The shell supplies only acknowledged source, a private view/sequence and visible
range. The core chooses the advertised full, delta or range request, retains its
own bounded delta base, and returns validated source-relative spans.

| Member | TUI | coreprotocol |
|---|---|---|
| `RequestLSPSemantic` | Source, private view and sequence, visible range | `lsp.semantic_tokens` |
| `ReleaseLSPSemantic` | Retire the exact private view through its sequence | `lsp.semantic_tokens_release` |

A definite busy refusal can retry with a bounded cooldown while the source and
view remain current; an uncertain outcome is not replayed. Release cannot retire
a newer view. See [semantic token colors](editor.md#semantic-token-colors).

### `InlayHints`

This optional connected read capability has no shell-local source/provider
fallback and uses the existing joined cancellable exchange off Update.

| Member | TUI | coreprotocol |
|---|---|---|
| `RequestLSPInlayHints` | Acknowledged source, private view ID/sequence and visible anchor range | `lsp.inlay_hints` |
| `InspectLSPInlayHint` | Original set ID/digest/index and explicit tooltip-only resolve | `lsp.inlay_inspect` |
| `ReleaseLSPInlayHints` | Fence the private view through this sequence without affecting a newer one | `lsp.inlay_release` |

`LSPService.InlayHintsState` reads the detached gate/feature epoch without cloning
all language settings. Source/provider/context evidence stays core-owned; hint
labels remain a private display projection and never confer edit, command or
navigation authority. See [inlay hints](editor.md#read-only-inlay-hints).

### `ReviewedEdits`

This optional connected-document capability never falls back to shell-local file
reads or typed protocol reconstruction. The core owns target selection, original
wire version interpretation and exact preview data. All calls run off Update.

| Member | TUI | coreprotocol |
|---|---|---|
| `PrepareLSPRename` | Read immutable review for acknowledged source/name/cursor | `lsp.rename_preview` |
| `ApplyReviewedEdit` | Approve only original opaque ID/digest | `lsp.edit_apply` |
| `ReleaseReviewedEdit` | Retire only pending exact-owner preview | `lsp.edit_release` |
| `InspectReviewedEdit` | Observe original stable-client result without renewed mutation authority | `lsp.edit_receipt` |

### `CodeActions`

This optional connected capability sends acknowledged source and source-byte
selection, never raw provider actions, reconstructed diagnostics or caller edits.
The core retains exact provider JSON and qualifies it at its synchronized source.
All three calls use the same cancellable original-command exchange. Preview is
read-only; mutations still require `ReviewedEdits` and its opaque review ID/digest.

| Member | TUI | coreprotocol |
|---|---|---|
| `RequestLSPCodeActions` | Read a detached, provenance-qualified manual list | `lsp.code_actions` |
| `PrepareLSPCodeAction` | Resolve/preview only the original set ID/digest/index | `lsp.action_preview` |
| `ReleaseLSPCodeActions` | Revoke an exact pending list and dependent Apply capabilities | `lsp.actions_release` |

Null/empty results are source/selection-scoped evidence, not project cleanliness.
Closing a list or an unattempted shared review retires its pending capabilities.
Releasing a list never discards committed/uncertain outcomes or retained Local
History safety. Bounds and unsupported forms:
[manual code actions](editor.md#manual-code-actions).

### `SourceControl`

SourceControl is the Git surface of a workspace.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `Git` | 6 | reads | accessor | git.* |
| `EnsureGit` | 6 | mutates | accessor | none; core-owned |
| `PreviewGitDiscard` | 1 | reads | yes | git.discard_preview |
| `DiscardGitReviewed` | 1 | mutates | yes | git.discard_reviewed |
| `DiscardGitHunk` | 1 | mutates | yes | git.discard_hunk |
| `PrepareCommitMessageWriter` | 1 | mutates | no: returns live streaming writer | git.commit_model / git.commit_message (core job + progress) |

### `GitService`

GitService owns source-control reads and mutations.

`Snapshot(ctx)` is an explicit fresh core read, including manual Refresh,
post-mutation status and the bounded watcher-loss fallback. Its immutable
`SourceID`/`Sequence` evidence fences delayed older push/RPC results. It is not a
read of the last shell topic cache. Stage/Unstage transfer exact selected paths
over stdin to native Git, not a potentially oversized argv or expanded pathspec.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `AnnotateFile` | 1 | reads | yes | git.annotate_file |
| `Checkout` | 2 | mutates | yes | git.checkout |
| `CommitStaged` | 1 | mutates | yes | git.commit_staged |
| `CompareChanges` | 1 | reads | yes | git.compare_changes |
| `InspectCommit` | 1 | reads | yes | git.inspect_commit |
| `Snapshot` | 4 | reads | yes | git.snapshot |
| `Stage` | 3 | mutates | yes | git.stage_paths |
| `StageHunk` | 1 | mutates | yes | git.stage_hunk |
| `StageReviewed` | 1 | mutates | yes | git.stage_reviewed |
| `Unstage` | 2 | mutates | yes | git.unstage_paths |
| `UnstageHunk` | 1 | mutates | yes | git.unstage_hunk |
| `UnstageReviewed` | 1 | mutates | yes | git.unstage_reviewed |

### `History`

History is the local-history surface of a workspace.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `LocalHistory` | 18 | reads | accessor | history.* |
| `HistoryService` | 10 | reads | accessor | history.plan_message / history.plan_agent |
| `AppendHistoryRestoreEvent` | 1 | mutates | yes | history.restore_event |
| `ApplyHistoryRestoreChoices` | 1 | mutates | yes | history.apply |

### `HistorySafety`

Optional native workspace retention administration. All calls run off Update.
The core derives evidence and attribution; inspection never grants the older
author's reviewed-edit mutation authority. The existing Local History window
owns the paged list/detail and the human-only exact-inspection confirmation.

| Member | TUI | coreprotocol |
|---|---|---|
| `ListHistorySafety` | Replace one bounded reference page, independent of timeline limits | `history.safety_list` |
| `InspectHistorySafety` | Read original outcome, verified snapshot coverage and current protection separately | `history.safety_inspect` |
| `ChangeHistorySafetyProtection` | Explicit native consent to inspected identities, revision and digest | `history.safety_change` |
| `HistorySafetyReceipt` | Observe the original disposition request, never repeat a mutation | `operation.receipt` |

### `LocalHistoryStore`

LocalHistoryStore reads immutable project snapshots and plans restores.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `FileHistory` | 1 | reads | yes | history.file |
| `PlanRestore` | 1 | reads | yes | history.plan |
| `PlanRestorePaths` | 1 | reads | yes | history.plan_paths |
| `Prune` | 1 | mutates | yes | history.prune |
| `Root` | 1 | reads | yes | none (path hint; fold into attach result) |
| `SnapshotIfChanged` | 1 | mutates | yes | history.capture |
| `Stats` | 3 | reads | yes | history.stats |
| `Timeline` | 2 | reads | yes | history.timeline |
| `VerifyAll` | 1 | mutates | yes | history.verify |

### `LocalHistoryService`

LocalHistoryService plans agent- and message-scoped restores.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `AgentUndoableScopes` | 1 | reads | yes | history.undo_scopes |
| `PlanAgentRestore` | 2 | reads | yes | history.plan_agent |
| `PlanMessageRestore` | 2 | reads | yes | history.plan_message |

### `Memory`

Memory is the workspace memory surface.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| embeds `MemoryStore` | - | - | - | - |
| `BindMemory` | 1 | reads | no: returns a bound handle | none (identity token) |

### `MemoryStore`

MemoryStore is the memory browse and curation surface.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `SearchMemoryEpisodes` | 1 | reads | yes | memory.episodes |
| `SearchMemories` | 1 | reads | yes | memory.search |
| `InspectMemory` | 1 | reads | yes | memory.inspect |
| `PinMemory` | 1 | mutates | yes | memory.pin |
| `ForgetMemoryRecord` | 1 | mutates | yes | memory.forget |
| `CorrectMemory` | 1 | mutates | yes | memory.correct |

### `Capabilities`

Capabilities is the skills, MCP and acquisition surface.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `CapabilityAction` | 2 | mutates | yes | settings.capability.action |
| `CapabilityRuntimeSnapshot` | 1 | reads | yes | settings.capability.runtime |
| `CapabilitySnapshot` | 1 | reads | yes | settings.capability.snapshot |

### `Collaboration`

Collaboration is the agent board surface.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `BoardSnapshot` | 1 | reads | yes | board.snapshot |
| `MarkBoardRead` | 1 | mutates | yes | board.mark_read |

### `TerminalHost`

TerminalHost exposes the in-app PTY services.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `Terminals` | 4 | reads | accessor | terminal.* |
| `EnsureTerminals` | 1 | mutates | accessor | none; core-owned (terminal.create creates) |
| `TermMgr` | 3 | reads | accessor | terminal-inventory state topic |

### `TerminalService`

TerminalService owns the one parser and reply state for every PTY.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `Acquire` | 2 | mutates | no: takes TerminalController interface (callback owner) | terminal.control |
| `Close` | 1 | mutates | yes | terminal.close |
| `Create` | 1 | mutates | yes | terminal.create |
| `Detach` | 1 | mutates | no: takes TerminalController interface | terminal.release |
| `Focus` | 1 | mutates | partial: takes opaque TerminalLease | ordered lease input stream |
| `Key` | 1 | mutates | partial: takes opaque TerminalLease | ordered lease input stream |
| `Mouse` | 1 | mutates | partial: takes opaque TerminalLease | ordered lease input stream |
| `Paste` | 1 | mutates | partial: takes opaque TerminalLease | ordered lease input stream |
| `Resize` | 1 | mutates | partial: takes opaque TerminalLease | ordered lease input stream |
| `Rows` | 2 | reads | yes | terminal.rows (bounded on-demand history/selection); normal viewport uses grid topic |

Connected asynchronous terminal services also expose `TerminalInventoryBarrier`.
Its `AwaitInventory(context.Context)` waits off Update for a post-call inventory
subscription value to be applied by the existing replica owner. Interactive
creation uses it before reporting a mounted terminal; direct SDK `Create` remains
a resource receipt and does not require a UI pump. Caller, workspace-switch and
attachment cancellation release the wait. Local synchronous managers need no
extra publication contract. This barrier never fetches or writes a second
inventory, and no terminal is recreated on a cancelled mount.

### `TerminalManager`

TerminalManager owns the PTY sessions.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `Get` | 5 | reads | yes | terminal-inventory state topic replica |
| `List` | 4 | reads | yes | terminal-inventory state topic replica |

### `StatusHost`

StatusHost exposes the workspace status center.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `Status` | 37 | reads | accessor | status state topic replica |

### `StatusService`

StatusService retains workspace-scoped operations, issues and notification history.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `Agent` | 3 | reads | yes | status state topic replica |
| `Agents` | 1 | reads | yes | status state topic replica |
| `Dismiss` | 2 | mutates (shared store) | yes | status.mutate |
| `FinishOperation` | 1 | mutates (shared store) | yes | status.mutate |
| `Hydrate` | 1 | mutates | yes | status state topic replica |
| `Issues` | 2 | reads | yes | status state topic replica |
| `LatestTransient` | 1 | reads | yes | shell-local transient |
| `MarkRead` | 3 | mutates (shared store) | yes | status.mutate |
| `Notifications` | 2 | reads | yes | status state topic replica |
| `Notify` | 6 | mutates (shared store) | yes | status.mutate |
| `ResolveIssue` | 3 | mutates (shared store) | yes | status.mutate |
| `SetIssue` | 2 | mutates (shared store) | yes | status.mutate |
| `SetOperation` | 2 | mutates (shared store) | yes | status.mutate |
| `UnreadCount` | 1 | reads | yes | status state topic replica |
| `Version` | 1 | reads | yes | status state topic replica |

## Members that cannot cross a process boundary as written

These signatures are local port contracts, not wire payloads. The connected adapter already replaces live objects/closures with typed requests, detached replicas or shell-local handles. This inventory records why raw signatures cannot be serialized, not a list of unimplemented methods.

| Member | Why |
|--------|-----|
| `Runtime.Logger` | Client-local sink, not a transferable logger object. |
| `Runtime.WorkspaceDB` | Bound service identity; no SQL handle crosses the wire. |
| `Runtime.SetEventBus` / `SetEventSender` | Client ingress wiring; core owns backend event delivery. |
| `Agents.AgentMgr` / `PaneMgr` | Detached client state holders; never shared runtime pointers. |
| `Agents.AdvanceAgentState` / `ApplyAgentEvent` / `ApplyAgentStatusEvent` | Core-only reducers; remote methods do not re-run them. |
| `Agents.AppendAgentMessages` | Detached evidence via `agent.append_messages`, not a conversation pointer. |
| `Agents.RegisterAgentRoot` | Existing replicated node; registration remains core-owned. |
| `Agents.PrepareAgentRecovery` / `PrepareCancelAgentRecovery` | Client closures issue fenced `agent.recover*` RPCs off Update. |
| `Agents.PrepareAsyncCommandTermination` | Captured command identity via `agent.stop_command`. |
| `Agents.PrepareCompactForAgent` | Client waiter around `agent.compact`; no live compactor crosses the wire. |
| `Agents.PrepareAgentInputAdmission` / `CommitAgentInputAdmission` / `ApplyAgentInputAdmission` | Local intent binds one `agent.submit` result and exact draft receipt. |
| `Sessions.Sessions*` / `SessionForAgent` | Replica sessions without backend/database ownership. |
| `Sessions.NewAgentSession` / `NewAgentSessionAs` / `OpenAgentSession` | `session.create/open` identities; replica materializes their state later. |
| `Sessions.EnsureAeronSession` | `session.ensure` returns the workspace's one Aeron (reused, resumed or created); replica materializes it like any root. |
| `Sessions.BackendSession` | Always unavailable in connected shells. |
| `Sessions.PrepareMultiAgentToggle` / `PrepareSessionCapabilityToggle` / `PrepareSessionSettingsSave` / `PrepareSessionSettingsReset` / `ApplySessionSettingsResult` | Captured typed settings RPC/result; local owner pointers stay client-side. |
| `Settings.PreparePreferencesSave` | Off-UI typed scoped set/remove edits; awaits durability, last explicit write wins. |
| `Settings.PrepareAccountModels` / `PrepareDraftAccountModels` | Captured `settings.account.models` request with write-only secret semantics. |
| `Files.EnsureDocuments` | Client-local timer facade over the core disk-save authority. |
| `DocumentService.AcceptDue` | Consumes only that shell's exact timer epoch. |
| `LSPService.DiagnosticSnapshot` | Non-destructive replica data with independent client cursor. |
| `ReviewedEdits` | Core-owned immutable preview and ID/digest-only Apply; no raw `WorkspaceEdit` crosses to the shell. |
| `SourceControl.PrepareCommitMessageWriter` | Lazy client writer; core resolves Aeron and streams request-scoped progress. |
| `Memory.BindMemory` | Attachment-bound service, not a memory database pointer. |
| `TerminalService.Acquire` / `Detach` | Core-issued controller lease / exact release, not a client callback. |
| `TerminalService.Focus` / `Key` / `Mouse` / `Paste` / `Resize` | Typed intents carrying the exact core controller lease; acknowledged off Update. |

## Connected ownership and remaining boundaries

Protocol v2 uses generated bounded MessagePack tuples on independent command,
topic, input and bulk streams. The local/SSH/TLS mux and QUIC carrier share one
retained-session contract; no port object, callback or database handle is a wire
value. See [wire and delivery](core-client-protocol.md#wire-format-and-bounded-delivery).

- `StatusService` reads its independent latest-state topic and uses typed
  `status.mutate` operations; transients remain shell-local. Settings, registry,
  LSP, status, Git, Board and terminal inventory have independent state topics,
  not periodic fetches or blocking calls on Update.
- Agent inventory, semantic event reduction and ticks run only on the core actor.
  Acknowledged subscriptions post detached `ReplicaChangesMsg` batches; Update
  preflights the complete publication and commits atomically before releasing its
  applied ACK. A repeated invalid publication fails after bounded reset, rather
  than looping. `StartReplica` follows sink installation.
  Pane renderers bind canonical replica Conversations; snapshots/resync preserve
  surviving identities. Pending root activation waits for its replica and peer
  roots do not steal editor focus.
- Session create/open runs off-UI. Workspace switching prepares an attachment to
  the destination before replacing client-local state, then closes the old
  connection without stopping peer work. Failed destination preparation retains
  the original shell attachment.
- LSP requests are typed and ordered in core resource worker lanes. The shell uses
  `DiagnosticSnapshot` / `LSPDiagnosticConsumer`, never a destructive shared
  diagnostic drain. Rename uses a core-retained reviewed plan and selected safety
  snapshot, never a shell-owned file edit or raw protocol DTO. Document views are per-client; detach releases
  those views without stopping servers.
- `LiveDocuments` Open/Edit/Close/Save and optional Format/receipt commands
  carry `DocumentOwner` attachment epochs. Each open owns a document topic with
  snapshot, revision operations and savepoint state. Update commits a prepared
  canonical/private rebase atomically; ordinary edits and saves do not call
  `SyncDocument`. Sync remains explicit recovery/shutdown infrastructure. Warm
  resume retains cursors; cold reset protects pending private work as conflict.
  Topic tokens fence close/reopen, resolution and workspace-generation changes.
  Reconnect observes exact receipts and never replays uncertain mutations.
  Core SQLite retains accepted unsaved text,
  author undo and savepoints. Stable IDs survive Path rename; dirty/conflicted
  files are protected by durable batch intents.
- Core owns project and lazy document-directory watchers. Connected tree browsing,
  catalog/search/read and replace planning use typed RPC, not local project reads.
  Explicit desktop effects use bounded client-local materialized caches.
- Private composer drafts use `clientstate` and an exclusive stable client profile;
  prompt history uses core RPC and `history.InstallReplica` for local reads.
  Existing development drafts require manual backed-up alignment, not a legacy
  product reader. References/media use typed transfer and exact owner fences.
- Settings saves, Reset, promotion and palette shortcuts await typed durable
  results. They carry scoped set/remove edits, not a full resolved preference
  snapshot or configuration revision. Exact consent/grant and resource-lifetime
  safeguards remain separate from ordinary last-write-wins configuration.
- Replica `*workspace.Session` objects have no backend. `BackendSession` must not
  expose a remote execution owner. `WorkspaceDB` is an identity token, not a
  client-accessible SQL handle. `Local` does not add locking and is not a second
  interactive CLI startup path.

`RemoteManager` is supplied by the shell host, not the selected core. It owns the
local connection catalog, credential access, Test/Connect/Disconnect, remote
workspace browsing and exact-backend switching. `RemoteAccessAdmin` is a separate
selected-core port, gated by authenticated immutable LocalHost provenance; it
cannot be granted by naming a connection Local. Per-form and backend generations
fence asynchronous results.

The public shell attaches through `ConnectCore`, the Remotes manager, or strict
system SSH selected with `--ssh HOST --workspace REMOTE_PATH` and optional
user/port/identity flags. Native-platform, distinct-host and full-process acceptance
require separate evidence; see the [protocol reference](core-client-protocol.md).
