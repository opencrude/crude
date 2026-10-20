# Broad Workspace UX and Resource Bounds

> Status: IN PROGRESS. Design and implementation plan only; no product behavior in this document has landed.

## Outcome

Starting Crude in home, Desktop, Downloads, or another large folder must not
accidentally start an expensive recursive workload. Users should immediately
understand the folder they are opening, choose a project easily, and configure
the few relevant preferences in the existing Settings window.

Normal projects must remain frictionless. A project does not need Git. Choosing
a small workspace reduces accidental scope; neither the chooser nor scan
exclusions are a filesystem sandbox for commands or MCP tools.

This work does not repair LSP, introduce semantic indexing, redesign Settings,
rewrite the undo engine, or promise that all process memory is capped by a
file-count setting.

## Engineering precedent: the settings refactor

Reviewed commit 9d4ffcd, "persist only explicit settings field edits", against
its predecessor 536b1d6, and checked the current implementation at 9784e98.

Before the refactor, ordinary preference requests carried whole settings or
overlays and an expected configuration revision. An unrelated change could
reject Save with "preferences changed" or "reopen settings before saving".
Resolved values could also be re-diffed against a parent instead of preserving
the user's explicit choices.

The current approach is the model for this feature:

- Forms submit only the fields actually edited, through `types.SettingEdit`.
- Existing app locks and workspace SQLite transactions patch the latest stored
  layer. Different fields do not overwrite or block each other; the last
  explicit write to the same field wins.
- Unset workspace fields inherit live. Explicit Off, empty lists, and
  parent-equal choices remain explicit. Reset removes choices.
- Save waits for persistence; failure keeps the draft and reason visible.
- Settings uses one retained frame, category rail, native controls, help strip,
  and Save/Close footer. No new configuration wizard or competing writer.

Do not reintroduce configuration revisions, stale-settings conflicts, whole-form
replay, a trust database, or automatic configuration repair. Existing lifetime
checks for asynchronous results and exact preimages for file mutation/restore
still serve different purposes and stay intact. Simplifying preference saves
does not authorize overwriting newer file contents.

See [settings layers](../reference/settings.md),
[settings design](../reference/settings-design.md), and
[dialog design](../reference/dialog-design-language.md).

## Verified starting points

| Current behavior | Consequence for this design |
|---|---|
| `app/shell_client.go` attaches before starting the full TUI. `core/runtime.go` constructs a workspace before returning its snapshot. | A warning on the existing launcher would be too late. Selection needs a genuine pre-workspace entry point. |
| `core/file_watch.go` recursively registers watches and mainly excludes managed worktrees. | Bound registration, traversal, and invalidation delivery; do not call this a code index. |
| `workspace/history_service.go` captures at startup, message boundaries, external-change debounce, and periodic reconciliation. | History is an independent workload, not solved by a sidebar optimization. |
| `localhistory/snapshot.go` scans eligible files before comparing manifests. Ignore-rule discovery in `localhistory/ignore.go` also traverses directories. | Put bounds before allocation/content reads and inside ignore discovery, not only around the final manifest. |
| Every settings projection calls History UpdateSettings, which currently restarts even for unchanged history values. | An unrelated setting must not restart watchers or recapture the workspace. |
| Filename catalogs are on demand and already have entry/path-text limits. Some directory readers collect all siblings before the callback can stop. | A result cap alone is not a traversal or peak-memory bound. Include huge flat directories and Windows readers in validation. |
| Connected picker search currently calls CatalogFiles for the active workspace, not the picker's browsed root. | Correct that shared request contract before relying on the picker to select a different project. |
| `workspace/document_watch.go` watches retained documents and ancestors separately. | Background exclusions/limits must not break open-file external-change detection. |
| Direct-tool undo records exact operation preimages, but message scopes and restore safety capture also depend on snapshots. | Turning off broad snapshots behind the user's back is not a safe lightweight mode. |
| History rejects a store inside its workspace. Home normally contains the profile's data directory. | Detect this before opening, explain it, and do not invent a new storage location or silently disable history. |

The friend's 4 GB incident is not reproduced or attributed yet. Profiles must
separate core, shell, and subprocess memory rather than assuming one owner.

## 1. Opening a folder

