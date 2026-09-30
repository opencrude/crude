# Message-Scoped Undo and Workspace Local History Plan

> Status: LANDED - end-to-end Local History system completed on 2026-09-01;
> agent-scoped provenance hardening landed on 2026-09-14:
> content-defined storage, SQLite timeline/index recovery, recursive watcher and
> reconciliation, message/child attribution, collision handling, bounded text
> merge, immutable preview plans, partial/exact restore, safety rollback,
> retention/GC, settings, timeline/file/message UX, active-agent `/undo`, context
> actions, refresh events, and append-only provider context notification.

## 1. Product Intent

Crude needs IDE-grade local history, not an operation stack.

The user-facing contract is:

1. There is one slash command: `/undo`.
2. Every durable user chat message can expose an `Undo changes from this
   message` context-menu action.
3. Undo is preview-first. The user sees affected files, diffs, conflicts, and
   the exact restore plan before the filesystem changes.
4. Restoring history is itself undoable.
5. Agent edits, shell-command side effects, editor saves, file-tree operations,
   and external filesystem writes are retained by the same workspace history.
   Only a direct file-tool operation with an agent identity and a durable
   write-ahead receipt is eligible for agent-scoped undo.
6. Generated and reproducible content is not tracked by default.
7. The full conversation and provider context history remain immutable. Undo adds a
   new event; it never deletes or rewrites chat history and therefore does the
   least possible damage to cache-prefix continuity.

This feature should feel closer to JetBrains Local History than to `git reset`
or an editor Ctrl+Z stack.

## 2. Current State and Why It Is Insufficient

The current `fileio.Journal` is an append-only JSONL operation journal with
whole-file compressed preimages. `fileio.Editor.WithMessage` already associates
direct Crude file operations with a message ID, and `UndoMessage` exists.

That is useful migration data, but it is not the target architecture.

The `localhistory/` package provides the target storage/restore foundation:

- `github.com/restic/chunker` v0.5.0 content-defined chunks with SHA-256
  identities (BSD-2-Clause; small, pure Go, maintained by the restic project)
- compressed, header-versioned chunk objects and path-independent file objects
- immutable hash-addressed snapshot manifests and restore plans
- default derived-directory, `.gitignore`, and `.crudehistoryignore` filtering
- byte/mode/link snapshots, corruption verification, and partial previews
- current-state precondition checks between preview and apply
- staged object validation, a safety snapshot before mutation, automatic
  rollback on failure, and an `after_restore` snapshot
- workspace-owned manual capture/preview/apply methods under the external
  per-workspace `local-history/` directory

The workspace service adds asynchronous baseline capture, recursive fsnotify,
debounced external snapshots, bounded reconciliation, message scopes, child
quiescence, conservative concurrent attribution, settings-driven retention,
and restore suppression. `/undo`, the command palette, user-message context
actions, and Explorer file history all open the same native preview system.
Legacy numeric/all undo commands are rejected; session-owned journals are
read-only compatibility data and no longer receive new entries.

Current limitations:

- Only writes routed through `fileio.Editor` are guaranteed to be recorded.
- `execute_command`, build tools, generators, scripts, LSP actions, user edits,
  and external processes can bypass the journal.
- Repeated edits store repeated preimages instead of content-addressed blobs.
- `/undo n` exposes internal operation ordering rather than user intent.
- `/undo-all` is too destructive and has no review boundary.
- Reverting entries sequentially can partially mutate the tree before a later
  conflict fails.
- There is no workspace timeline, retention policy, file-level history, safety
  snapshot, restore preview, collision model, or redo of a restore.
- The global prompt `history` package is unrelated. It stores composer prompt
  recall and must remain separate from filesystem local history.

## 3. Non-Negotiable Invariants

### 3.1 Immutable history

- Snapshots and manifests are immutable.
- Restore never deletes history.
- A restore captures a safety snapshot first.
- A failed restore leaves the worktree exactly as it was before restore began.
- The manifest commit is atomic: either the complete snapshot exists or none
  of it is visible.
