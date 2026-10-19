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
| `agents.go` | `Agents` | agent inventory replica, turn control, blocking prompts, input admission |
| `sessions.go` | `Sessions` | session registry replica, create/open, per-session settings |
| `settings.go` | `Settings`, `ToolLayoutStore` | app/workspace settings, accounts, tool layout |
| `files.go` | `Files` | file mutations, project search; carries `Documents()` and `LSPMgr()` accessors |
| `documents.go` | `DocumentService` | checked snapshot save infrastructure |
| `live_documents.go`, `live_document_commands.go` | `LiveDocuments`, formatter/receipts/command interfaces | universal connected editor lifecycle, sync, exact-owner commands |
| `lsp.go` | `LSPService` | language-server manager |
| `git.go` | `SourceControl`, `GitService` | Git reads and mutations |
| `history.go` | `History`, `LocalHistoryStore`, `LocalHistoryService` | Local History |
| `memory.go` | `Memory`, `MemoryStore` | memory browse and curation |
| `capabilities.go` | `Capabilities` | skills / MCP / acquisition |
| `collaboration.go` | `Collaboration` | agent Board |
| `terminals.go` | `TerminalHost`, `TerminalService`, `TerminalManager` | PTY services |
| `status.go` | `StatusHost`, `StatusService` | workspace status center |
| `local.go`, `local_delegates.go` | `Local`, `Unwrap` | in-process adapter over `*workspace.Workspace` |

## Conventions

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
  one `Local(ws)` value is a stable token. `BindMemory()`, `BindToolLayout()` and
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
  state is delivered by `workspace.attach` / `workspace.snapshot` /
  `agent.changes` and read from `coreclient.WorkspaceView`, or `none` for local-only/core-internal behavior. Closure/object signatures remain local port contracts; only detached typed payloads cross the wire.

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
| `SetEventBus` | 1 | mutates | no: passes event sink | none; replaced by workspace.attach subscription + workspace.changed |
| `SwitchProject` | 1 | mutates | yes | workspace.detach + workspace.attach (shell re-attaches to the target workspace) |
| `TakeStartupIssues` | 1 | reads | yes | workspace.startup |
| `LastRegistryError` | 2 | reads | yes | workspace.startup |
| `SetEventSender` | 1 | mutates | no: passes callback interface (session.EventSender) | none; replaced by workspace.attach subscription |

### `WorkspaceStore`

WorkspaceStore is the workspace-level metadata database, reduced to what the shell reads.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `ListSessions` | 1 | reads | yes | session.list |

### `Agents`

Agents is the agent inventory, turn control and blocking-prompt surface.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `AgentMgr` | 87 | reads | no: live mutable state holder (replica type; clients own a local copy) | workspace.attach / workspace.snapshot / agent.changes (replica: coreclient.WorkspaceView.AgentMgr) |
| `PaneMgr` | 5 | reads | no: replica state holder | replica (coreclient.WorkspaceView.PaneMgr); pane state is shell-local derived data |
| `AdvanceAgentState` | 1 | mutates | no: returns AgentReduction effects consumed locally | none; runs on the core owner loop (replica receives agent.changes) |
| `AgentActivity` | 2 | reads | yes | replica: coreclient.WorkspaceView.AgentActivity |
| `AgentModelIdentity` | 2 | reads | yes | replica: coreclient.WorkspaceView.AgentModelIdentity |
| `AgentTurnActive` | 5 | reads | yes | replica: coreclient.WorkspaceView.AgentTurnActive |
| `UsageSummary` | 4 | reads | yes | replica: coreclient.WorkspaceView.UsageSummary |
| `YoloForAgent` | 6 | reads | yes | replica: coreclient.WorkspaceView.YoloForAgent |
| `SetYoloForAgent` | 1 | mutates | yes | agent.yolo |
| `ApplyAgentEvent` | 3 | mutates | no: core-side reducer with effects | none; core-side reducer. Replica side is WorkspaceView.ApplyChanges (agent.changes) |
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
| `RejectAgentRecoveryCancellation` | 1 | mutates | yes | agent.recover_reject |
| `PrepareAsyncCommandTermination` | 1 | mutates | no: returns closure | agent.stop_command (exact AsyncCmdRef) |
| `PrepareCompactForAgent` | 1 | mutates | no: returns live PreparedCompaction | agent.compact (request progress, no live PreparedCompaction on wire) |
| `HydrateSubAgents` | 1 | mutates | yes | none (workspace.snapshot / agent.changes deliver hydrated children) |
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
| `OpenAgentSession` | 1 | mutates | partial: same as NewAgentSession | session.open |
| `BackendSession` | 1 | reads | no: returns live *session.Session backend | none; live backend (replica exposes a settings snapshot instead: settings.session.read) |
| `SessionConnectionIssue` | 1 | reads | yes | session.snapshots replica |
| `PrepareMultiAgentToggle` | 1 | mutates | no: returns *SessionSettingsOperation (carries Run closure) | settings.session.toggle_multi_agent |
| `PrepareSessionCapabilityToggle` | 1 | mutates | no: returns *SessionSettingsOperation (carries Run closure) | settings.session.toggle_capability |
| `PrepareSessionSettingsSave` | 1 | mutates | no: returns *SessionSettingsOperation (carries Run closure) | settings.session.save |
| `PrepareSessionSettingsReset` | 1 | mutates | no: returns *SessionSettingsOperation capturing the current session layer, never a draft | settings.session.reset |
| `ApplySessionSettingsResult` | 2 | mutates | partial: result embeds owner pointers | settings.session.save (result applied to replica) |