### Normal project

Keep the existing launch behavior. No mandatory trust prompt, Git requirement,
recursive "is this a project?" scan, or setup page. The current workspace-switch
confirmation and dirty-editor/composer protections remain.

### Known broad location

Before creating its workspace services, present a small native project chooser:

```text
Choose a project folder

You're opening Desktop:
/the/exact/core-side/path

This folder may contain unrelated projects and private files.
Choose the folder you want to work on.

Recent projects
  my-app                  /.../Desktop/my-app
  another-project         /.../Projects/another-project

[ Choose folder... ]  [ Open Desktop anyway ]  [ Quit ]
```

- Choose folder is the focused primary action. Recent projects are selectable;
  Enter opens the selected destination. The path is readable and copyable.
- Reuse the native folder picker, list, search, buttons, and frame components.
  This is a focused pre-workspace surface, not a fake Workspace, a hidden home
  attachment, or a second full IDE implementation.
- The picker initially lists only the current directory, in bounded pages.
  Expanding a folder reads that folder. Recursive filename discovery happens
  only after a non-empty query, with the correct displayed root and bounds.
- Open anyway is deliberate consent to that displayed folder, not permission
  escalation. It does not raise resource limits, disable history, or persist a
  "trusted home" record. The default asks again on the next fresh launch.
- In an existing shell, enrich the existing destination confirmation instead
  of showing two consecutive confirmations. Cancel/Esc keeps the old workspace,
  dirty buffers, drafts, selection, and focus.
- For startup, Esc/close quits before workspace creation. Resume stays bound to
  its original workspace; choosing a different project never reparents a saved
  session or carries its resume ID into that project.

Classify exact OS-known home/personal folders and filesystem/volume roots on the
core host. Resolve symlink aliases; support relocated/localized Desktop and
Downloads. Do not classify arbitrary folders just because their basename is
Desktop, or every ancestor of a Git project. A child project opens normally.
Use real OS home information for personal-folder classification and
`buildinfo.Home` for Crude storage; CRUDE_HOME is not the user's Desktop location.

Filesystem/volume roots require a narrower selection. The Allow preference
below does not bypass that restriction. A known store-inside-workspace conflict
with enabled history is likewise an explanatory preflight error, not a failed
agent turn after opening. Intentional home use is possible only with a profile
outside that tree or with history explicitly configured Off, as permitted by
the existing history policy. Do not silently make either change.
Where opening is unavailable, replace Open anyway with the reason beside
Choose folder; never leave a button that predictably fails after selection.
Within an already-open shell, Open settings can navigate to the existing
Local History page without losing the current workspace. Startup always keeps
Choose folder and Quit usable; it does not manufacture a workspace to host
Settings or offer a second configuration editor.

Keep existing saved workspace/session data untouched. These are intentional
admission safeguards, not deletion, migration, or reassignment of old work.

### Minimal ownership change

The connected core must expose bounded, read-only pre-attachment inspection,
recent-workspace metadata, and single-directory browsing. These call the
existing owners/helpers without constructing a workspace actor. The startup
chooser owns only its connection and selection, then hands the selected root
to normal shell startup. SSH uses the same core-side operations, not client-side
filesystem guesses.

Actual attachment rechecks classification and requires an explicit broad-folder
choice when the application preference is Ask. Bind that choice to the canonical
path shown; if the destination changes, ask again. Use an ordinary typed request,
not approval tokens, a revisioned consent store, or another policy engine.
Noninteractive/control clients receive an actionable refusal until their caller
explicitly selects the broad folder; do not read stdin or auto-consent for them.
Reconnection to the same accepted attachment does not repeatedly prompt.

## 2. One small Settings page

Add **Workspace files** in the existing IDE group. The mockup below is content,
not new chrome; use the exact existing Settings anatomy and controls.

```text
Workspace files
Choose what Crude scans in the background.

Opening personal folders       [ Ask first | Allow ]
Background scan limit          [ 100000 ] entries
Skip folders                   [ node_modules/
                                 .venv/
                                 ...             ]

Application | Workspace        [ Reset section ]  [ Save ] [ Close ]
```

