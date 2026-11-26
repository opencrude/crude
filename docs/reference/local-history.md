# Local History

> Status: Current reference — immutable workspace snapshots, operation-scoped agent undo and precondition-checked restore.

Top-level folders default to no Local History. Opening one does not create a
history store or a baseline. An explicit Workspace settings choice can enable
capture if the profile is outside the workspace. The existing store containment
and mandatory safety checks still apply. See [workspace entry defaults](settings.md#workspace-entry-defaults).

## Storage and recovery

- Atomic publication has one shared low-level owner: `fileio.CommitAtomicFile`.
  Local History stages and syncs its own files, then delegates replacement and
  platform durability. Unix keeps rename plus parent-directory fsync; Windows
  uses write-through replacement, never `Sync` on a read-only directory handle
  or a delete-then-rename fallback. Real permission/write/commit errors propagate
  and still abort tracked turns before provider or tool execution.
- Startup recovery reads only manifests/plans missing from the SQLite index.
  Snapshot identity and its path index commit atomically; never replay all
  historical paths on open. LoadManifest/LoadPlan still verify immutable data on
  use.
- The content-addressed manifest is the only record of a snapshot's entries
  (type, mode, hash, size, link targets); its hash proves them. SQLite keeps
  only which paths each snapshot contains: `history_snapshot_keys` gives each
  snapshot an integer sequence, `history_index_paths` stores each path name
  once, and `history_snapshot_paths` is a `WITHOUT ROWID` table keyed by
  (snapshot, path). Rows are clustered by snapshot, so recording appends one
  contiguous range and retention deletes one. Do not add an index keyed by
  path: it scatters every capture and prune over the whole history. File
  history scans the compact table instead (about 70 ms for 1.1 million rows).
  The earlier full copy of every entry per snapshot grew one store to 787 MB;
  deleting 37 snapshots held the store's only connection for 16 to 22 s, and
  each capture wrote about 10,000 wide rows. The same history now takes
  12.6 MB, a 10,000-path capture indexes in about 35 ms and each pruned
  snapshot commits in about 35 ms.
- `localhistory.Store` owns immutable objects/manifests/plans and SQLite index
  metadata. `workspace.LocalHistoryService` owns watcher lifecycle, settings,
  message scopes, reconciliation, and restore suppression. TUI only calls
  workspace methods and renders neutral plans.
- Continuing an accepted human input reuses its existing message scope and
  original safety snapshots through a continuation lease. It never inserts a
  second scope with the same identity or rewrites its before/after snapshots.
  If retention removed a finished scope before a delegated or compacted
  continuation starts, the continuation opens a fresh scope for that message
  (checked under the Store gate, so it cannot race retention) and closes it
  when its last lease ends. File tools never fail because a scope expired.
- With Local History off, direct file tools proceed unrecorded; the mutation
  hook returns `session.ErrHistoryDisabled` instead of failing the tool.
- Never write history inside the project, follow a symlink parent, or apply a
  plan whose current hash/mode/type differs from preview. Safety snapshots are
  mandatory; every failure after mutation rolls back before returning.
- Snapshot, SQLite, verification, pruning, and restore work always runs in a
  command/background service, never Bubble Tea `Update`. Project switch and
  shutdown wait for active restore ownership.
- Integration apply/undo capture their safety snapshot and register its existing
  message scope under one Store/process mutation gate (`SnapshotMessageScope`).
  Retention cannot prune the preimage between those steps. Prepared, committed
  and restart-ambiguous `execution_apply`/`execution_undo` operation journals keep
  their scope's before/after snapshots and operation paths even after the scope
  closes; aborted operations and ordinary closed scopes remain prunable.
  Retention verifies referenced execution evidence before deleting anything.
  A missing index, manifest or object fails closed; a new snapshot never repairs
  an old receipt's immutable identity.
- Non-agent reviewed editor mutations use retained snapshot references, not
  fabricated agent/message attribution. Capture and reference registration share
  the Store/process mutation gate with pruning. Exact references survive modal
  close, detach, cancellation and restart ambiguity; release is explicit.
  Referenced index, manifest, entries and content objects are verified before
  pruning and before startup orphan recovery. Missing or corrupt evidence fails
  closed instead of being repaired by a later snapshot.
- `SnapshotRetainedFiles` stores only supplied immutable core-reviewed preimages:
  at most 200 canonical relative regular-file keys, 2 MiB each and 16 MiB total.
  It validates the native root and bounded Git administration metadata, but never
  walks the tree, loads ignore files or reads the selected source contents.
  The mutation owner must separately prove actual native preimages before commit.
  Ordinary `SnapshotRetained` remains a full snapshot; `IncludePaths` still means
  force inclusion, never selection.
- Selected manifests carry explicit `ScopePaths`. Only those keys observe
  presence/absence; a missing out-of-scope entry is unknown, never a deletion.
  Default restore and path-subset planning directly read only selected keys,
  reject unsafe/nonregular/oversized replacements and preserve unrelated files.
  Restore safety/after snapshots retain the same bounded operation coverage through
  the existing transaction and rollback owner. Diff only compares jointly
  observed keys. Full snapshots have no selected scope and keep their ordinary
  complete-workspace semantics.
- A stored snapshot reuses the previous scan's entry for a file whose size,
  mtime, change key and file identity are unchanged, whose object still exists,
  and whose observation is older than the timestamp granule (a same-granule
  write is invisible to metadata, so such files are re-read). Paths the walk did
  not visit leave the cache. Linux and macOS use the inode change time to
  detect same-size writes that restore the original mtime. Restore planning
  never uses the cache and reads every file.
- A scan walks directories in order and gives the per-file metadata calls (type,
  ignore rule, identity, cached object) to at most eight workers, as Git's
  preloaded index does; content reads, cache updates and hardlink detection stay
  in walk order. On Windows each such call costs tens of microseconds, and
  identity opens a file for attributes only, so the antivirus filter does not
  scan it and a file without read sharing still has an identity.
- Ignore files are read in one walk, parent before child, with the priority
  defaults < Git exclude and `.gitignore` files (deeper last) < extra patterns
  and `.crudehistoryignore`. Like Git, the walk does not enter an ignored
  directory, so an ignore file inside it cannot bring its contents back.
- `SnapshotIfChanged` requests that wait while another capture holds the store
  share the next scan, which starts after each of them asked. Each still records
  its own snapshot when the entries changed. Concurrent agents therefore wait for
  at most two scans at a message boundary, not one scan per agent. A caller that
  stops waiting leaves the other requests queued for the next holder.
- `Close` saves the stat cache to `stat-cache.json` in the store and `Open`
  loads it, so the first capture after a core restart reads only changed files.
  Saved entries pass the same metadata, timestamp-granule and object checks as
  entries kept in memory; a missing, damaged or older file is ignored.

## Reset disposable test history

On Linux and Windows test machines, skip index conversion if you do not need
old snapshots, restore points, undo records, or safety evidence. Close all
shells and stop the core on the machine that owns the workspace with
`crude --core-stop`. Check `crude --core-status` before removing anything. Use
the same build and `CRUDE_HOME` as the running core.

On Linux, preview the exact folders and remove them only if they are disposable:

```sh
crude_home=${CRUDE_HOME:-"$HOME"}
find "${crude_home}/.crude/workspaces" -mindepth 2 -maxdepth 2 -type d -name local-history -print
# After checking the list:
find "${crude_home}/.crude/workspaces" -mindepth 2 -maxdepth 2 -type d -name local-history -exec rm -rf -- {} +
```

On Windows, use PowerShell:

```powershell
$homeDir = if ($env:CRUDE_HOME) { $env:CRUDE_HOME } else { $env:USERPROFILE }
Remove-Item -Path (Join-Path $homeDir '.crude\workspaces\*\local-history') -Recurse -Force -WhatIf
```

Remove `-WhatIf` only after checking the list. Both examples use the standard
`.crude` directory name. A beta build uses `.crude-beta`; a source build with
no `CRUDE_HOME` uses the `.crude-dev` profile inside your home directory. Match
that profile before running the removal command.

Crude creates a fresh store when each workspace next opens. Do not remove the
workspace directory itself: it holds data such as sessions and chat history.

## Retention cost

When snapshot timestamps match, retention keeps the last committed snapshot
first. Snapshot IDs do not determine which snapshot is newer. A canceled
capture does not record a snapshot from a scan shared with other requests.

- Reconciliation and retention are maintenance. They hold the Store/process
  mutation gate that message scopes and every direct file tool's operation
  receipt wait on, so they never run while an agent turn (message scope or
  continuation lease) is active. They run on the reconcile tick only after
  agents have been idle for `maintenanceQuietPeriod` (30 s), prune only when a
  snapshot was added since the last pass, and yield: starting a turn or a
  restore cancels the maintenance context. Watcher captures also wait while
  agents work; changed paths stay pending and the turn's after-message
  snapshot or the next gap records them.
- Retention removes each generation in its own transaction and stops between
  generations when maintenance yields, so another store user waits for at most
  one generation. Foreground work takes the store from maintenance before its
  first query: a turn's message scope and the safety copy before a delete both
  start as work.
- The serialized workspace owner never queries the history store. A session
  lifecycle worker reads a resumed session's pending restore notifications;
  the owner only installs them, and a created or forked session reads nothing.
  Waiting on the store from the owner froze every request for 8 s while
  retention held its connection.
- Retention must stay cheap: re-reading every protected snapshot's content on
  each pass once held the gate for over a minute per pass in a real store.
- Protected evidence is content-verified once per open Store. Retention keeps
  that proof for immutable content-addressed objects and protected snapshots;
  later passes still check the index row and manifest presence and fail closed
  when they are gone. Garbage collection resolves chunk references through the
  same proofs and evicts proofs for objects it deletes. Explicit
  Verify/VerifyAll, loading a retained snapshot for use, and restore always read
  content again.
- Over budget, retention removes the oldest unprotected generations in
  shrinking batches (a quarter of the remaining candidates, at least one) and
  measures the store after each collection. It never removes every generation
  at once: that discarded the last hours of history and the scopes running
  agents still recorded into.
- The storage budget measures retained content: objects, manifests, plans and
  SQLite's in-use pages. Free pages left by pruned snapshots and the WAL/shm
  sidecars are reusable working space, never budget usage; counting them kept a
  store permanently over budget and discarded all ordinary history each pass.
  The WAL is truncated to 64 MiB after checkpoints.

## Restore and agent undo

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
  reject or silently omit an otherwise authorized direct mutation merely because
  its path is external. Managed execution separately bounds native writes to its
  exact checkout and the owning agent's private scratch directory; Local History
  does not widen that execution or file-ownership authority.
- Deleting from the file tree first records a `safety` snapshot that includes
  the deleted path, so the file or folder can be restored even when Git ignores
  it. `files.delete` uses the actual native target in the destination checkout's
  store. A semantic delete in a managed checkout uses its bound execution ID,
  never a same-name file in the logical workspace. If Local History is on but
  the store cannot open or cannot keep the target, nothing is deleted. An
  unavailable store is not the same as turning History off. With Local History
  off, delete proceeds and the prompt does not promise a copy.
  Delete safety uses a subtree-scoped manifest (`ScopeRoots`): it walks only
  the deleted file/folder, not the project or its ignore files. It captures
  ignored descendants and large files through the ordinary object store.
  Restore preview, safety capture, rollback and after-restore capture retain
  that coverage; absent siblings are unknown, never deletion targets. Ordinary
  full snapshots and bounded reviewed-edit `ScopePaths` keep their own semantics.
- Restores append one hidden `<workspace_history_event>` to provider context and
  one visible canonical `history_restored` context-boundary table without
  rewriting accepted conversation prefix. The completed boundary is static,
  survives resume, closes the restore modal, and exposes its safety-snapshot
  inverse from the row context menu; never render it through `ActivityDot`.

## Reviewed-edit safety protection

Local History exposes **Review safety references** above the snapshot timeline.
Its independent keyset traversal defaults to 100 rows and accepts at most 200 per
page, so references are reachable beyond the ordinary 500-row timeline. Each
page reports the active retained-reference count and whether more references
remain. This is bounded traversal, not an atomic multi-page inventory or a
freed-space estimate. A summary never authorizes release.

The detail reader separates the original persisted operation outcome from the
current protection disposition. It exposes the exact reference and snapshot,
original client/epoch, reviewed preimages and preview, integrity blockers and the
latest immutable protection audit event. Its read-only native TextArea supports
scrolling and selection. **Restore preview** uses the existing restore planner;
changing protection never edits project files or invokes restore.
Escape follows the native reader's back ladder: clear a text selection, return
from detail to the reference list, then return to the ordinary history timeline.
Closing the containing window retires the reader directly.

**Release protection…** requires a durably committed or positively no-effect
refused reviewed-edit journal, its original preview digest and exact selected
preimage manifest/object coverage. Prepared, unconfirmed, missing, corrupt and
unattributed evidence remains protected. Current disk bytes, age, detach and an
absent process cannot prove a historical outcome. A replacement native workspace
shell may inspect and administer this retention evidence; it does not acquire
the original author's Apply/InspectReviewedEdit authority.

Release and **Keep protected again** use an exact-inspection confirmation with
Cancel focused and outside clicks ignored. They are human-only actions, including
the shared confirmation buttons; generic control grants cannot approve them.
Only opaque inspected identities, revision and digest cross back as consent.
The core derives original evidence and current actor/request attribution, holding
the document-to-history exclusion order through the Store transaction.

`history_retained_snapshots` remains the sole current pin owner.
`history_safety_protection_events` appends audit evidence without a snapshot
foreign key: each exact release/re-protection event and pin transition commits
atomically, and the audit survives ordinary snapshot pruning without re-pinning
it. Original edit receipts and snapshot bytes are never rewritten. Audited
references cannot be recycled or released through the unaudited low-level API.
Release does not delete a snapshot immediately or defeat other protections.
Re-protection verifies the same original snapshot while it remains available;
it never reconstructs a pruned snapshot from present files.

A lost disposition reply retains **Check original result**, not Retry or a new
mutation. It observes the original core request receipt and, for an ambiguous
Store acknowledgement, a matching immutable event as positive commitment
evidence. Missing evidence never becomes a no-effect verdict. Closing the reader
does not settle a pending mutation; workspace switch is refused while its
original outcome is unresolved, and shutdown cancels and joins its exact command.