### `Settings`

Settings loads, previews and persists application and workspace settings.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `BindToolLayout` | 2 | reads | no: returns a bound handle | none (identity token); see ToolLayoutStore |
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

### `ToolLayoutStore`

ToolLayoutStore persists rail and panel layout for one workspace database.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `LoadToolLayout` | 1 | reads | yes | workspace.tool_layout |
| `SaveToolLayout` | 1 | mutates | yes | workspace.tool_layout_save |

### `Files`

Files is the filesystem mutation and project-search surface.

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
| `ApplyLSPRename` | 1 | mutates | yes | files.edit (typed lsp.rename supplies WorkspaceEdit) |
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

LSPService is the workspace language-server manager.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `AllDiagnostics` | 3 | reads | yes | lsp.snapshot replica |
| `ClearDiagnostics` | 1 | mutates | yes | lsp.clear_diagnostics |
| `CloseFile` | 2 | mutates | yes | lsp.close_file |
| `DocumentSymbols` | 1 | reads | yes | lsp.snapshot replica |
| `DiagnosticSnapshot` | 1 | reads | yes: non-destructive per-client cursor | lsp.snapshot replica + per-client consumer |
| `FileDiagnostics` | 2 | reads | yes | lsp.snapshot replica |
| `FindReferences` | 1 | reads | yes | lsp.references |
| `NotifyFileSave` | 2 | mutates | yes | lsp.notify_file_save |
| `NotifyWatchedFilesChanged` | 1 | mutates | yes | lsp.notify_watched_files_changed |
| `OpenFile` | 2 | mutates | yes | lsp.open_file |
| `ProjectInsights` | 3 | reads | yes | lsp.snapshot replica |
| `QueueFileUpdate` | 1 | mutates | yes | lsp.queue_file_update |
| `RequestCompletions` | 1 | reads | yes | lsp.completions |
| `RequestDefinition` | 1 | reads | yes | lsp.definition |
| `RequestDocumentSymbols` | 1 | reads | yes | lsp.document_symbols |
| `RequestHover` | 1 | reads | yes | lsp.hover |
| `RequestRename` | 1 | mutates | yes: typed *protocol.WorkspaceEdit | lsp.rename (typed *protocol.WorkspaceEdit) |
| `RestartServer` | 1 | mutates | yes | lsp.restart_server |
| `ServerHealthSnapshots` | 3 | reads | yes | lsp.snapshot replica |
| `ServerReady` | 2 | reads | yes | lsp.snapshot replica |
| `SetActiveFile` | 1 | mutates | yes | lsp.set_active_file |
| `Settings` | 2 | reads | yes | lsp.snapshot replica |
| `ShowDiagnosticsInEditor` | 2 | reads | yes | lsp.snapshot replica |
| `ShowDiagnosticsInTree` | 1 | reads | yes | lsp.snapshot replica |
| `StartForWorkspace` | 1 | mutates | yes | lsp.start_for_workspace |
| `Status` | 6 | reads | accessor | lsp.snapshot replica |
| `Stop` | 1 | mutates | yes | lsp.stop (explicit action only, never detach) |
| `StopServer` | 1 | mutates | yes | lsp.stop_server |
| `Version` | 1 | reads | yes | lsp.snapshot replica |

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
| `TermMgr` | 3 | reads | accessor | terminal.list |

### `TerminalService`

TerminalService owns the one parser and reply state for every PTY.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `Acquire` | 2 | mutates | no: takes TerminalController interface (callback owner) | terminal.control |
| `Close` | 1 | mutates | yes | terminal.close |
| `Create` | 1 | mutates | yes | terminal.create |
| `Detach` | 1 | mutates | no: takes TerminalController interface | terminal.release |
| `Focus` | 1 | mutates | partial: takes opaque TerminalLease | terminal.input |
| `Key` | 1 | mutates | partial: takes opaque TerminalLease | terminal.input |
| `Mouse` | 1 | mutates | partial: takes opaque TerminalLease | terminal.input |
| `Paste` | 1 | mutates | partial: takes opaque TerminalLease | terminal.input |
| `Resize` | 1 | mutates | partial: takes opaque TerminalLease | terminal.resize |
| `Rows` | 2 | reads | yes | terminal.rows |

### `TerminalManager`

TerminalManager owns the PTY sessions.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `Get` | 5 | reads | yes | terminal.list |
| `List` | 4 | reads | yes | terminal.list |