| Field | Scope and meaning |
|---|---|
| Opening personal folders | Application-only; Ask first by default. Workspace scope displays it read-only. Allow skips the personal-folder prompt, not root/storage checks or limits. This is not a tool access grant. |
| Background scan limit | Application default with optional workspace override. One finite entry budget for each recursive background watch/catalog/history scan, counting directories and files. Initial candidate: 100,000, subject to the baseline measurements below; validate a finite supported range rather than offering Unlimited. |
| Skip folders | Application default with optional workspace override; one Git-style pattern per line. Used by broad background watching, filename catalogs, and automatic snapshots. Does not hide directly browsed files or authorize/block explicit file tools. An explicit empty workspace list replaces the inherited list. |

Store these as one ordinary workspace-files preference group using the current
typed settings and sparse overlay machinery. Enforce the opening preference's
application-only ownership in persistence/control validation, not merely by
disabling a field. Do not create another JSON file, consent ledger, per-folder
mode registry, or per-consumer copies of the configured limit. Runtime progress
and coverage are derived, not persisted choices.

Populate Skip folders with the existing common dependency/build/cache exclusions,
not a new opaque preset system. History retains its current Honor .gitignore and
history-specific Also ignore controls. Catalogs and watchers keep their necessary
Git administration distinctions. Crude's actual state tree, managed execution
trees, and unsafe traversal remain hard exclusions, not removable settings.
Do not add a new ignore-file format or silently reinterpret hidden-file options.

Keep **Local History** as the only page for capture enablement, per-file size,
cadence, and retention. Do not duplicate those switches or bundle them into a
hidden "lightweight" preset. Explain that exclusions and size cutoffs affect
automatic snapshots; required exact mutation/restore safety can still read an
explicitly changed file. Do not claim otherwise in field help.

No session-level scan policy, workers slider, filesystem knowledge quiz, mode
matrix, global RAM slider, or editable safety bypass. No live persistence while
typing: the normal Save/Reset/discard workflow applies. Runtime coverage is a
read-only projection, never another stored setting.

## 3. Bounded behavior with truthful status

### Watchers and browsing

- The workspace owns one background invalidation watcher regardless of shell
  count. Retain targeted document watches; keep the small Git HEAD/index/ref
  signal path working without walking every Git object or managed worktree.
- Use cancellable batched directory reads and finite pending work. Check the
  entry budget before retaining paths or registering watches. Stop within a
  giant flat directory rather than reading/sorting all its children first.
- Reuse small traversal/pattern helpers where semantics match. Do not create a
  universal filesystem service with plugins, caches, or per-consumer mode flags.
- Coalesce filesystem invalidations separately from agent/lifecycle events.
  Overflow becomes one bounded "refresh needed" invalidation, not unbounded
  queued filenames or a request to recursively rescan on every tick.
- An explicit Refresh may retry limited coverage once, for example after the
  user removes an oversized subtree. It must not arm an automatic retry storm.
- Retain explicit refresh and on-demand browsing. Open files still detect external
  edits and preserve dirty buffers even when their folder is excluded/unwatched.
- Do not close retained agents, terminals, documents, or history on shell detach.
  Stop/join only the worker lifetime owned by the changed policy or shutdown.

### Filename discovery and content search

- Preserve the existing filename catalog approach. Bound entries, all retained
  path text, response size, and queued work; the current relative-path byte count
  is not a total-memory measurement. Do not add content indexing.
- Surface the existing catalog Truncated result: "Search limited to N entries.
  Open a smaller folder or change Workspace files settings." Keep manual path
  entry/navigation available. Do not turn omitted results into "No files exist".
- Do not implement Zed-style silent search omission by making Find in Files
  search only the sidebar's loaded nodes. Content search stays an explicit disk
  search with cancellation and its own visible result limit.
- Retain the existing agent glob/grep contracts. A scan exclusion is not an
  access-control rule, and directly addressing an excluded folder still works.

### Local History

- Carry the budget through ignore discovery, startup, external reconciliation,
  message boundaries, manual capture, and restore safety work. Keep an internal
  bound on per-attempt content I/O/work as well; a file count alone does not bound
  hashing 100,000 large files. Derive that bound from measurements, not the
  retained-history storage budget, and explain which bound was reached.
