# Local History

> Status: Current reference — immutable workspace snapshots, operation-scoped agent undo and precondition-checked restore.

## Storage and recovery

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
- Continuing an accepted human input reuses its existing message scope and
  original safety snapshots through a continuation lease. It never inserts a
  second scope with the same identity or rewrites its before/after snapshots.
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
  not visit leave the cache. Restore planning never uses it and reads every file.

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
