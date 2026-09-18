# Editor Gutter and Non-Disruptive Autosave

> Status: LANDED - implemented and acceptance-validated 2026-09-17. Structured Git hunks, native gutter interaction, revision-aware saves, LSP/watcher ordering, shared mutation leases, and opt-in autosave are the authoritative product paths.

## 1. Outcome and Scope

Deliver an accurate, restrained, interactive editor gutter and optional VS Code-like
autosave. Autosave is **disabled by default**. Enabling it must not interfere with
typing, selection, completion, diagnostics, undo, scrolling, or other file writers.
The gutter must remain accurate before, during, and after both automatic and manual
saves. Neither feature is complete as an isolated widget or background timer.

This plan builds on the [workspace architecture](workspace-refactor-plan.md),
[component system](tui-component-system-plan.md),
[render authority](render-authority-hardening-plan.md),
[terminal width contract](../reference/terminal-width.md),
[settings model](../reference/settings.md), and
[performance baselines](../reference/performance-baselines.md).

### Product invariants

- One authoritative live document and savepoint per shared buffer, not per tab.
- One save transaction path for manual save, save-and-close, and autosave.
- One gutter geometry calculation for paint, cursor placement, and hit-testing.
- One structured Git hunk projection for markers, inspection, and navigation.
- Disk saves, Git changes, and diagnostics are different facts. Saving clears the
  unsaved indicator only for the saved content; it never clears HEAD-relative Git
  markers or existing diagnostics as a side effect.
- No file I/O, subprocess, diff calculation, or LSP wait on the UI goroutine.
- No silent overwrite of a known external change, no success before write
  completion, and no claim of filesystem guarantees the platform cannot provide.
- Follow the current Crude component and theme system. State-of-the-art here means
  precision, discoverability, restraint, and dependable interaction, not copying
  graphical IDE pixels or adding unsupported folding/debugging decorations.

## 2. Evidence and Existing Risks

The following findings come from the current code, not a hypothetical architecture.

| Area | Current implementation | Consequence |
|---|---|---|
| Git mapping | `workspace/git_editor.go:lineEdits` renders a unified diff, starts `afterIdx` at zero, and skips every hunk header | Changes far down a file are painted near its beginning; gaps between hunks are lost |
| Reproduction | Actual `AnnotateFile` on the current `AGENTS.md` reports added lines 4-12 while `git diff --unified=0 HEAD -- AGENTS.md` reports 165-173 | Confirmed user-visible false annotations, not a theme issue |
| Git model | Only unchanged/added/modified per-line kinds | Pure deletions have no representable boundary; inspection lacks an authoritative hunk |
| Gutter layout | `tui/editor/render.go:formatLineNum` does not add leading padding; numbers are effectively left-aligned, followed by fixed diagnostic/Git/rule columns | Uneven number alignment, excess space in the wrong place, source text against a permanent divider |
| Autosave settings | `EditorSettings.AutoSaveDelay` and the settings field exist, but no runtime consumer schedules saves | UI currently exposes an ineffective preference |
| Save path | `Editor.Save` calls `Buffer.Save`/`PieceTable.Save`, ultimately `os.WriteFile`, synchronously | A slow write blocks input; adding a timer would not fix ownership or responsiveness |
| Change notifications | `Editor.Save` invokes `notifyContentChanged`, including after a failed save | Save can manufacture a text-change/LSP/Git invalidation cycle |
| Formatting | Save can run an external in-place formatter, reread disk, and replace buffer contents; `SaveAndFormat` can format twice | Unsafe basis for quiet autosave; formatter errors are currently swallowed by a no-op notifier |
| Watchers | `tui/editor/watcher.go` ignores all matching events for 200 ms after save | Can miss a genuine external edit or reload a delayed self-event |
| Reload | `Editor.Reload` replaces its PieceTable, while a shared Buffer may still reference the old one | Shared source authority must be repaired before async saves or reloads are trusted |
| LSP | `QueueFileUpdate` has asynchronous debounce; `LSPClient` has no save notification contract | Saves need explicit ordered notification support, not synthetic `didChange` calls |
| Ownership | Editor, Buffer, and PieceTable each carry modified/dirty state | An asynchronous completion cannot safely clear independent flags without a common savepoint |

The existing four-line annotation test does not expose skipped hunk coordinates.
The preceding horizontal-scroll investigation is separate: native horizontal and
Shift-wheel inputs pass through Update and painted output; the user's Kitty/Tilix
comparison identified terminal-dependent gesture delivery. Do not modify input
decoding as part of this work.

## 3. Research and Deliberate Differences