- A limit/cancellation/error never publishes a partial workspace manifest. The
  previous complete snapshot and operation receipts remain intact. Do not infer
  that unvisited files were deleted or that a restore has complete coverage.
- A limit stops that capture attempt. Do not repeat the same known-over-budget
  full scan every debounce/periodic tick. A relevant saved setting, explicit
  capture retry, or reopened workspace can start a new bounded attempt.
- Keep the current required-history failure policy at agent admission and before
  restore/mutation. Show an actionable reason and **Open settings**; do not
  silently continue unprotected or offer a one-click "ignore safety" action.
  Users may deliberately use the existing history-Off preference, with its
  current limitations clearly stated. Do not promise operation-only undo while
  current message/restore dependencies still require whole snapshots.
- Unchanged effective capture policy does nothing. Retention-only changes need
  not rebuild watches or recapture. A real capture-policy change cancels/joins
  the old worker off the actor/UI thread, then starts one replacement. Existing
  message scopes retain their admitted capture policy through completion.
- Preserve scoped undo, durable preimages, mandatory restore safety snapshots,
  newer-write checks, rollback, interrupted recovery, ignored/external direct
  writes, and protected retention. No snapshot-schema migration or new partial
  snapshot mode is justified by this UX change.

### What the user sees

Use existing inline status/help and native operational-recovery surfaces:

| Situation | Presentation |
|---|---|
| Ordinary project, within limits | No new persistent banner or telemetry. |
| Background watch limit | One deduplicated notice plus a retained Workspace files status: background updates limited; open files remain monitored. Offer Open settings / Choose folder. |
| Filename result limit | Status in the search/picker that reached it; direct path entry still works. |
| History capture limit/failure | Local History reports capture paused/failed and last successful capture; required actions report the exact block with Open settings. No false "Ready". |
| Saved change | "Saved" means persisted. If a worker is applying it, show "Applying..." separately; later resource failure is not misreported as a failed settings save. |

Notices retain evidence but are never the only way to discover why a requested
action could not proceed. No repeated modal on every tick and no auto-navigation
away from an active editor or agent.

## Implementation order and stop conditions

1. **Establish reproduction and limits.** In throwaway profiles, measure empty,
   ordinary project, nested-project Desktop, huge flat directory, and a growing
   tree. Capture cold startup, idle beyond one history interval, search, settings
   saves, message start/end, detach, and shutdown separately. Record heap/RSS,
   bytes read, watcher registrations, queued invalidations, first usable frame,
   and input latency. Profile both core and shell. Confirm which original path
   causes growth before optimizing it. Set finite defaults and allocation gates
   from repeated results with limited headroom, not a claimed universal RAM cap.
2. **Land the settings and bounded worker owners.** Add the small settings group
   through the current sparse commit/merge/reset paths; fix unchanged-policy
   restarts, exclusions, directory-read bounds, and history error/status behavior.
   Do not wire an Open anyway button to the old unbounded services.
3. **Land pre-workspace selection and shared navigation.** Add read-only
   pre-attachment operations, reuse picker/list components, correct remote picker
   root/directory-only requests, and integrate startup/switch/SSH/control
   admission. No normal-project setup regression or duplicate confirmation.
4. **Complete integrated UX and safety acceptance.** Exercise actual rendered
   startup, folder selection, settings, search, history, and recovery at desktop
   and constrained sizes. Update affected reference documents and remove this
   plan only after all acceptance evidence exists.

Keep tightly coupled policy/worker/history decisions together. Independent
settings UI, native path detection, and regression fixtures may be delegated
after the typed contracts are fixed. No release of the chooser alone described
as the memory fix. If profiles falsify the suspected owner, revise the fix before
adding more caches or exceptions.

## Regression and acceptance matrix