- Local History syncs its staged files and publishes through the same
  `fileio.CommitAtomicFile` primitive as document/file writes. Unix retains
  rename followed by parent-directory fsync; Windows uses write-through
  replacement. No read-only directory flush, ignored access-denied error, or
  delete-then-rename fallback is permitted. Genuine commit failures remain
  fail-closed at the root turn's tracking boundary.

### 3.2 Durable attribution

- Every submitted user message has one stable `message_id` persisted in the
  canonical conversation snapshot.
- Every root turn has one `session_id`, `message_id`, and optional agent IDs.
- Child agents inherit the root user message ID that caused their work.
- A file version may record multiple owners when concurrent actors touched it.
- Ambiguous ownership is displayed as a collision and never silently assigned
  to one agent or message.
- Watcher timing, declared file ownership, and raw tool arguments are not
  sufficient proof of a writer. Before its first filesystem mutation, a direct
  tool durably records exact preimages and intended postimages for every file
  endpoint. If the receipt cannot be persisted, the tool does not mutate.
- Agent undo reverses those operation receipts, never the message baseline. An
  external/editor/command change made before the direct tool is therefore part
  of its preimage and survives undo. Later non-overlapping text edits use a
  strict reverse three-way merge; overlaps, binary changes, and later ownership
  by another agent fail closed behind an explicit conflict.
- The receipt is a write-ahead record with `prepared`, `committed`, and
  `aborted` states. File tools compare their preimage again after WAL commit and
  immediately before replacement; aborted receipts never become undo entries.
  Move/copy endpoints and operation-created parents are one atomic selection,
  so a conflicted endpoint blocks the whole operation chain.
- Selective tools reject existing hardlinked files until an operation receipt
  can represent the complete link topology. Full workspace restore preserves
  hardlinks and verifies both content and inode identity.

### 3.3 Cache-safe conversation semantics

- Undo never removes or edits a user message, assistant message, tool result,
  provider-control context entry, or prior compacted context generation.
- The restore result is appended as a new canonical event.
- If the model needs to know about the restore, a short context-only event is
  appended after the accepted provider history.
- Undo does not force context compaction; the configured per-agent threshold
  remains the only automatic compaction trigger.
- Filesystem blobs and manifests never enter model context automatically.

### 3.4 Restore safety

- Preview and apply use the same immutable restore plan ID.
- Apply rejects the plan if any selected current file changed after preview.
- Current file hashes, modes, symlink targets, and existence are preconditions.
- Every restore writes through temp files plus atomic rename where supported.
- Deletes happen only after all replacement blobs validate successfully.
- Symlinks are restored as links and may never escape the workspace root.
- Binary files use exact replacement; text files may use conflict-aware merge.

### 3.5 Layer ownership

- `workspace/` owns local-history lifecycle, settings resolution, attribution,
  snapshot scheduling, preview, restore, retention, and events.
- A new storage package owns content-addressed blobs and immutable manifests.
  It has no TUI imports.
- `session/` publishes root message/turn ownership boundaries but does not own
  workspace filesystem history.
- `tui/` resolves a clicked canonical message ID, calls workspace methods, and
  renders preview/results. It never restores files itself.

## 4. Open-Source Go Foundation Decision

### 4.1 Selected primitives

Use mature, importable Go components rather than shelling out to another CLI:

- [`github.com/restic/chunker`](https://github.com/restic/chunker) for
  content-defined chunking. It is the chunking foundation used by restic and
  avoids storing a full new blob when a large file changes slightly.
- The existing
  [`go-git` gitignore package](https://pkg.go.dev/github.com/go-git/go-git/v5/plumbing/format/gitignore)
  for Git-compatible ignore semantics. `go-git` is already a Crude dependency.
- The existing [`fsnotify`](https://github.com/fsnotify/fsnotify) dependency for
  change detection, supplemented by bounded scans because filesystem watchers
  can overflow or miss events.
- SQLite for transactional snapshot manifests, labels, attribution, restore
  plans, retention metadata, and collision records.
- SHA-256 initially for blob/chunk identity. Hash choice is versioned in the
  object header so a later algorithm can coexist.

### 4.2 Reference implementation

[`undo-anything`](https://github.com/agenticraptor/undo-anything) is an MIT Go
project whose architecture closely matches the product intent: immutable
manifests, content-addressed compressed objects, Git-style ignores, safety
snapshots, timeline, retention, and atomic restore. It is a useful reference
and compatibility target.

It is not selected as a direct dependency in phase 1 because its reusable code
is currently under Go `internal/` packages and the project is very new. The
dependency spike must review its object format and tests; reusable MIT code may
be adapted with attribution if it passes the quality gate.

### 4.3 Rejected alternatives

- Kopia is mature and robust but introduces a complete encrypted backup
  repository, policy, maintenance, and storage stack. That is excessive for a
  per-workspace IDE history and makes message attribution harder to control.
- A hidden Git repository does not capture ignored/untracked work cleanly,
  interacts badly with the user's repository semantics, and stores binary or
  generated churn poorly.
- Continuing the current JSONL journal cannot safely capture shell/external
  side effects or provide atomic tree restores.

### 4.4 Dependency acceptance spike

Before product integration, benchmark the selected primitives against:

- 100,000 source files with 95 percent unchanged between snapshots.
- A 1 GB workspace with `node_modules` and Cargo `target` present but ignored.
- A 100 MB file with small edits at the beginning and end.
- Snapshot interruption and object corruption.
- Windows rename, symlink, path-length, and locked-file behavior.

The spike must record dependency license, activity, security posture, transitive
dependency size, memory use, and restore correctness.

## 5. Storage Architecture

### 5.1 Location

Store history outside the project tree:

```text
~/.crude/workspaces/<workspace-id>/local-history/
  objects/
  history.db
  tmp/
```

The store must never create `.undo`, `.history`, or other product artifacts in
the user's repository.

Permissions are owner-only. Objects are local and are not uploaded by Crude.

### 5.2 Object model

```text
ChunkObject
  format_version
  hash_algorithm
  compression
  uncompressed_size
  payload

FileObject
  path-independent content identity
  ordered chunk hashes
  whole-file hash
  size

SnapshotManifest
  snapshot_id
  parent_snapshot_id
  workspace_id
  created_at
  kind
  label
  entries[path] -> {file_object, mode, type, symlink_target}
```

Objects are written to a temp file, synced, hash-verified, and atomically
renamed. Existing objects are reused.

### 5.3 SQLite metadata

Proposed logical tables:

```text
history_snapshots
history_snapshot_entries
history_message_labels
history_actor_scopes
history_restore_plans
history_restore_events
history_retention_runs
```

Important relationships:

- One user `message_id` has a before snapshot and zero or more after snapshots.
- One snapshot can be shared by several labels without duplicating objects.
- A restore event references both the source snapshot and its safety snapshot.
- Message attribution is independent from the active context generation.

## 6. Capture Model

### 6.1 Workspace baseline

When local history is enabled for a workspace:

1. Resolve ignore policy.
2. Capture an initial baseline.
3. Start the watcher.
4. Periodically run a bounded reconciliation scan to recover watcher overflow
   or changes made while Crude was closed.

### 6.2 User-message boundary

Before a root user message is admitted to an agent turn:

1. Flush pending watcher changes.
2. Capture or reuse a `before_message` snapshot.
3. Label it with `session_id` and stable `message_id`.
4. Open an attribution scope for the root message.

When the root turn and all children caused by it become quiescent:

1. Debounce watcher events.
2. Reconcile the filesystem.
3. Capture an `after_message` snapshot.
4. Compute changed paths between before and after.
5. Store actor attribution where known.

Long-running or asynchronous children keep the root scope open. Interim
snapshots are allowed, but the message boundary closes only after ownership is
terminal or explicitly detached.

### 6.3 Capturing shell and external changes

The watcher captures changes regardless of whether they came from:

- Crude file tools.
- `execute_command` or background commands.
- LSP code actions.
- The built-in editor.
- Another process or editor.

Direct Crude file operations still publish intent metadata. That improves
attribution and avoids unnecessary scans, but it is not the source of truth.
For agent undo, exact operation preimages and intended postimages are committed
to the content-addressed store before mutation. Watcher-only, opaque-command,
editor, and external changes remain available in workspace timeline history but
never become an agent undo candidate without equivalent write-ahead provenance.
Blocking commands remain blocking; asynchronous command trees are lifecycle-
owned, joined on shutdown, and block restores while running.
Human turns, detached children, and child-wake continuations retain a shared
activity lease. Restore owns the exclusive side, eliminating check-then-admit
races. A durable restore transaction rolls back interrupted process death, and
provider-context notification is a protected, idempotent outbox retried before
the next provider turn and on resume.

### 6.4 Concurrent attribution

Each active root message/agent scope records:

- declared file ownership
- direct file-tool paths
- process start/end interval
- watcher event interval

If attribution is not exclusive, the path is marked shared. Message-scoped undo
may preview it but must not auto-select it as safe.

## 7. Ignore and Derived-Content Policy

Ignore decisions happen before hashing or reading file contents.

Default ignored paths include:

```text
.git/
node_modules/
target/
dist/
build/
out/
.next/
.nuxt/
.turbo/
.cache/
coverage/
__pycache__/
.pytest_cache/
.mypy_cache/
.venv/
venv/
bin/
obj/
```

Rules:

- Honor nested `.gitignore` rules by default, including negation.
- Add `.crudehistoryignore` for local-history-only exclusions.
- App and workspace settings can add patterns.
- Workspace settings can disable `.gitignore` inheritance.
- Never ignore an explicitly tracked source file merely because a directory
  name is common; negation and explicit include rules win.
- Store directories, sockets, devices, FIFOs, and files above the configured
  size limit are excluded.
- Derived-content defaults are versioned and visible in Settings.

## 8. Settings Model

Add `LocalHistorySettings` to app settings with field-level workspace
overrides, matching the existing app -> workspace inheritance model.

Proposed fields:

```text
enabled
use_gitignore
extra_ignore_patterns
max_file_size_mb
debounce_ms
reconcile_interval_seconds
retention_keep_hours
retention_keep_daily
retention_max_snapshots
storage_budget_mb
capture_external_changes
safety_snapshot_before_restore
```

Settings UI requirements:

- A dedicated Local History category.
- Effective ignore-pattern preview.
- Current object count, snapshot count, logical bytes, physical bytes, and
  deduplication ratio.
- `Prune now`, `Open history location`, and `Verify store` actions.
- Changes to capture/ignore settings trigger an explicit new baseline; they do
  not reinterpret old manifests.

## 9. Undo Semantics

### 9.1 One slash command

Keep only `/undo`.

`/undo` finds the latest undoable scope for the active agent and opens its
restore preview. It never restores immediately. The preview contains only the
selective inverse of that agent's direct file-tool operation receipts; it never
includes watcher-only workspace changes.

A successful restore never rewrites accepted conversation history. It appends
one hidden runtime-context event and one permanent `history_restored` context
boundary at the exact timeline position where filesystem state changed. The
boundary uses the compaction table language, records operation/target/safety
IDs and restored-path count, is static after completion, survives resume, and
offers the safety-snapshot inverse through its context menu. The restore modal
closes after success so the boundary is immediately visible. Its rule and body
rows participate in the shared chat drag-selection and clipboard path.

If the active agent has no verified file changes, it reports that clearly. If
several concurrent scopes are candidates, it opens a message picker instead of
guessing.

Remove:

- `/undo-n`
- `/undo-all`
- numeric `/undo N` parsing

The command palette `system.undo` invokes the same preview flow.

Editor Ctrl+Z and `edit.undo` remain editor-buffer undo and are unrelated.

### 9.2 User-message context menu

Right-clicking a rendered message requires canonical message hit testing:

```text
Renderer.MessageAt(localY) -> {message_id, role}
```

For `role=user`, add a Local History section:

- `Preview changes from this message`
- `Undo changes from this message`
- `Open workspace history at this point`

`Undo changes` still opens preview; the label communicates the intended target,
not immediate execution.

Items are disabled with a useful detail when:

- the message has no snapshot label
- the turn is still active
- history is disabled
- the store is unavailable or corrupt
- every changed path is ambiguous

### 9.3 Preview

The preview is a modal floating IDE surface and shows:

- message excerpt and timestamp
- before and after snapshot IDs
- files added, modified, deleted, moved, or type-changed
- subsequent modifications since the message
- collisions and unsafe paths
- per-file diff/disclosure
- selectable files
- estimated restore bytes

Actions:

- `Restore selected`
- `Restore all safe`
- `Cancel`

There is no unreviewed `Restore everything` shortcut when conflicts exist.

### 9.4 Restore algorithm

1. Revalidate the preview plan and current hashes.
2. Capture a safety snapshot of the current tree.
3. Materialize and verify every required object in a staging directory.
4. Compute text merges for files changed after the target message.
5. Refuse unresolved conflicts unless the user explicitly selects exact
   replacement for that file.
6. Apply writes/renames atomically where possible.
7. Apply deletes last.
8. Reconcile watcher state.
9. Capture an `after_restore` snapshot.
10. Append a visible conversation status and a short hidden provider-wire event.

If apply fails, restore from the safety snapshot before reporting failure.

## 10. Cache and Conversation Interaction

Undo is append-only from the model's perspective.

The following are forbidden:

- removing the original user message
- removing tool calls/results caused by that message
- editing canonical snapshots
- rebuilding earlier provider messages
- compacting the active context only because files were restored

After a successful restore, append a small hidden message such as:

```text
<workspace_history_event>
Restored 4 files to the state before message msg-42.
Restore event: restore-17. Safety snapshot: snapshot-91.
</workspace_history_event>
```

This tells a future model call that the filesystem changed while preserving the
accepted provider prefix. Full diffs stay in local-history storage and are read
only when requested.

## 11. Migration and Removal Plan

### Phase 0: dependency and format spike — DONE

- Benchmark and review selected Go dependencies.
- Freeze versioned object and manifest formats.
- Add crash/fault-injection test harness.

Exit gate: deterministic snapshot/restore across Linux, macOS, and Windows;
license and security review recorded.

### Phase 1: content-addressed store — DONE

- Implement chunk/object storage and immutable manifests.
- Implement Git-compatible ignore resolver.
- Implement snapshot, diff, verify, and retention APIs.
- No product restore UI yet.

Exit gate: property tests prove snapshot -> mutate -> restore is byte/mode/link
identical; corrupt objects are detected.

### Phase 2: workspace history service — DONE

- Mount service in `workspace/`.
- Add baseline, watcher, reconciliation, and settings.
- Capture direct, shell, editor, and external changes.
- Add storage statistics and pruning.

Exit gate: shell-created/deleted/renamed files appear in history; generated
directories do not.

### Phase 3: message and agent attribution — DONE

- Use canonical user `message_id` as the root scope.
- Propagate root message ID to every child.
- Persist direct `(operation_id, session_id, message_id, agent_id)` write-ahead
  receipts with every path's exact preimage and intended postimage before the
  first filesystem mutation.
- Operation receipts cover every direct agent mutation allowed by its policy,
  both inside and outside the project. Workspace paths use relative keys;
  external paths use canonical absolute keys and are explicitly visible in the
  preview. Restore safety/after manifests carry selected external paths,
  including absence, so the safety-snapshot inverse is complete.
- Build selective inverse plans from operation preimages, with strict reverse
  text merge for non-overlapping later edits. Do not treat claims, raw
  arguments, watcher timing, or opaque command effects as agent-owned changes.
- Add before/after labels, collision detection, and agent/message-to-files query
  APIs.

Exit gate: one root message with parallel children produces one reviewable
change set, with ambiguous paths marked.

### Phase 4: preview and atomic restore — DONE

- Add immutable restore plans.
- Add safety snapshots and rollback-on-failure.
- Add text merge/conflict handling and exact binary restore.
- Publish workspace events for buffer/tree/git refresh.

Exit gate: injected failure at every apply step leaves the original tree
recoverable and the store valid.

### Phase 5: product UX — DONE

- Add canonical chat-message hit testing.
- Add per-user-message context actions.
- Add local-history preview modal and timeline.
- Change `/undo` and command palette to open the latest-message preview.
- Add settings category and store diagnostics.

Exit gate: keyboard and mouse flows produce the same preview and restore plan;
completed/resumed sessions retain message associations.

### Phase 6: legacy retirement — LANDED

- Stop writing new JSONL journal entries after the new service is proven.
- Offer a one-time import of legacy journal preimages as labeled snapshots where
  reconstruction is unambiguous.
- Keep legacy journals read-only for one compatibility release.
- Remove `/undo-n`, `/undo-all`, `UndoN`, and `UndoSession` product paths.
- Remove the old journal after migration telemetry and tests are clean.

## 12. Test and Acceptance Matrix

### Correctness

- create, overwrite, delete, move, copy, chmod, symlink, binary, empty file
- nested directories and case-only renames
- shell command changes outside `fileio.Editor`
- external-editor changes
- process crash during object, manifest, preview, and restore phases
- restore of restore
- partial selection restore
- concurrent agents touching disjoint and shared files
- an external write after a successful agent file tool is never auto-selected;
  it appears only as a guarded conflict or a strict non-overlapping reverse merge
- resumed session message attribution

### Ignore policy

- Cargo `target`
- `node_modules`
- nested `.gitignore` with negation
- custom workspace excludes/includes
- file-size limit
- symlink to ignored/outside path

### Cache safety

- provider context before undo is an exact prefix after undo
- no context compaction from restore alone
- original user/tool messages remain byte-identical
- only one hidden workspace-history event is appended

### Performance

- initial baseline latency and memory on large repositories
- incremental snapshot latency after one small edit
- physical/logical byte ratio
- watcher storm coalescing
- restore preview latency
- restore apply and rollback latency

### Security

- workspace traversal and symlink escape
- object hash mismatch
- malformed manifest
- untrusted ignore patterns
- locked/read-only files
- store permissions
- no history content in logs, prompts, or telemetry

## 13. Completion Criteria

The plan is complete only when:

1. `/undo` is the sole agent-file undo command and always previews.
2. Every durable user message can resolve its history label after restart.
3. Right-click undo and `/undo` produce the same restore plan.
4. Shell and external file changes are captured.
5. Derived directories are excluded by effective settings.
6. Restores are atomic or automatically rolled back.
7. A restore can itself be undone.
8. Full conversation and filesystem history remain immutable.
9. Undo appends to the active provider epoch without mutating its prefix.
10. Race tests, fault injection, full tests, lint, docs, and cross-platform
    restore suites pass.

## 14. Explicit Non-Goals

- Replacing Git or creating commits.
- Cloud backup or cross-device synchronization.
- Exposing local-history blobs to model providers.
- Treating editor Ctrl+Z as workspace history.
- Automatically restoring ambiguous concurrent changes.
- Tracking reproducible dependency/build output by default.