- [VS Code Save / Auto Save](https://code.visualstudio.com/docs/editing/codebasics#_save-auto-save):
  explicit save by default; off, after-delay, editor-focus, and window-focus modes;
  separate delay setting with a 1,000 ms default.
- [JetBrains editor gutter](https://www.jetbrains.com/help/idea/editor-gutter.html):
  separate line numbers and contextual icons, color-coded changed ranges, and
  original-text inspection from a change marker.
- [LSP document synchronization](https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/#textDocument_synchronization):
  capability-gated `willSave`/`didSave`, explicit save reasons, and optional exact
  saved text. `didSave` does not carry a document version.
- [LSP willSaveWaitUntil](https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/#textDocument_willSaveWaitUntil):
  pre-save edits are optional and may be dropped when slow or unreliable.
- The existing `go-udiff` dependency exposes `Lines`, `Edit`, and
  `ToUnifiedDiff`; a structured source already exists. Do not parse rendered patch
  strings or introduce a second diff implementation without measured need.

Crude intentionally makes **all automatic save modes text-only**. Formatting,
organize-imports, whitespace cleanup, and edit-producing save hooks are not
automatic save participants. This is a deliberate non-interference policy, not a
claim that every VS Code mode behaves identically. Explicit save can honor the
existing format-on-save preference through the corrected transaction path.

Autosave is not crash backup or hot exit. Local History records saved filesystem
states; it is not a durable copy of every unsaved keystroke. Do not advertise either
guarantee as part of this feature.

## 4. Authoritative State and Boundaries

Keep the current shared Buffer/PieceTable editing engine. Do not expand this into
a general editor rewrite or create a second live text store in workspace.

| Owner | Responsibility |
|---|---|
| Shared editor Buffer | Live text, monotonic content revision, stable buffer lifetime ID, path epoch, saved-content identity, mutation notifications |
| Editor view | Cursor, selection, viewport, gutter geometry and interaction; no independent disk-save authority |
| `types/` contracts | Immutable document snapshots, save intents/results, file identity, hunk/deletion data, operation and generation IDs |
| Workspace document service | Save scheduling, bounded background workers, per-document serialization, disk preconditions, committed-write receipts, lifecycle cancellation |
| Workspace Git service | Repository/blob baseline, structured hunk derivation, separate blame work, annotation generations |
| Workspace LSP manager | Per-document ordered notification stream and negotiated synchronization capabilities |
| `fileio/` | Checked atomic file replacement and platform-specific filesystem mechanics |
| TUI viewmodel/screens | Submit intents, apply generation-valid results, project settings/status, route hunk/diagnostic actions |

Workspace accepts immutable snapshots and events through methods; it never imports
`crude/tui`, calls back into a mutable editor, or holds a second authoritative
document. Background results use presentation-neutral events/command results.
Never call synchronous `EventBus.Send` from Bubble Tea Update.

Collapse the three dirty flags into one shared-buffer savepoint contract. A save
acknowledgement identifies exactly what reached disk. A newer buffer stays dirty;
undo back to saved content becomes clean even though its revision is newer.
Standalone editable consumers must use this same buffer contract; read-only
previews are not save targets. Retain view-local state, and ensure a mutation in
one shared view invalidates the other views without duplicating save jobs.

Snapshots must be safe to read after subsequent edits. Share immutable text storage
where possible and capture piece descriptors once; never read a live PieceTable
from a worker. Measure snapshot capture and materialization separately. Repeated
per-keystroke or per-frame full-document copies are not acceptable.

## 5. Gutter Correctness

### Baseline and structured hunks

The default remains **HEAD versus current buffer**, including staged, unstaged,
and unsaved text. Staging alone does not clear markers. Saving alone does not
clear markers. Committing matching content does. Inspection names this baseline
explicitly; do not add a second staged/unstaged rail in this release.

Produce immutable, presentation-neutral hunks containing old/new half-open line
ranges, inserted/deleted counts, baseline identity, document revision, and exact
source slices for lazy inspection. Maintain deletion boundaries independently of
surviving-line kinds. All consumers use this one projection.

Requirements:

- Honor original/new coordinates across long unchanged prefixes and separated
  hunks. Prefer structured `go-udiff` results over repairing the string parser.
- Pair deleted/inserted lines inside a contiguous replacement block consistently;
  surplus new lines are additions and surplus old lines remain deletion data.
  A deterministic positional mapping is acceptable; do not pretend it establishes
  semantic equivalence between different lines.
- Preserve blank lines, repeated text, actual `+++`/`---` content, final-newline
  changes, CRLF/LF, and Unicode. Never confuse content with patch headers.
- Define logical line counts and the editor's trailing empty row explicitly.
  End-of-line-only changes remain explainable; never silently normalize bytes.
- Handle untracked files, unborn HEAD, deletion to an empty file, renamed paths,
  repository root changes, worktrees, missing Git, and unmerged index entries.
  Distinguish clean, no-repository, ignored, conflicted, loading, and failed states;
  a Git error is not evidence that every line was added.
- Use exact object identity for the baseline and repository generation for HEAD,
  index/conflict, rename, and checkout events. Detect externally created commits,
  not only commits initiated from Crude.

### Asynchronous validity and cost

Key each projection by workspace generation, buffer lifetime, path epoch, content
revision, repository baseline, and relevant diff options. Compute off the UI thread
with latest-wins bounded work. Discard stale results at application time.

Invalidate annotations for changed text, not save acknowledgements or repaint.
Map hunks exactly through committed document edits so typing in one place does not
blink the entire file's change rail, and never drop the marker of a region the
keystroke landed in: it is still changed relative to HEAD, and only an
authoritative refresh may replace the projection. An annotation that arrives after
further edits is mapped forward from the revision it was computed at instead of
being attached to newer text, so the async result and the live edits do not fight
over the same rail. Full invalidation is appropriate for an unknown replacement or
baseline change, not every keystroke. Recompute after a quiet delay (400 ms) so a
typing burst coalesces into one refresh, and bound the hold (2 s) during
uninterrupted typing so the projection cannot go stale indefinitely. A transient
Git failure keeps the last projection instead of blanking the rail.

Waiting for the refresh before showing anything is not acceptable either: the rail
must answer the keystroke. The editor therefore keeps one subordinate, paint-only
claim layer for lines it just touched. It is not a second authority: it holds no
old text, it is invisible to inspection and navigation (`GitHunks`/`GitHunkAtLine`
answer from the authoritative projection only), it never covers a line an
authoritative hunk owns, and every arriving annotation discards it wholesale. Its
flavour is structural — the line the edit started on reads as edited, lines the
edit created with newlines read as added — and the authoritative refresh is what
refines that into real HEAD-relative hunks.

Cache HEAD content by blob identity. Separate fast line-change projection from
slow blame retrieval so blame never delays marker correctness. Active views get
priority; inactive tabs remain revision-keyed and warm. Updating disk state through
autosave must not cause a second equivalent buffer diff or full-tree Git scan per
keystroke.

## 6. Gutter Visual and Interaction Contract

### Geometry and appearance

Use a compact, borderless arrangement:

```text
[diagnostic slot] [right-aligned line number] [change rail] [space] source
```

- Numbers reserve `max(2, digits(last logical line))` cells and align on the units
  digit. Reserve a one-cell gap after numbers and a one-cell gap before source.
- Diagnostic and change lanes remain reserved even when empty, preventing content
  jumps as asynchronous annotations arrive. Diagnostic slot width is measured
  through the shared calibrated profile, not assumed to fit arbitrary emoji.
- Use the normal editor background, muted ordinary numbers, and normal-foreground
  emphasis for the current line number. Selection remains independent.
- Replace repeated plus/tilde glyphs with one narrow theme-owned vertical stroke;
  adjacent additions/modifications form contiguous bands. Added, modified, deleted,
  and conflict colors come from semantic theme tokens, not hard-coded RGB values.
- Remove the permanent separator rule. No background cards, gradients, blinking,
  pulsing, or invented empty folding/breakpoint columns.
- Deleted ranges use a compact boundary cap/wedge. BOF uses the leading boundary,
  EOF the trailing boundary of the final logical line; an emptied document uses
  its existing empty edit row. Multiple changes at a boundary remain discoverable
  together, without falsely coloring an unchanged neighbor as modified.
- Diagnostics and Git state never overwrite each other. Highest diagnostic
  severity is visible; inspection exposes all diagnostics at the line.
- No line numbers after EOF; an empty editable document still has its line 1.
  Turning Line Numbers off hides numbers, not diagnostics or Git status.
- Provide a monochrome/high-contrast marker form and textual descriptions so color
  is not the only carrier of change kind.
- One measured geometry object defines every column, text origin, cursor offset,
  scrollbar extent, and hit region. Clamp safely at narrow widths. If an existing
  view wraps lines, continuation rows do not acquire invented document numbers;
  do not invent a second document-to-visual-row mapper.

### Interaction

- Marker hover gives a restrained description including baseline and hunk counts.
  It does not steal focus, move the caret, or open a large popup on every motion.
- Marker click opens a read-only Changes inspector anchored to that hunk. Reuse
  `OverlayHost`, `FloatingWindow`, and `ui.WindowFrame`; use native selection,
  copy, scrolling, theme tokens, and normal Escape/focus restoration.
- Inspector displays HEAD text versus current-buffer text, real line ranges,
  whitespace/EOL information, and Previous/Next change controls. It lazily renders
  visible context and never syntax-highlights an entire large file to show a hunk.
- A changed snapshot invalidates or refreshes inspection by source identity. A
  stale marker cannot navigate to, copy, or act on an unrelated current hunk.
- Number-lane click selects the entire logical line. Drag and Shift extension use
  the existing editor selection owner, root Host capture, and outside autoscroll;
  copy-on-release follows Crude's shared contract. Padding is not a hunk button.
- Diagnostic click opens the existing diagnostic-detail route, not a competing
  tooltip stack. All lanes have distinct hit ownership.
- Register Previous Change, Next Change, and Inspect Current Change through the
  shared action registry, palette, help, and context menus. Assign default chords
  only after checking terminal delivery and existing bindings.
- Hunk revert/stage operations are deferred. They require independent immutable
  preview, current-source preconditions, audit, rollback, and undo design.

## 7. Autosave Settings and UX

Replace implicit "zero delay means off" with an explicit mode plus a delay:

| Setting | Values / default | Behavior |
|---|---|---|
| Auto Save | Off (default), After Delay, On Editor Focus Change, On Terminal Focus Loss | One authoritative mode; Off is a genuine persisted setting |
| Auto Save Delay | 1,000 ms default, validated 250-60,000 ms | Relevant only in After Delay; never independently enables saving |
| Format on Manual Save | Existing `FormatOnSave`, unchanged default | Applies to explicit save only; description matches actual formatter backend |

Use one `EditorSettings.AutoSave` enum and the existing `AutoSaveDelay` as tuning,
not an additional enabled boolean. Missing/unknown mode resolves safely to Off;
invalid explicit input in settings is rejected with a field error. An old nonzero
delay must not unexpectedly turn on a formerly nonfunctional feature. There is no
legacy runtime path or development-format migration requirement.

Expose these fields in Application and Workspace Settings under Editor. Preserve
live, field-level inheritance, explicit workspace Off over application On, Reset
section semantics, JSON round-trip, and normal Save/Cancel behavior. Session agent
settings do not own editor autosave. Apply effective changes to all open buffers.

The action palette includes Enable Auto Save / Disable Auto Save and Open Auto Save
Settings. The quick action explicitly changes the current workspace override:
enabling chooses After Delay, disabling chooses Off, and its help names the scope.
It must not silently change global preferences or ignore an effective override.

Mode semantics:

- After Delay: debounce actual committed text mutations per buffer, including
  undo/redo and completed replace/paste transactions. Cursor movement, scrolling,
  diagnostics, Git results, theme changes, and save results never reset the timer.
- On Editor Focus Change: trigger when focus leaves the logical document, not
  when moving between two views of the same buffer or an editor-owned completion,
  hover, find, or gutter-inspection surface. Switching to another document, chat,
  sidebar, or settings counts. Close/discard is governed by the close barrier below.
- On Terminal Focus Loss: use actual outer-terminal focus reports, not mouse exit
  or shell-component focus. Label the dependence on terminal support. If reports
  are unavailable, explain this and allow After Delay; never infer blur from idle.
  Do not silently advertise reliable desktop-window tracking in all multiplexers.
- Enabling over existing eligible dirty buffers waits a full configured delay in
  After Delay mode; focus modes wait for their next qualifying transition. No
  surprise bulk save merely from closing the settings window that enabled them.
- Disabling cancels pending work and pre-commit automatic jobs. An already committed
  write is acknowledged honestly, not rolled back; no subsequent job is scheduled.

Successful autosave is silent. No "Saved" toast, focus change, popup closure,
scroll reset, cursor movement, or formatting. Keep the dirty indicator until the
appropriate save acknowledgement. A slow save may expose subdued text in the
existing file details; do not flash a spinner on every short save. Errors produce
one deduplicated, actionable status issue per buffer/error episode, not repeated
modal interruptions or per-keystroke toasts.

A real disk save can legitimately trigger a language server, build watcher, or
development server. Non-interference means correct ordering and no redundant work,
not suppressing their legitimate reactions. Delay and focus modes let users choose
the appropriate save frequency for expensive downstream tools.

Eligibility is file-backed, editable buffers with a confirmed disk baseline.
Scratch/untitled, read-only previews, binary/unsupported encodings, unresolved disk
conflicts, and files under destructive restore/rename ownership are not silently
saved. Autosave never opens Save As, makes a deleted parent tree, elevates privileges,
or recreates an externally deleted file. Dirty inactive buffers are eligible;
their completion must not switch tabs or update the wrong active editor.

## 8. Save Transaction and Scheduling

### One bounded pipeline

The workspace coordinator keeps per-buffer scheduling state, not mutable live text.
Use one resettable deadline service and a bounded worker pool; no unbounded
goroutines/timers, full-buffer snapshot queue, or scan of every tab per frame.
At most one write is in flight per canonical target, with only the latest pending
intent retained. Explicit save takes priority over pending automatic work without
permitting parallel writers. Serialize aliases of the same file identity.

State transitions are Clean -> Dirty/Pending -> Saving -> Clean or Dirty/Pending;
Blocked and Error preserve unsaved content. A changed revision during a write does
not cancel or mutate that immutable snapshot: acknowledge its actual disk result,
keep newer content dirty, then schedule the latest eligible revision. Never replay
a backlog of intermediate snapshots after typing stops.

Save intent carries buffer lifetime, workspace generation, path epoch, content
revision/identity, expected disk fingerprint, settings epoch, operation ID, and
reason. Save result returns those identities plus the actual committed disk
fingerprint and structured outcome. Path alone is insufficient ownership.

### Transaction sequence

1. At the UI mutation boundary capture a safe immutable snapshot and submit intent.
   Capture only when work is due; deadline messages carry IDs, not live editor
   pointers that workers later dereference. Recheck eligibility on submission.
2. Off-thread, acquire the target's save/mutation lease, materialize bytes, and
   verify cancellation, generations, path/parent identity, disk type, and expected
   content/metadata. An automatic request with equal bytes is a no-op: no rewrite
   or synthetic save event. Explicit requests still evaluate their chosen save
   participants before deciding whether the resulting bytes need persistence.
3. For an explicit save only, run configured formatting against a snapshot, never
   the live on-disk file. Return text edits to the UI owner; apply as one undoable
   document transaction only if the source revision still matches, then persist
   the resulting snapshot. Stale formatting is discarded, not replayed over typing.
   Bound retries; if the document keeps changing, skip formatting for that request,
   save the selected text snapshot, and report that distinction without claiming
   unsaved newer text was persisted. No double-format SaveAndFormat path remains.
4. Write a sibling temporary file, preserve supported permissions/encoding/BOM/EOL,
   flush and close it, recheck preconditions at commit, and use checked platform
   replacement. Skip the write if the final selected content already matches disk.
   Do not blindly reuse `fileio.writeAtomicChecked`: audit its current
   parent creation, symlink, metadata, replacement, and directory durability behavior.
5. Record the committed receipt, schedule required LSP/history/tree notifications,
   and return completion. Distinguish pre-commit failure, successful commit, and
   uncertain post-commit durability; none may masquerade as another.
6. Apply completion only to the matching buffer lifetime/path/workspace. Advance
   its saved-content identity, not its current contents. If current text differs,
   it stays dirty. Update all views of that buffer, not just the active tab.

Automatic saves skip step 3 entirely: no formatter, imports cleanup, newline
insertion, trimming, `willSaveWaitUntil`, code action, reload, or undo checkpoint.
Saving identical text produces no content-change callback or document revision.

### Conflicts, cancellation, and filesystem limits

Use content fingerprints plus type, identity, mode, and path/parent checks, not
mtime alone. A detected external change blocks autosave for that buffer and offers
Compare, Reload/Discard, Save As, and explicit overwrite-with-preview through the
existing safety UX. Do not merge or overwrite silently.

Share the mutation lease with Crude-owned file tools, project replace, rename,
delete, checkout/discard where applicable, and Local History restore. A restore
must not be immediately undone by a previously queued autosave. Reconcile buffers
and acquire new baselines before resuming eligibility.

There is no portable atomic compare-and-swap against an arbitrary uncooperative
external process. A stat/hash check followed by rename still has a race. Document
the supported local-filesystem contract, use available platform identity guards,
retain recoverable preimages for destructive conflict resolution, and verify the
post-commit state. Never market this as universal race-free saving. Unsupported
symlink/hardlink/ACL/network-file semantics must fail safely or be explicitly
handled and tested, not silently converted by replacement.

On ordinary failure the original file remains intact and the buffer stays dirty.
Transient sharing/busy errors may get two bounded backoff retries (1 s, then 5 s)
with fresh preconditions. Permission, missing-path, disk-full, and conflict errors
wait for explicit retry or a verified relevant condition change; typing alone does
not cause an endless failure loop. LSP/Git failure after a successful disk commit
does not convert that commit into a failed save.

## 9. LSP and Watcher Non-Interference

### LSP ordering

Extend the workspace-facing LSP contract and TUI adapter with negotiated save
notifications and an explicit per-document ordering owner. Preserve the bounded
async pipe transport; separate notification order from UI responsiveness.

- Real text mutations drive `didChange`. A save does not fabricate another change.
- Before saving snapshot R, enqueue the corresponding change barrier ahead of
  capability-gated `willSave` with Manual, AfterDelay, or FocusOut reason.
- Send `didSave` only for a successful real write and only when requested by the
  server. Honor `includeText` using the bytes/text actually saved, not whatever
  happens to be in the editor when the write completes.
- Later edits must keep reaching completion/diagnostic services while slow disk
  I/O runs. Do not block the LSP queue for the duration of a save. If revision R+1
  has already been sent, never replay R to "align" a late save notification.
  `didSave` has no version: test this normal newer-buffer/older-disk case explicitly,
  with exact saved text when negotiated and monotonically ordered document changes.
- Order open, change, save, and close through the same document stream; do not
  allow independent timer goroutines to reorder notifications. Handle server
  restarts/open-document generations and dynamic save registrations correctly.
- Do not advertise edit-producing save hooks that Crude will not execute. Keep
  `willSaveWaitUntil` disabled for automatic saves; any future participant requires
  cancellation, revision checks, a strict deadline, and explicit user policy.
- A slow/dead/disconnected server must not block disk persistence. Record notification
  failure as LSP health, reopen with latest text on recovery, and do not replay a
  history of obsolete save events.
- Preserve completion popup, selections, diagnostics, and in-flight read requests
  when text has not changed. Accept/reject diagnostic versions according to actual
  document revisions, not save events; versionless server diagnostics need explicit
  conservative handling rather than an invented version guarantee.

### Exact write reconciliation

Delete the blanket 200 ms suppression policy. Correlate watcher events with exact
committed-write receipts and observed disk fingerprints off the UI thread.

- A delayed self-event matching the committed disk image advances disk knowledge;
  it does not reload the editor or produce another `didChange`/`didSave`.
- A genuine external change immediately after save is not ignored because it is
  "too soon". Reconcile it against current disk and saved identities.
- Coalesce event bursts, not mutations. Handle events arriving before the save
  acknowledgement, atomic-replace rename/create patterns, missing events, and
  watcher overflow. Match exact current content, not an arbitrary past receipt.
- Clean external reloads use a command and a revision-checked shared-buffer edit;
  no replacement of one view's private PieceTable. Dirty external changes enter
  conflict state without clearing local text or selection.
- Reconcile every open buffer, including inactive tabs. No active-editor-only
  watcher polling as the authority.
- Self-event suppression applies only to redundant editor reloads. File tree,
  Git disk status, registered LSP watched-file observers, and Local History still
  receive one legitimate coalesced disk transition through their owners.
- Keep open-document `didChange`, `didSave`, and watched-file notifications distinct.
  A commit and its watcher echo must not double-publish the same notification kind.
- Autosaves remain editor/user changes in Local History, never attributed to an
  agent/message operation. Reuse history's content deduplication and retention;
  do not snapshot the whole workspace or synchronously verify history per save.

## 10. Lifecycle and Safety Boundaries

| Transition | Required behavior |
|---|---|
| Tab switch | Preserve buffer, undo, cursor, gutter cache; focus mode may save the old document without selecting it again |
| Multiple views | One savepoint/job stream; a save never moves any view's cursor or viewport |
| Manual save during autosave | Serialize; deduplicate identical work; explicit formatting policy is not lost merely because autosave just made the buffer clean |
| Close with pending save | Stop new automatic admission, await/cancel owned work asynchronously, then evaluate the actual remaining dirty revision |
| Save and close | Close only after the requested content is saved and no newer unsaved revision would be lost; failure keeps the document and prompt |
| Don't Save | Cancel uncommitted automatic work and establish a close barrier; no delayed writer may resurrect discarded content. Already committed saves cannot be retroactively unsaved |
| Cancel close | Preserve document state and re-arm normal eligible scheduling without duplicate timers |
| Rename / Save As | Serialize with pending writes, advance path epoch, and invalidate old-path work; never recreate the old file |
| Workspace switch / quit | Stop admission, settle owned writes and dirty prompts before destroying buffers/services; no blocking I/O in Update and no silent dirty-work discard |
| Settings change | Recompute effective mode for all buffers, invalidate stale deadlines, retain ordinary editing/LSP behavior |
| Agent/external mutation | Verify expected disk identity; conflict rather than overwrite newer work |
| Restore / destructive Git operation | Quiesce automatic writes, settle dirty buffers with explicit choices, invalidate epochs, reconcile before resuming |
| Process crash | Acknowledge only completed commits; clean orphan temporary files safely on recovery. Do not promise persistence of still-unsaved edits |

No fallback may close a dirty document merely because its modal host is unavailable.
Shutdown cannot claim success while an owned transaction can still write afterward.

## 11. Implementation Phases

Each phase requires its own failing reproduction before the fix, focused tests,
and a coherent usable state. Feature-wide completion requires every phase.

| Phase | Work | Exit evidence |
|---|---|---|
| A: Correct annotation source | Structured hunks/deletions, baseline cache, generation keys, separate blame; fix misplaced markers before redesign | Distant/multiple-hunk tests fail old parser; actual annotation and Git agree on the `AGENTS.md` workload shape |
| B: Native gutter | Shared geometry, aligned numbers, rail/caps, themes, line selection, hunk and diagnostic inspection, actions | Paint and hit tests plus readable desktop/narrow visual acceptance |
| C: Save authority | Shared-buffer savepoint, immutable snapshots, workspace coordinator, checked writer, async manual save and close, safe explicit formatting | Fault-injected disk and revision races; no synchronous legacy product save/reload path remains |
| D: Synchronization | LSP save contract/order, exact watcher receipts, all-buffer reload/conflict handling, history/mutation leases | Fake-server wire log and real-server acceptance; self-events never reload; external events are not lost |
| E: Autosave product | Mode/delay settings, live inheritance, scheduling, focus reporting, quiet status, bounded retry and lifecycle | Off-by-default plus every mode, disable-in-flight, inactive tabs, close/discard/switch tests |
| F: Production acceptance | Integration, race, performance, cross-platform, visual and documentation gates | All gates below pass; unresolved platform limits are explicit, not hidden by green unit tests |

Primary touchpoints: `workspace/git_editor.go`, `types/types.go`,
`types/settings.go`, `tui/editor/{buffer,buffer_manager,edit,editor,watcher,git,render,events}.go`,
`tui/screens/shelleditor/`, `tui/viewmodel/{editor_git,editor_tabs,dirty_prompt,update,keyboard,command_palette}.go`,
`workspace/lsp_{manager,types}.go`, `tui/editor/lsp_integration.go`,
`tui/settings/settings.go`, `fileio/`, and workspace history/file-operation services.
New files should hold real owners, not parallel compatibility adapters.

## 12. Acceptance Matrix

### Correctness and interaction

| Scenario | Required assertion |
|---|---|
| Late single hunk and multiple distant hunks | Exact current line ranges; all unrelated rows remain unchanged |
| Insert/replace/delete, unequal replacement lengths, BOF/EOF/empty document | Correct kinds and deletion boundaries in both markers and inspection |
| Blank/repeated lines, patch-looking content, CRLF/BOM/final newline, tabs/emoji | Exact bytes preserved and calibrated paint/hit geometry agrees |
| Staged + unstaged + unsaved, untracked/unborn/ignored/conflicted, external commit | Correct explicitly named baseline/state; save does not clear Git markers |
| Number boundaries 9/10, 99/100, 999/1000; line numbers disabled; EOF | Stable right alignment, valid text origin, no phantom numbers |
| Marker and diagnostic on the same line | Independent visible lanes and correct click/hover ownership |
| Drag across gutter and outside viewport; keyboard hunk navigation | Exact line selection, normal capture/copy, no source/gutter hit confusion |
| Hunk changes while inspector is open | No stale content/action; normal selection and Escape restoration |
| Default/missing mode and old delay-only config | Zero automatic writes and zero save notifications |
| Delay edit burst, paste, undo/redo, replace, quiet idle | Latest content saved once after the deadline; non-text events do not reset it |
| Focus modes with editor popups/shared views/terminal blur | Only defined ownership transitions save; unsupported focus reporting is truthful |
| Slow write while typing or closing | UI keeps processing input; saved R cannot clear dirty R+1; discarded content never written later |
| Completion/diagnostics during autosave | No popup closure, cursor/selection movement, restart, duplicate change, or stale-result application caused solely by save |
| Explicit format-on-save vs automatic save | Explicit formatting is one undoable guarded transaction; every automatic mode saves exact text only |
| Delayed/early/duplicated self-event vs immediate external write | No self-reload and no broad time-based suppression of external changes |
| Permission failure, disk full, sharing conflict, cancellation, post-commit error | Honest outcome, preserved text/disk where applicable, bounded recovery and no repeated prompts |
| External writer, agent tool, history restore, rename, workspace switch | Ownership/preconditions prevent known stale overwrites or old-path resurrection |
| Application On / workspace Off / Reset / restart | Correct field-level inheritance and persisted explicit disable |

Use fake clocks, controllable writers, completion barriers, and a fake LSP server
to make ordering tests deterministic. Include a wire log for open/change/willSave/
didSave/close, negotiated `includeText`, failure, restart, and edit-during-write.
Verify with at least gopls and one additional available language server; record
unavailable integrations as blocked, not passed.

### Performance and visual gates

Measure small files, a 10,000-line document, a 100,000-line/large-byte document,
many open dirty buffers, and an editor alongside a dense resumed conversation.
Separate mutation, snapshot capture, background derivation, disk commit, and
presented-frame latency. Compare Off and On under the same workload.

- Warm gutter paint is viewport-sized with zero Git calls/diff work on paint,
  tick, hover, or source-unchanged tab switch.
- Repainting never triggers autosave, and clean idle has zero save writes/snapshot
  exports. One typing burst does not queue one full snapshot per keystroke.
- Writes/formatters/LSP stalls of several seconds do not block input, drag, tab
  switching, or the latest presented state. Preserve 16 ms active presentation
  targeting and the existing >=2 FPS idle contract, not frame suppression.
- Record p50/p95/worst mutation and presented latency, allocations, retained memory,
  Git invocations, bytes written, and LSP message counts. Set new work/allocation
  ceilings from repeated measurements; do not loosen existing benchmark gates.
- Use serial `scripts/tuishot.sh` captures at 160x40 and 80x24 in dark/light themes;
  include high-contrast/monochrome and a narrower geometry test. Capture clean,
  added, modified, deleted, diagnostic, hover, inspector, and saving/conflict states.
  Close every capture window. Do not update goldens before visual review.
- Exercise Linux, Windows, and macOS file replacement/permission paths through
  platform tests or cross-compilation, with runtime coverage where available.
  Test Kitty and a VTE terminal for glyph/geometry acceptance; do not conflate
  their differing horizontal-gesture delivery with gutter geometry.

Required commands at implementation completion:

```bash
go test ./workspace ./fileio ./types ./tui/editor ./tui/screens/shelleditor ./tui/viewmodel ./tui/settings ./tui/ui
go test -race ./workspace ./fileio ./tui/editor ./tui/viewmodel
go build ./...
go test ./...
golangci-lint run ./...
make bench-gate
make docs-check
scripts/tui_design_audit.sh
```

Synchronize `AGENTS.md`, settings/interaction/control/surface references, performance
baselines, and this plan as implementation lands. Do not describe proposed behavior
as current behavior before its acceptance gates pass.

## 13. Completion Checklist

- [x] Exact structured annotation replaces the broken rendered-diff parser.
- [x] Deletions, current baseline, stale results, and external commits are covered.
- [x] Gutter geometry, appearance, inspection, selection, and keyboard access pass.
- [x] Manual save and save-and-close use the one asynchronous save transaction.
- [x] Shared buffers have one text/savepoint authority; no detached reload path.
- [x] LSP ordering and exact watcher reconciliation pass race/failure tests.
- [x] Autosave is opt-in, setting changes are live, and automatic saves are text-only.
- [x] Conflicts, inactive buffers, disable, close/discard, rename, restore, and shutdown pass.
- [x] Performance, cross-platform, visual, lint, build, tests, and documentation gates pass.
- [x] Removed legacy paths and supported filesystem/terminal limitations are documented.

## 14. Post-Landing Audit Fixes (2026-09-18)

An independent pass over the landed save paths found three ways the §10 lifecycle
table could still be violated. Each is fixed and now carries a regression test that
fails against the pre-fix behavior:

- **Rename during a committed save.** The save acknowledgement was resolved by the
  path captured at request time, so a tree rename that landed first made the tab
  unreachable: the buffer stayed dirty against a stale disk baseline and the next
  save reported a false "file changed on disk" conflict. Acknowledgements now
  resolve by buffer lifetime (`editorTabByBufferID` in
  `tui/viewmodel/editor_tabs.go`), with the request path as a preference only.
- **Cancel close left no autosave deadline.** The dirty-close prompt cancelled the
  pending After Delay deadline and cancelling the prompt never restored it, so a
  dirty buffer could sit with no scheduled save until the next keystroke.
  `dirtyPromptCancel` now re-arms normal eligible scheduling without duplicates.
- **Bulk close discarded dirty buffers.** "Close others" and "Close to the right"
  force-closed every tab, losing unsaved work with no confirmation. Bulk closes now
  stop at the first dirty tab, open the unsaved-changes prompt, and resume the
  remaining walk only after the user chooses save or discard; cancel aborts it.

Coverage: `tui/viewmodel/editor_save_test.go` and `tui/viewmodel/dirty_prompt_test.go`.