| Journey | Required assertion |
|---|---|
| Cold launch in fake home/Desktop/root | No recursive scan, history capture, LSP start, or workspace actor for the candidate before the user chooses. Directory count does not scale pre-choice memory. A bounded recent-list/single-directory read is allowed. |
| Normal Git and non-Git project | Existing initial tree, New Session, resume, editor, Git signals, and Settings operate without a new prompt. A project under Desktop is not Desktop itself. |
| Paths/platforms | Core-side localized/redirected personal folders, symlink aliases, spaces, case-sensitive/case-insensitive paths, Windows drives/UNC, SSH, unavailable paths, CRUDE_HOME, and state-inside-root conflict behave explicitly. No host-side path substitution. |
| Choose/cancel/open anyway | Painted keyboard and pointer routes agree. Esc/close/cancel perform no destination attachment or config write. Selection cannot move a resume ID to another project. No double confirmation. |
| Settings layers | Opening creates zero edits. Changing one field writes only that leaf. Different stale drafts preserve sibling changes; same-leaf last write wins without a revision conflict. Workspace Off/empty list/parent-equal choices survive restart, Reset inherits live, and failed Save retains the draft. |
| Unrelated settings | Theme/account/agent/retention edits do not restart capture/watchers or reread workspace content. Changed relevant policy replaces one worker, not one per shell. |
| Watcher limits and events | Wide/deep/growing fixtures stop within the entry/work bounds. Event bursts remain bounded and coalesced without dropping agent events. Git HEAD/index/refs and open-file atomic replace/delete/recreate detection remain correct. |
| Picker and search | Connected search uses its displayed root, not the current workspace. Filename truncation is visible, direct paths work, and Find in Files still finds unopened deep sources. Cancel/close/switch discard late results without changing another view. |
| History limits | Boundary and over-boundary fixtures cover ignore discovery, per-file/content I/O, startup, periodic/debounce, message boundaries, and restore safety. Failure creates no complete-looking partial snapshot, repeat storm, or false deletion. Prior snapshots/receipts remain readable. |
| Undo and restore | Exact direct-write undo, ignored/external paths, other-agent/newer edits, stale previews, dirty documents, mandatory safety capture, failed apply rollback, interrupted recovery, and retention protections retain existing guarantees. Required capture failure precedes mutation/provider work where the current contract requires it. |
| Lifetime | Multiple shells share the workspace worker; switching or closing one preserves peers, agents, terminals, editors, and drafts. Relevant settings changes and final shutdown cancel/join correctly. Detach alone is not shutdown. |
| Resource evidence | With the same configured limit, increasing a fixture far beyond that limit does not grow retained data/watch count or peak memory with the full tree. Bound actual allocation, directory reads, and presented latency separately; a "no renders" counter is not acceptance. |

Extend the existing settings explicit-layer and mixed-core journeys, workspace
navigation/switch tests, `core/file_watch_test.go`,
`workspace/history_service_test.go`, `workspace/history_agent_e2e_test.go`,
`localhistory/safety_acceptance_test.go`, and
`localhistory/external_operations_test.go`. Preserve the original independent
content/behavior assertions instead of changing expected values to match a helper.

Run affected tests, full build/test/lint/design/docs/bench gates, relevant races,
native Windows/Linux acceptance, and cross-builds for platform detection.
Use sequential repository visual captures at 160x40 and 80x24, with one isolated
core/profile and cleanup of every capture window. Cross-builds do not substitute
for native path/watcher behavior. Do not raise existing ceilings, skip safety
checks, or claim "no regression" from focused tests alone.

## Planning evidence and remaining uncertainty

- Current settings build passed, and focused explicit-field/layer-isolation,
  reset/draft-preservation, and mixed-scope core/restart tests passed.
- Inspected the current Settings window using the repository capture workflow at
  160x40 and 80x24 in an isolated profile. The existing rail, help, scope footer,
  and Local History form are the visual precedent. These were baseline captures,
  not acceptance of the proposed feature.
- No product code changed for this plan. The full suite, native matrix, original
  memory reproduction, final numeric budgets, and proposed UX remain unverified.
- The documentation gate and whitespace checks pass for the plan and its index
  entry. This is documentation validation, not feature or no-regression proof.

External precedents inform behavior, not a dependency choice:
[OpenCode's broad-folder indexing fix](https://github.com/anomalyco/opencode/issues/32511),
[Zed's bounded/lazy non-Git scan](https://github.com/zed-industries/zed/pull/62583),
and [Claude's nonpersistent home trust](https://code.claude.com/docs/en/security#additional-safeguards).
Crude's actual workload is watching/snapshotting/discovery, not a semantic index.