### `StatusHost`

StatusHost exposes the workspace status center.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `Status` | 37 | reads | accessor | status.snapshot replica |

### `StatusService`

StatusService retains workspace-scoped operations, issues and notification history.

| Member | Sites | TUI | RPC-able as-is | coreprotocol |
|--------|------:|-----|----------------|--------------|
| `Agent` | 3 | reads | yes | status.snapshot replica |
| `Agents` | 1 | reads | yes | status.snapshot replica |
| `Dismiss` | 2 | mutates (shared store) | yes | status.mutate |
| `FinishOperation` | 1 | mutates (shared store) | yes | status.mutate |
| `Hydrate` | 1 | mutates | yes | status.snapshot replica |
| `Issues` | 2 | reads | yes | status.snapshot replica |
| `LatestTransient` | 1 | reads | yes | shell-local transient |
| `MarkRead` | 3 | mutates (shared store) | yes | status.mutate |
| `Notifications` | 2 | reads | yes | status.snapshot replica |
| `Notify` | 6 | mutates (shared store) | yes | status.mutate |
| `ResolveIssue` | 3 | mutates (shared store) | yes | status.mutate |
| `SetIssue` | 2 | mutates (shared store) | yes | status.mutate |
| `SetOperation` | 2 | mutates (shared store) | yes | status.mutate |
| `UnreadCount` | 1 | reads | yes | status.snapshot replica |
| `Version` | 1 | reads | yes | status.snapshot replica |

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
| `Sessions.NewAgentSession` / `OpenAgentSession` | `session.create/open` identities; replica materializes their state later. |
| `Sessions.BackendSession` | Always unavailable in connected shells. |
| `Sessions.PrepareMultiAgentToggle` / `PrepareSessionCapabilityToggle` / `PrepareSessionSettingsSave` / `PrepareSessionSettingsReset` / `ApplySessionSettingsResult` | Captured typed settings RPC/result; local owner pointers stay client-side. |
| `Settings.BindToolLayout` | Attachment-bound RPC service, not a database object. |
| `Settings.PreparePreferencesSave` | Off-UI typed scoped set/remove edits; awaits durability, last explicit write wins. |
| `Settings.PrepareAccountModels` / `PrepareDraftAccountModels` | Captured `settings.account.models` request with write-only secret semantics. |
| `Files.EnsureDocuments` | Client-local timer facade over the core disk-save authority. |
| `DocumentService.AcceptDue` | Consumes only that shell's exact timer epoch. |
| `LSPService.DiagnosticSnapshot` | Non-destructive replica data with independent client cursor. |
| `LSPService.RequestRename` | Typed `WorkspaceEdit`; old untyped edit/callback/drain port is removed. |
| `SourceControl.PrepareCommitMessageWriter` | Lazy client writer; core resolves Aeron and streams request-scoped progress. |
| `Memory.BindMemory` | Attachment-bound service, not a memory database pointer. |
| `TerminalService.Acquire` / `Detach` | Core-issued controller lease / exact release, not a client callback. |
| `TerminalService.Focus` / `Key` / `Mouse` / `Paste` / `Resize` | Typed intents carrying the exact core controller lease; acknowledged off Update. |

## Connected ownership and remaining boundaries

- `StatusService` uses `status.snapshot` and typed `status.mutate` operations;
  transients remain shell-local. Settings/session/LSP/status/terminal registry
  reads are replicas, not blocking calls on Update.
- Agent inventory, semantic event reduction and ticks run only on the core actor.
  The collector coalesces notifications, workers fetch detached data, and Update
  alone installs `ReplicaChangesMsg`. `StartReplica` follows sink installation.
  Pane renderers bind canonical replica Conversations; snapshots/resync preserve
  surviving identities. Pending root activation waits for its replica and peer
  roots do not steal editor focus.
- Session create/open runs off-UI. Workspace switching prepares an attachment to
  the destination before replacing client-local state, then closes the old
  connection without stopping peer work. Failed destination preparation retains
  the original shell attachment.
- LSP requests are typed and ordered in core resource worker lanes. The shell uses
  `DiagnosticSnapshot` / `LSPDiagnosticConsumer`, never a destructive shared
  diagnostic drain. Rename returns `*protocol.WorkspaceEdit`; no callback or
  untyped edit crosses the wire. Document views are per-client; detach releases
  those views without stopping servers.
- `LiveDocuments` Open/Sync/Edit/Close/Save and optional Format/receipt commands
  carry `DocumentOwner` attachment epochs. Update installs deltas and optimistic
  state; commands run off-UI. Reconnect reopens views, observes exact receipts and
  never replays uncertain mutations. Core SQLite retains accepted unsaved text,
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

The public shell attaches through `ConnectCore`, or system SSH selected with
`--ssh HOST --workspace REMOTE_PATH` and optional user/port/identity flags.
Native Windows and distinct-host acceptance remain pending.
See the [active plan](../plans/core-shell-separation.md) and
[protocol reference](core-client-protocol.md) for release evidence and limits.
