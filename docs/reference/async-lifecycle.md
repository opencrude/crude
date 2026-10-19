# Async Ownership and Lifecycle

> Status: Current reference — UI-thread I/O, generation boundaries, shutdown ordering and resource lifetimes.

Every asynchronous operation has an owner, a generation, cancellation, a
timeout, stale-result rejection and user-visible failure semantics. Moving
blocking work into a different callback without those is not an asynchronous
architecture. Terminal and network lifetimes are specified in
[in-app terminals](in-app-terminals.md) and [network transport](network.md).

## The Update goroutine

- **`EventBus.Send` is synchronous.** Never call it from a command that is
  already executing on Bubble Tea's `Update` goroutine. File mutations return
  `FileOperationResultMsg` directly, terminal close is command-owned, and OSC
  title publication runs from a `tea.Cmd`; reintroducing an event echo on any
  of those paths can deadlock `Program.Send`. Manual tree mutations use
  `ApplyLocalMutation`; only real background watcher events use
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
- **Blocking PanelDecks shield pointer input as well as keys.**
  `blockingDeckShieldsPointer` consumes coordinates outside the deck before
  root-Host hit testing, cancels stale underlying captures, and permits only a
  deck-owned drag to release outside its slot.
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

- **Comparison preparation and search have explicit owners.** Git change sets,
  historical commits, disk/saved and shared/private snapshots carry tab/request
  and workspace generations. Superseding or closing cancels their contexts;
  shutdown also joins queued and running commands. Repository reads wait on a
  cancellable serialization lane, not an uninterruptible mutex behind a remote
  operation. Diff failure settles every affected surface, and an empty successful
  set is a completed clean state. Find scans immutable presentation snapshots off
  Update through one shared Git/local search projection; late results cannot
  change a closed reader or a newer query. See the bounds and pure-library
  cancellation limitation in [main view panes](main-view-panes.md#git-and-local-comparisons).
  Width-calibration reflow uses the same joined command owner, with one new
  geometry value per exact immutable source. Only Update installs it; pending
  readers suppress old-column paint and pointer selection. Replacement, repeated
  calibration, closed views, workspace boundaries and shutdown fence publication.
  Restoring a failed workspace switch schedules a fresh pass, never revives the
  cancelled command. Search highlights are discarded and re-derived with the
  retained query after geometry publication.

## Generation boundaries and shutdown

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

## Data roots and retained resources

- **`buildinfo.Home` is the one crude data root.** `CRUDE_HOME` gives an
  isolated profile (own core, config, workspaces, sessions) for developing crude
  inside crude; never call `os.UserHomeDir` for crude data paths. Display-only
  `~` abbreviation keeps the real home.
- **Detached cores run from a staged image.** `spawnCore` executes
  `~/.crude/bin/crude-core-<build-id>`, never the starting executable, so
  `go run`/rebuilds can delete or replace it on Windows while the core lives.
  Publish one closed candidate by no-replace hard link; concurrent losers reuse
  the immutable winner, never rename another writable inode over it (ETXTBSY).
  Invalid type/size/mode or unsupported hard-link filesystems fail explicitly.
- **Windows process control retains both signal and output ownership.** The
  detached core clears the inherited `CREATE_NEW_PROCESS_GROUP` Ctrl+C-ignore
  policy before spawning terminal children, without joining a shell console.
  On child exit, ConPTY closes its pseudoconsole exactly once while the parser
  drains final output; the manager publishes exited state only after EOF.
  Resize refuses a closing console, and pseudoconsole close never holds a lock
  required by the parser/output consumer.
- **Session retention has one owner.** `session.Close` prunes an empty session
  only when its DB holds no messages/queue/recovery and
  `collaboration.RetireUnreferencedSession` proves no run/event/delivery/
  artifact/claim/child refers to its agents (it then removes those directory
  rows). The workspace forgets a `Pruned()` session's registry row and name;
  it never re-registers a deleted DB. The legacy file journal is removed: undo
  is Local History operation receipts only.
- **Launcher workspace scans are bounded and leak nothing.**
  `readWorkspaceMetaBounded` aborts both the read and its goroutine's channel
  send on timeout, so a stale read never leaks an open SQLite handle (which kept
  `workspace.db` locked on Windows). It closes its `Rows` before the count query:
  the pool holds one connection, and an open `Rows` made the count wait on
  itself until the bound expired for every workspace.

- **Workspace navigation has one catalog and one switch intent.** Home and the
  Recent Workspaces rail consume one generation-owned, cancelable catalog read;
  home unmount does not discard navigation. Durable `last_accessed_at` metadata is
  recorded on successful core attachments (including returns to retained actors),
  never session creation, background work, catalog reads or DB initialization.
  Unknown access times sort deterministically after known visits; no inferred
  session-time fallback. Every different-workspace UI route first shows the same
  exact-destination confirmation with Cancel focused, then starts the switch
  lifecycle. A tabless destination recreates home even if a session unmounted it.
  Cancel flows through the core resource
  lane and bounded metadata workers. A connected switch retains old editor views
  through dirty decisions and waits for pending edits/history/saves asynchronously.
  Exact editor/draft seals reject changed work before adoption. Esc advances the
  attachment sequence, so a context-ignoring backend cannot apply a cancelled
  result. Prepared attachments are command-owned until a validated Update claims
  them; context cancellation/timeout closes unclaimed candidates off Update even
  if the ready message was not delivered. Post-program teardown joins these exact
  preparations before flushing and detaching the old workspace.

