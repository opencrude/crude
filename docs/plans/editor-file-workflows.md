# Editor File Workflows

> Status: IN PROGRESS. Unnamed files, the revised Save File window and strict
> Unicode integration pass the Linux milestone checks. Broader encoding-choice
> support, general conversion/Save As and native-platform acceptance remain open.

## Ownership

This plan owns general editor file behavior, not autonomous code intelligence.
Current contracts live in [Editor](../reference/editor.md),
[Settings](../reference/settings.md), [Semantic control](../reference/control-contract.md)
and [Core client protocol](../reference/core-client-protocol.md).
Language servers consume decoded source; they do not own file encodings.

## Decisions

| Date | Decision | From |
|---|---|---|
| 2026-11-25 | `crude PATH` with a file opens its parent folder as the workspace, opens the file, collapses the folder tree and starts no language server. Crude tells the user what it did. Verified in [the workspace entry record](../issues/2026-11-25-composer-entry-and-errand-setup.md#4-open-one-file-directly). | User |
| 2026-11-24 | Show the whole selected file in Find, Replace and Go to File. Start at the result and open the double-clicked source line. Open editable pasted text in a normal editor tab. | User |
| 2026-11-16 | Improve Find in Files performance; the current search feels slow. | User |
| 2026-11-14 | Restore the Save File picker header. Use bounded filename and encoding controls, with a real encoding dropdown rather than the stepper. Keep the shared form and window assets. | User |
| 2026-11-09 | A double click on empty main-tab rail opens a new unnamed buffer. An explicit save opens a practical save window using Attach Files as a visual reference. Automatic saving does not affect unnamed buffers; after the first explicit save they follow the normal setting. | User |
| 2026-11-09 | The save window must use visibly consistent form controls and offer encoding. Replacing a file asks a simple question; it must not open a diff automatically. Offer comparison through the existing comparison tab, without a second diff surface. Keep the UI clean and deliberate. | User |
| 2026-11-09 | Encoding is a first-class IDE responsibility, not a Save File decoration. Treat opening, edits, autosave, search, comparison, formatting, recovery and tool writes consistently, with the care expected of JetBrains IDEs and VS Code. | User |
| 2026-11-09 | Reuse Settings' native form, input and labeling assets. Do not add parallel controls or presentation logic. | User |
| 2026-11-09 | Keep feature plans with their actual owners. Integrate meaningful application/workspace settings, self-control and related capabilities; every flow must work within a terminal IDE's bounds. | User |
| 2026-11-09 | The examples are not the scope boundary. Own the feature's complete lifecycle and consumers without waiting for the user to identify each integration gap. | User |
| 2026-11-09 | Do not add fragile logic, agent-specific chores, arbitrary restrictions or pitfalls. Reuse the established owners, and justify bounds with concrete safety or resource requirements. | User |

## Current checkpoint

**Untitled editor buffers (2026-11-09).**

Goal: create and edit a new unnamed buffer in the pane that received a double
click on empty tab-rail space, then choose its name and destination when saving.
This editor plan does not close native-platform or repository-gate limits in
other feature plans.

Scope: use the core document owner for unique untitled identity, edits, history,
durable recovery and first-save transition. An unnamed buffer has no project
path and causes no project file or language-server activity before explicit
save. Preserve its identity and linked views when its first save supplies a
path. Restore open unnamed work across workspace and shell lifecycles. Closing
untouched work is quiet; closing changed work offers Save, close without saving,
and Cancel through the existing dirty-close path.

Ctrl+S on an unnamed buffer opens a native Save File window based on the Attach
Files browser: location and tree, search, shared filename and encoding form
fields, and Save / Cancel. Use UTF-8 by default. Retain the chosen encoding for
subsequent saves, autosave, disk reconciliation and reopening.
The same encoding contract applies throughout the IDE. Keep raw disk bytes
distinct from canonical UTF-8 text and source offsets. A shared strict codec
owns conversion; byte hashes, preimages, backups and transport remain exact.
Expose the current encoding and distinguish reopening bytes from converting
text. Conversion must not silently substitute characters, change line endings
or overwrite newer work. BOM detection is authoritative; ambiguous input needs
an explicit choice, not a guessed conversion.
Choose a safe destination through core-owned browsing and a revision-checked
commit. A pre-existing target needs an explicit safe-default confirmation and
the same preimage, conflict and history protections as other file mutations.
The confirmation shows the destination, not an automatic diff. Compare opens
the established main-area comparison reader only on request.
Cancel, errors and uncertain outcomes preserve the draft and never retry a
possibly completed write. Saving an empty untitled buffer still creates a file.

All autosave modes ignore unnamed buffers: no deadline, focus-loss write or
save dialog. An explicit successful first save moves the same document onto the
normal named-file save and autosave paths. No temporary path is exposed as a
project file or used as a substitute for document identity.

Exit: connected entry-point tests cover multiple panes, typing, undo, explicit
save and close, empty files, conflicting/existing targets, failure and retry,
late results, workspace switching and restart recovery. Test every autosave
mode before and after first save. Native desktop and constrained captures must
show the editor and usable Save File journey, including keyboard and pointer
input. Encoding tests also cover encoded files in project search/replacement,
Git and local comparisons, language-source reads, formatting, agent text edits,
and recovery. Run focused, race, build and relevant documentation gates without
weakening assertions or ceilings. Out of scope: a new scratch-file subsystem,
tool setup, or changing the existing New Session button.

Implementation evidence (2026-11-09): connected tests cover independent unnamed
buffers, linked-view editing and undo, close/cancel, all three autosave modes,
first save to a new file, and autosave after first save. Core tests cover
restart of unnamed text and history and an atomic no-replace create. Focused
race and affected-package tests pass. Linux native captures at 160×40 and
80×24 show the double-click, Save File window and saved tab. Windows has a
cross-build only, not native acceptance.

The reviewed replacement path uses an owner-bound preview, a retained Local
History preimage and the shared transactional writer. It refuses targets it
cannot review safely, including files outside the project, nonregular files,
already retained documents and files or drafts over the reviewed-edit bounds.
Connected tests cover Cancel-default confirmation, optional comparison and return,
and approved replacement; core tests cover stale targets and uncertain commits.
Pane-memory restoration, workspace-switch Save and returning after shell-profile
release have connected tests.

The 2026-11-09 visual review rejected the bare filename field, missing encoding
and automatic diff window. That presentation is replaced, not retained behind
another action. The new window uses the native Form, one title and one action
row. Native 160×40 and 80×24 captures cover keyboard saving, encoding choice,
Replace / Compare / Cancel and the existing comparison tab. A final constrained
Settings journey also saves a new-file default and creates an unnamed file with
that encoding. Its clipped BOM caption has a rendering regression; the shared
codec now supplies the concise `UTF-8 BOM` label instead of a separate UI name.

### Save File visual revision (2026-11-14)

The previous stepper and title-free body were rejected. Save File now uses the
shared Crude picker header, a short instruction, a bounded filename box and a
compact native select dropdown for encoding. The dropdown opens over the browser;
it does not move the fields or footer. Keyboard and pointer tests retain
filename editing, explicit choice, pending-save fencing and connected encoding
persistence. Native Linux captures at 160×40 and 80×24 verify the header,
bounded controls, open dropdown and an actual UTF-16 LE save. These captures
validate this revision, not the rejected stepper. Repository gate results are
reported separately; Windows/macOS UI acceptance is not claimed.

### File-decision handoff acceptance (2026-11-16)

The editor/file changes in `1b937e0b`, followed by the fixed-size wire decoder
repair in `01cb8d96`, have Linux native journey coverage through
`TestEditorFileFlowVisualAcceptance`. Ten sequential captures at 160×40 and
80×24 verify:

- Save / Discard / Cancel, preserving dirty text on Cancel and closing without
  writing it on Discard.
- The disk-conflict dialog, Cancel, Compare, per-change choices, and saving the
  combined result to the actual file.
- Two regions from a real Git merge, choosing current/incoming independently,
  then saving and staging the result.
- File icons in split panes and a real pointer tab move that restores the source
  pane's most recently selected file and focuses only the moved file.
- Real project search with an exact match above an earlier fuzzy suggestion,
  excluding a phrase assembled from unrelated prose.

The performance follow-up reruns those ten journeys plus two `dense-search`
captures. A 512-file project checks the 1,000-result limit, an exact hit in the
last file, keyboard navigation, replacement of the query, no-results state and
Open landing on the current match. All twelve native Linux journeys pass.
The [performance record](../reference/performance-baselines.md#find-in-files-scan-and-navigation-2026-11-16)
separates scanner and navigation measurements from physical-display latency.

Acceptance found and corrected two shared presentation defects: constrained
action bars now reduce gaps before clipping labels and retain their compact
captions when choices enable Save; terminal resize keeps modal decisions on
screen without replacing their content or focus. The headless counterpart checks
the same flows, but is not substituted for the native captures.
The full suite also exposed a selection-history mutation when pressing the
already selected tab. That selection is now a no-op; the existing all-edge
ancestor-drag regression and the MRU close/move checks remain unchanged.

All accepted captures exit cleanly and join their private runtime. The wire
decoder regression and repository gates are checked separately. Windows/macOS
component cross-builds are not native UI acceptance. This record does not close
the encoding and Save As scope below.

### Outside edits and comparison pages (2026-11-16)

User acceptance rejected the previous behaviour: an outside edit to a file
with unsaved changes stayed hidden until Save or close; the comparison page
repeated the file path and the editor's generic actions; its tab had no
tooltip; and settling the conflict elsewhere left the page stale with dead
controls. The replacement, recorded in [editor](../reference/editor.md) and
[main view panes](../reference/main-view-panes.md):

- The core applies outside edits at once: saved text follows the file with
  minimal edits; non-overlapping changes merge into unsaved text; only real
  overlaps become a conflict. One shell policy speaks only when unsaved work is
  affected, once per file until it is saved, batched; a conflict in a visible
  editor is the disk bar, not a notice.
- Comparison pages show the versions and the decisions only, follow their
  sources (including a file that changes again during a conflict) and close
  when nothing is left to choose. Every main tab kind and terminal tab has a
  tooltip.
- Linux native captures at 160×40 and 80×24 pass for the `disk` and `git` file
  flows; live tmux journeys checked merge, conflict bar clicks, the hidden-file
  notice, comparison refresh with kept choices, settling elsewhere and the
  saved bytes.

### Handoff and search verification (2026-11-16)

- All twelve Linux native journeys pass at 160×40 and 80×24, with clean exits,
  exact disk outcomes and inspected screenshots. Budgie notification-logo
  acceptance also passes through the actual freedesktop backend.
- Build, protocol-cohort freshness, whole-repository lint (zero issues), design,
  names and documentation checks pass. The complete `make bench-gate` passes,
  including the new search/navigation budgets; no existing limit was raised.
- `textsearch`, `fileio`, Find in Files and editor package race tests pass.
  Connected search/replacement, conflict, close and file-flow race journeys pass.
  The earlier exhaustive workspace/viewmodel race groups passed before the search
  performance follow-up; they are not relabelled as an exhaustive final-tree race
  run. Windows/macOS affected-component cross-builds pass, not native UI tests.
- **The final `make test` run is not green.** It stops in core group 2 at
  `TestSessionCoreCloseIsConsumedAndConcurrentCloseJoins`
  (`core/session_close_test.go:114`): the client reports `core client disconnected`
  instead of the expected lost-session ending. No core, client or transport
  shutdown source was changed. The failed group was not retried or suppressed.
  Every group the failure prevented from starting passes separately: core groups
  3–4, all five session groups, all nine viewmodel groups and all five workspace
  groups. This does not turn the failed full-suite command into a pass.

The requested performance improvement and feature visual acceptance are verified.
An all-green repository handoff remains open on the core shutdown-test failure.

## Integration and acceptance

| Area | Required outcome | Current evidence |
|---|---|---|
| Editor lifecycle | Core-owned text, stable identity, undo, linked views, dirty state, restore, first save and autosave immunity while unnamed | Connected/core and scoped race checks pass, including profile release/return and workspace-switch Save |
| Save window | Settings-style bounded filename and encoding dropdown, picker header, native editing/selection, correct focus and bounded actions | Linux 160×40 and 80×24 keyboard/pointer captures inspected; native Form input tests pass |
| Replacement | Cancel-default standard confirmation; optional existing comparison tab; return preserves destination and encoding; preimage/history/unknown-result safety | Connected confirmation/Compare/return and stale-target tests pass; core retained-preimage, restore and uncertain-commit checks pass |
| Encoding authority | One strict codec; canonical UTF-8 text; exact encoded disk hashes, receipts and preimages; no silent replacements | Four Unicode formats pass exact-byte, malformed-input, split-stream, first-save/autosave/reconcile/restart tests; UTF-32 is explicitly refused |
| Text consumers | Search/replacement, local/Git comparison and gutter, formatting, LSP sources, text tools and attachment previews decode consistently; byte operations stay byte-exact | Affected packages and scoped race checks pass; regressions cover encoded search/replace/preview, tool read/edit/write, Git JSON transport/hunk stage and execution integration/undo |
| Editor controls | Visible current encoding, explicit conversion versus reopen, protection of dirty work, pending operations and late results | Connected menu conversion, reload, autosave exclusion and undo pass; stale encoding-choice fence passes ordinary/race checks |
| Preferences | Only useful policy: a default for new file encoding at application/workspace scopes, explicit per-save override, ordinary inheritance/reset/live save | Sparse save/explicit UTF-8/reset tests pass; native 80×24 preference save and new-buffer inheritance observed; all four captions pass the narrow rendering regression |
| Self-control | Discoverable document/encoding operations and exact native controls; resolve real effects, owner, revision and outcome through the same commands | `TestControlUnnamedEncodingJourney` passes settings → new → edit → first save → convert → reload through the real core router; pathless edits have no invented file claim |
| Terminal bounds | Keyboard and pointer; readable at 80×24 and desktop; no hidden decisions, clipped choice lists, overlapping headers or duplicate diff | Final 80×24 and 160×40 captures inspected; Save File, optional comparison, all menu choices and the full `UTF-8 BOM` caption are readable; preference save reaches a new unnamed buffer |
| Verification | Focused, affected-package, race, build, lint, docs and performance gates; native acceptance distinct from cross-build | Handoff/search native, scoped race, build/lint/docs and complete benchmark gates pass. The final full suite retains the core shutdown-test failure recorded above; Windows/macOS cross-builds are not native acceptance |

### Linux verification record (2026-11-09)

- The repository command (`DESKTOP_SESSION= go test ./... -p 2 -count=1
  -timeout=180s`) passes 142 packages. Only `tui/viewmodel` fails, at its
  three-minute aggregate limit, without an assertion failure. The session
  package, including its previously intermittent media check, passes.
- The final viewmodel binary discovers 1,492 top-level test/example/fuzz cases.
  All four exhaustive 373-case shards pass from the package directory with
  the same 180-second limit. The existing `scripts/test_race.py` partition
  function verifies complete, nonduplicated coverage. Normal opt-in skips stay
  in place; native visual tests run separately through `scripts/tuishot.sh`.
  Shard success does not relabel the unsharded timeout as a pass.
- Affected-package and scoped race journeys pass for unnamed documents,
  first-save/replacement, encoding boundaries and consumers, settings, connected
  conversion/reload/undo and lifecycle recovery. The final stale encoding-choice
  fence and the complete settings/codec packages also pass under `-race`.
- Wire generation freshness, native `go build ./...`, whole-repository lint,
  `make design-audit docs-check` and `make bench-gate` pass.
  No assertion, timeout, performance ceiling or visual golden is relaxed.
- Final sequential 80×24 and 160×40 captures exit cleanly and leave no capture
  window. The narrow journey includes pointer Replace/Compare, keyboard Cancel,
  returning from comparison, preference save and a new buffer using the saved
  default. Windows/macOS cross-builds pass; neither native platform is accepted.

## Remaining scope and limits

- This milestone supports UTF-8, UTF-8 BOM and BOM-marked UTF-16 LE/BE. It does
  not guess legacy codepages or select an encoding for BOM-less UTF-16.
  Reload is BOM-driven, not a manual decoder chooser. The explicit-choice
  requirement above remains open; no VS Code or JetBrains parity is claimed.
- Named-file conversion and replacement retain the existing reviewed writer's
  project-containment, regular-file and 2 MiB bounds. That writer retains the
  complete immutable before/after images and a Local History preimage; ordinary
  editing and preserving an existing encoding do not add that review step.
  General large-file conversion requires a deliberate extension of that owner,
  not a second writer or an unexplained agent restriction.
- Named-file Save As does not use the new first-save window.
- Windows and macOS native behavior is not verified by cross-compilation.

## Research

- [VS Code basic editing](https://code.visualstudio.com/docs/editing/codebasics):
  file encoding is an editor property and has explicit save/reopen actions.
- [IntelliJ IDEA encoding](https://www.jetbrains.com/help/idea/encoding.html):
  distinguish Reload from Convert; BOM supplies an explicit encoding; defaults
  do not justify silently changing existing bytes.

Do not equate a four-choice save control with IDE-wide encoding support or claim
parity from codec unit tests. Close this plan only on complete integrated evidence.
