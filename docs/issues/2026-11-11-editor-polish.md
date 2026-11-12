# Editor completion, paste and language-server capacity

> Status: IN PROGRESS. The editor fixes and LSP continuation have Linux
> verification. The original Windows memory-safety crash and tester-workload
> acceptance remain open. Broader editor parity remains in
> [Complete editor experience](../plans/editor-parity.md).

## Report and causes

The user reported that accepting a completion with Tab stopped recommendations
inside the inserted parentheses; JSON pasted from a web page acquired scrambled
line breaks; and a tester's 4.1 build waited indefinitely for a language server.
They also requested folding and useful code templates, and asked for a broader
technical audit instead of treating these examples as the scope boundary.

- Active snippet navigation was treated as exclusive ownership of all automatic
  completion. Ghost preparation also rejected snippets. A function could open its
  first field correctly but then suppress the suggestions needed to fill it.
- Editor paste inserted terminal-delivered bare CR bytes literally. Composer and
  generic multiline controls stripped CR instead. CRLF, LF and CR need one paste
  decoding policy, distinct from byte-preserving load/save and programmatic edits.
- The core admitted one quarter of GOMAXPROCS, clamped to 1–8 servers, while the
  workspace default allowed two and Settings allowed up to 32. Raising the
  workspace setting could not overcome the hidden lower core ceiling.
- Further inspection found an inert comment command for block-only syntax,
  forced four-space indentation, extra closing-line padding when splitting a
  bracket pair, and editor preferences without consumers.

The supplied JSON and screenshots were evidence, not files to rewrite. No change
here repairs an already-saved historical file or updates the running 4.1 binary.

## Changes and boundaries

Suggestions can run inside snippet fields. Tab accepts visible inline text or a
chosen row before advancing to the next field. Plain completion inside a field
keeps its mirrors. Function acceptance requests signature help when it leaves the
caret or first selected field just after an advertised signature trigger. The
continuation repair maintains help during native argument typing, deletion,
line breaks, indentation, caret keys and snippet-field navigation. Suggestions
and hints coexist, and only current acknowledged answers are displayed. Server
trigger/retrigger context travels through the connected LSP path.

`ui.PastedLineBreaks` is the shared decoder at shell and direct control paste
entry points. The editor then applies its existing first-line terminator.
Escaped JSON characters are not decoded as control characters. Paste remains one
undo transaction, including selection replacement and snippet mirrors.

The core's 32-process ceiling now matches the largest permitted workspace quota;
the workspace default is six. Explicit lower settings remain in effect. Waiting
status names either the workspace quota or shared capacity. This is process
admission, not memory/CPU containment or a guarantee against every server failure.
Problems also preserves a waiting explanation when it has no action button; it
no longer substitutes Language settings or claims “No problems” from an earlier
check while the active language is waiting or failed.

Comment toggle uses catalog block delimiters where no line prefix exists, avoids
nesting already-complete comments, and leaves unsafe partial delimiters intact.
Indentation commands share bounded source-style detection cached by revision.
Tab advances to the next indentation stop; Enter consumes padding between a pair
before moving its closer onto an aligned line. Configurable display tab width,
EditorConfig and general syntax-aware dedent are not implemented by this change.

Current contracts: [Editor](../reference/editor.md),
[text input](../reference/text-input.md),
[settings](../reference/settings.md),
[interaction matrix](../reference/interaction-matrix.md).

## Verification

- `TestProblemsPreservesWaitingWithoutSettingsAction` reproduced the dropped
  message/false clean state, then passed through the actual 80-column screen.
- Full editor, UI and input package tests pass. New direct-control paste tests
  first reproduced line joining as `onetwo` instead of two lines, then passed
  after routing those entry points through the shared decoder.
- Focused viewmodel tests pass for accepted argument fields, chosen/unchosen Tab
  precedence, composer/editor paste, connected snippet history and ghost behavior.
  Restoring the old snippet guard reproduced the argument-completion failure.
- Capacity/status regressions cover ordinary mixed-language projects, an explicit
  one-server quota, cross-workspace waiting, release/start and every permitted
  workspace quota against the real core constructor.
- Native Windows Terminal captures at 160×40 and 80×24 used
  `TestEditorPolishNative`: real Tab then Ctrl+/ input changed the source and last
  painted frame as expected. Both wrote the success sentinel after the renderer
  stopped. Capture windows were closed. This is native indentation/comment
  evidence, not installed-language-server or browser clipboard acceptance.
- The stale Windows launcher baseline was visually inspected using
  `TestLauncherNativeGolden` at 110×16, then synchronized. No Linux baseline or
  product paint was changed to suppress the mismatch.
- Warm indentation lookup over 30,000 source lines measured 3.8–4.6 ns/op and zero
  allocations in three runs on Windows amd64, i7-9850H. This is cache lookup cost,
  not mutation-to-display latency or cold scanning cost.
- The full suite exposed aggregate app/workspace deadlines. The runner now uses
  its existing exhaustive name-based grouping for these packages too; every
  discovered test/example/fuzz case still runs once, with unchanged 180-second
  normal and 300-second race group deadlines. Runner tests guard both modes and
  failure propagation. An isolated settings case passed; the prior complete app
  run took 114 seconds, and the later aggregate timeout occurred during a
  three-second-old SSH case, not a hung 180-second single case.
- Build, lint, source-cohort generation/check, design audit, names gate and docs
  gate passed during the repair. The full benchmark gate also passed without
  changing ceilings. Dense 20,000-line snippet painting measured 300 allocations
  with eight mirrors and 567 with 64, below the existing 1,110/1,320 limits.
- Native Windows race tests passed for editor/UI/input/governor packages, focused
  connected snippet and argument-completion journeys, and workspace/core capacity
  regressions. This is focused race coverage, not the full repository race suite.
- Website lint, build and Python acceptance tests passed with the existing pinned
  dependencies. No website source or deployment changed.
- The repository sweep passed the ordinary packages and all app/session groups,
  then found an old four-space expectation in the completion acceptance-key test.
  The expectation now checks the intended next-tab-stop result (`pr` plus two
  spaces), and the focused test passes. All four viewmodel groups then passed,
  as did the first three workspace groups. The fourth workspace group crashed
  during GC. Race and GC-pressure runs of that exact group passed without a code
  change; this does not resolve the original failure. See the
  [workspace GC investigation](2026-11-11-workspace-gc-crash.md).

### LSP continuation (2026-11-11)

The follow-up keeps the existing LSP completion engine, not an AI service or a
custom template library. The earlier HTML/C/Lisp constructs were examples.
Static capabilities are copied from the initialized server for each open file;
completion carries the advertised trigger or incomplete-list refresh reason.
Signature refresh preserves the exact prior server answer, including while a
new request is unanswered. The core rejects intent from a replaced server.

Two startup regressions were reproduced and fixed. An explicit Ctrl+Space or
Alt+I action must not be discarded because its advisory capability snapshot has
not arrived. Native typing during initialization retains one bounded intent,
then queries only an identifier or an actually advertised character after both
source acknowledgement and capabilities arrive. Unsupported punctuation makes
no request. No language-specific branch or hidden inference request was added.

- `TestLSPAdvertised*`, `TestConnectedCompletionTriggerAndIncompleteContexts`,
  `TestPendingCompletionDoesNotInheritReplacementServer`,
  `TestExplicitAssistanceSurvivesCapabilityPublicationLag`,
  `TestAutomaticCompletionWaitsForInitializingFileCapabilities` and
  `TestSignaturePendingRetriggerPreservesOriginalContext` cover native input,
  connected two-shell edits/Undo, delayed receipts and replacement boundaries.
  The final focused group and startup/delayed-response race checks pass.
- `TestAssistanceCapabilitiesAndRequestContextsOnWire` verifies actual framed
  initialize/request JSON, including non-dot characters, incomplete refresh and
  original signature markup/offsets. Transport, replica and manager-fence checks
  pass, including pure-Go and Windows/macOS component builds. Cross-building is
  not native platform acceptance.
- Linux `scripts/tuishot.sh` captures of `TestAutomaticSignatureVisualAcceptance`
  use installed gopls v0.21.1 at 160×40 and 80×24. The final frozen-source capture
  verifies all ten boundaries at each size: argument typing, both parameter
  positions, readable coexisting suggestions/hints, Escape order, the explicit
  reader and unchanged disk/private draft. Screenshots were inspected; both
  servers and private cores joined, profiles were removed and windows closed.
  A prior desktop capture injected `U` instead of comma through the host's
  multi-layout key mapping. That attempt is rejected. Final capture sends literal
  comma and requires every phase plus the success and joined-cleanup markers.
- The first repository sweep exposed the manual-request startup defect. Another
  overlapped generated-file writes and is not a gate pass. The latest focused
  fixture correction supplies the normalized filter text that real core rows
  contain; it does not change product ranking or weaken the painted-outcome check.
- Final Go 1.27.4 Linux validation: `python3 scripts/test_go.py` passes all
  ordinary packages and all 17 large-package groups. Build, wire freshness,
  design/names/docs, pure-Go and nested LSP/syntax checks, compatible
  golangci-lint v2.15.0 (zero issues), and website lint/build/acceptance pass.
  The newer lint check exposed ignored error-channel joins: tests now retain
  their cleanup evidence, and command cancellation/timeout retains its secondary
  I/O error. No rule or existing assertion was disabled.
- Race coverage is exhaustive across the same discovered groups, with an explicit
  execution caveat. The umbrella `scripts/test_go.py --race` command hit the
  agent's 45-minute command limit during workspace group 3, after every other
  package/group and workspace groups 1–2 had passed. Groups 3–4 were then completed
  separately (73.141 s / 51.444 s), with exact discovered membership and unchanged
  300-second group deadlines. Final affected-package race rechecks also pass.
  There are no race reports. This is complete grouped coverage, not a claim that
  the interrupted umbrella command passed.
- `make bench-gate` passes all 62 allocation checks without changing a ceiling.
  Dense 20,000-line snippet painting uses 292 allocations with eight mirrors and
  475 with 64, below 1,110/1,320. Dense 500-message width reflow uses 310,785 B and
  1,569 allocations, below 455,000 B / 2,180. These are benchmark allocations,
  not a physical display-latency guarantee.

The existing Windows GC finding is still open. None of these Linux passes
identifies its cause or establishes a Windows fix. Commit-character acceptance,
indentation-adjusting insertion modes, dynamic capability registration,
Emmet/postfix integration and the broader parity plan are not delivered by this
continuation. No local model is installed or used.

## Everyday commands and sole suggestions (2026-11-11)

The native editor now owns one typed command path shared by keys, the context
menu, the searchable **Editor actions** picker (Alt+/), and exact-view semantic
controls. Ctrl+D duplicates; line join/insert/move/delete, case conversion,
sort/reverse/transpose, paragraph/name-part motion, Select Line and reversible
selection growth use the same transaction and history. Deferred commands retain
an action ID behind shared Undo and survive private recovery. Current native
node ranges and lexically qualified bracket endpoints provide structural selection,
matching-bracket movement and paint; stale syntax cannot supply source locations.

Tab accepts the sole visible supported completion without Down, including a
non-inline fuzzy match, snippet fields and additional import edits. The original
server item supplies the edit; labels do not. Enter, multiple choices, stale rows,
and the acceptance preference retain their distinct behavior.

- Direct and connected regressions cover text/selection bytes, CRLF/graphemes,
  read-only/no-op cases, two shells, author Undo/Redo and Save, stale picker/menu
  tokens, exact-document controls and queued-action recovery. The original
  missing Ctrl+D, sole-row acceptance and structural-selection failures were
  reproduced before implementation.
- Linux native captures at 160×40 and 80×24 verify all ten action boundaries and
  all five sole-completion boundaries. Inspected frames show duplicate/Undo,
  a filtered Join lines picker, bracket paint, expansion/shrink, the unchosen
  single row and the accepted snippet/import. The sole fixture uses a deterministic
  protocol reply to isolate this path; it is not installed-server evidence.
  Each fixture records successful joined cleanup. Windows were closed; the user's
  current core was not stopped or replaced.
- The first captures exposed two driver defects: inherited desktop modifiers
  changed literal typing into shortcuts, and input could overtake an observer's
  completion. The shared script now clears modifiers for literal text and waits
  for fresh exact ready markers from the original live capture. Rejected captures
  are not passes. Alt+Q is the deliverable shrink shortcut; shifted arrows extend
  name-part movement without relying on ambiguous shifted Alt-letter sequences.
- The full ordinary suite passed. Final affected editor/UI/syntax packages and
  connected journeys pass after the last changes. Focused race checks and the
  separate native binding suite pass, with no race reports. Build, fresh generated
  codecs/cohort, lint (zero issues), design/names/docs and site checks pass.
  Windows amd64 and macOS arm64 native-parser component builds pass; they do not
  establish native Windows/macOS UI or GC acceptance.
- The first allocation gate failed: structural traversal allocated a Go node per
  visit (65 allocations versus the unchanged 64 ceiling in the small preparation
  fixture). Profiling identified the binding allocation. `TreeCursor.NodeValue`
  now borrows the same native tree by value, with an identity/zero-allocation
  regression. Final preparation measures 48 allocations for 200 lines and 67
  for 20,000, below the unchanged 64/96 ceilings. The complete benchmark gate
  passes. Timing and incremental costs are recorded in
  [performance baselines](../reference/performance-baselines.md#structural-editor-analysis-2026-11-11).

This closes the everyday-command checkpoint, not all editor parity. Configurable
geometry, folding/wrapping, multiple carets, remaining negotiated LSP workflows
and the original Windows GC finding remain open. No local model was added.

## Editor display preferences (2026-11-11)

The existing Line numbers, Syntax colors and Show whitespace preferences now
have consumers in the shared editor. New/open/linked views and peer settings
publications use the same path. Reset restores inherited values without changing
selected source bytes or Undo. Syntax palette changes reproject only color;
spaces/tabs are paint-only and cannot enter copy, search, save or inline hints.
The diagnostic/change gutter lanes remain present when numbers are hidden.

The connected regression initially failed because saved settings had no visible
effect. It now passes at 160×40 and 80×24 through actual form keys and Save/Close,
two shells, linked and newly opened views, reset and subsequent typing/Undo.
Direct checks cover exact whitespace cells, unchanged source/history, inlays,
diagnostics, cached geometry, parser identity and readable distinct palettes.
Native captures at both sizes verify all four boundaries and successful joined
cleanup; inspected frames show the existing preferences page and changed source
ink/markers without line numbers. No user core was replaced or stopped.

Affected editor/theme packages, connected editor/history/insight/split/theme/save
checks, focused race checks, native build, zero-issue lint and design/docs pass.
The unchanged complete benchmark gate passes. Three quiet 500 ms repetitions on
Linux amd64 measured 0.183–0.188 ms for disabled whitespace, 0.196–0.198 ms for
boundary markers and 0.197–0.199 ms for all markers on a 120×40 grid over 10,000
source lines. These include a newly allocated test surface (about 200 KB and
181–185 allocations per operation), not terminal display latency. No gate was
loosened. The previous full suite and site results are not relabelled as a new
full run for this checkpoint.

Tab-size geometry, wrap/fold projection and the other remaining editor workflows
are still open. This checkpoint is not a claim of full IDE parity or a Windows
GC fix.

## Source-anchored tab geometry (2026-11-11)

Tab size now supplies the one shared display width for each source table.
All source/cell consumers use it: rendering, whitespace, pointer hits, diagnostics,
search results, inlays, ghost text, completion/signature context and navigation.
Linked views retain their own viewports but cannot interpret the same source with
different tab widths. Undetected indentation uses the configured width; detected
space/tab indentation remains distinct. No source is rewritten on a preference change.

History snapshots, restored selections and Back/Forward locations retain source
anchors instead of old display columns. Private editor recovery is now payload
version 2 with UTF-8 byte offsets. The current local client-state database was
inspected read-only before this change: it had no editor-recovery tables/records,
so no existing recovery needed conversion and no user database was written.
No compatibility reader was added. Other profiles with old payloads must be
backed up and aligned outside product code before using the changed format.

Independent regressions first failed on the fixed four-column geometry. They now
cover 4→2→8, combining marks, wide glyphs, CRLF, selected source, linked panes,
search/diagnostic/hint positions, pointer editing, unknown-receipt recovery,
completion with an import and snippet, author Undo/Redo, Save and unchanged drafts.
Tab/Backspace on new content follows 1/2/8/32-column defaults without overriding
detected indentation. The 160×40 and 80×24 native journeys retain a selected source
word across Settings Save/Close and show it at the new column.

Native review also exposed clipped number-unit captions. The shared form primitive
now reserves the unit's width in both paint and input geometry, including compact
rows. Tests cover narrow widths and native edits. Corrected captures show the
complete **columns** caption and joined cleanup at both sizes.

The exhaustive normal suite exposed two older presentation assumptions: the
syntax test expected application ink rather than the selected source palette,
and the gutter test clicked an explicitly hidden number lane. The tests now use
the selected palette and enable the lane they exercise; the gutter check requires
complete selected source lines. The original failed groups and all remaining
viewmodel/workspace groups then passed without changing deadlines or skipping cases.

The allocation gate caught redundant byte-anchor capture for every snippet mirror.
Profiling attributed 63% of allocated objects to position conversion, mostly from
per-edit after snapshots overwritten by the transaction boundary. The existing
batch/history owner now captures the view once per transaction (once per batch
when ungrouped), retaining every sparse source edit. Three repetitions measured
106 allocations for eight mirrors and 283 for 64, below the unchanged 120/330
ceilings; mutation took 0.764–0.786 ms and 0.818–0.842 ms respectively over 20,000
lines. These are mutation measurements, not terminal presentation latency.

Final affected race checks, build, zero-issue lint, generated cohort/codecs,
design/docs and Windows/macOS component builds pass. The complete unchanged
benchmark gate passes after the batch fix. The source-caret cache measures
3.08–3.29 ns with zero allocations on a two-megabyte line; unchanged shell updates
do not repeat a source conversion. The full ordinary suite is accounted for by
the initial passing package/app/session groups plus the corrected exhaustive
viewmodel and workspace groups, not by relabelling the failed umbrella command.

File-specific indentation choices, EditorConfig, wrapping/folding and the other
remaining parity rows stay open. Linux verification does not settle the separate
Windows GC finding.

## Wrapped and folded editor rows (2026-11-11)

One view-local source-row index now owns wraps, collapsed ranges, paint, scroll,
pointer/drag, caret and popover geometry. Nested native source ranges and qualified
lexical brackets/comments/strings feed folding; stale results cannot supply new
ranges. Commands and gutter actions share the native executor. No source bytes,
provider coordinates, shared history or dirty state change when a block folds.

Current/private/linked rows, CRLF, tabs, combining marks, wide glyphs, copied hidden
source, search reveal, disjoint and touched peer edits, width changes and retained
profile folds have behavioral regressions. Cold source work was measured at
45–48 ms before changes. It is now a cancellable worker, not UI work; warm paint
and width-only reflow are independently measured and guarded. Large-line changes
use that same worker while current source stays editable. Fold-only geometry stays
cached and visible while new wrap metrics are pending.

Linux native 160×40 and 80×24 captures verify seven interaction boundaries:
wrapped source, Fold, Unfold, nested gutter fold, Go to line reveal, Ctrl+D and
one Undo. Inspected frames show hidden-line counts and continuation ink. Both
record joined cleanup with unchanged disk bytes; every window is closed. The first
chord was not delivered correctly by the terminal and was replaced with Alt+-/Alt+=.
The pointer diagnostic proved no click reached the product while desktop modifiers
were held; the shared capture driver now clears modifiers for click/wheel input,
as it already did for keys. Failed captures are retained as failures, not passes.

The ordinary suite was disrupted by running wire generation concurrently with
compilation; every affected build/golden package and the complete app/session/
viewmodel/workspace inventory then passed after generation finished. The only
intentional golden change is the source fold chevron. Find-in-files kept its
original golden after correcting empty gutter ink. Final affected race checks
pass, including the shared ANSI module, without race reports. Build, zero-issue
lint, generated cohort, design/docs and Windows/macOS component builds pass.
The complete benchmark gate passes with all existing ceilings unchanged and five
new wrap checks. A simultaneous build/lint run first pushed the existing chat-find
amortized allocation measurement to 4,606 B (ceiling 4,500); three isolated runs
measured 3,028–3,104 B, and the complete quiet gate then passed without changing
chat code, its benchmark or any ceiling. The failure is not relabelled a pass.
The existing Windows GC finding remains open. This is not full editor parity.

## File indentation policy (2026-11-11)

The source table now owns sparse explicit, project and inherited policy layers.
The pinned official EditorConfig parser handles parent traversal, section/glob
precedence, root and unset. Core reads are owner/path fenced and initial open
preparation installs policy before the editor accepts its first key. Retained
refreshes are bounded, cancellation retires the exact request before retry and
workspace/shutdown use the existing joined client-I/O lifecycle. No project code
runs and no source is rewritten on open.

The header menu, searchable actions, existing Settings form and exact-view semantic
control reach the same owner. Private client profile return preserves overrides;
peer shells follow their own choices. Saved project changes and explicit Reload
refresh inheritance, while invalid config retains the last valid geometry and
exposes a recovery action. Explicit Reset cannot be overwritten by a late restore.

Independent source regressions exposed insertion before an existing tab, partial-tab
Backspace/outdent and selection drift after a body tab. Indent/outdent/conversion
now derive complete leading runs and retain source anchors. CRLF and untouched body
bytes survive one-step Undo. The new file policy never joins canonical history.

Native 160×40 and 80×24 journeys verify status clicks, policy menu, width input,
conversion through typed action search, one Undo and Reset. The first functional
32-row chooser was rejected during visual inspection as too tall. It was removed
and replaced with the existing compact text prompt, with selected current value,
range errors, stale-owner rejection and Escape cancellation. Both replacement
captures verify seven phases and joined cleanup with unchanged disk bytes. This
is deterministic EditorConfig/native input evidence, not language-server evidence.

Affected ordinary and race checks, build, zero-issue lint, design/docs and
Windows/macOS component cross-builds pass. A names-gate failure exposed a duplicated
configuration filename; the watcher now uses the official parser's filename
contract through the indentation owner, without an exception or weaker gate.
The original whole-suite leaf inventory passed except the names finding; its
fixed package and the complete app/session/viewmodel/workspace grouped inventory
then passed. The final compact-prompt race/lint/docs checks and full unchanged
allocation gate pass. Cached dense indentation resolves in 48.9–49.7 ns with
zero allocations (three Go 1.27.4, GOMAXPROCS=2 runs).
The Windows GC finding and the remaining editor acceptance rows stay open.

## Multiple selections (2026-11-11)

The editor now owns immutable private UTF-8 selection sets, per-caret motion goals
and one simultaneous edit vector. Native occurrence commands, adjacent carets,
Alt-click, captured Alt-drag rectangles, typed/pasted replacement, pairs, line
commands, conversion, expansion/shrink, copy/cut and author Undo use those owners.
Overlapping peer ranges coalesce; identical line edits run once. Lost or conflicting
coordinates cannot partly edit a command. Numeric snippets and completion have an
explicit single-caret policy rather than silently applying to one member.

New regressions initially failed on primary-only edits, lost vertical goals,
duplicate edits after a peer deletion, source-reset residue, hidden caret paint,
and old tab-width goals. The fixes keep source, painting and geometry ownership
separate. Recovery preserves pending source and all ranges and rejects split
Unicode/CRLF boundaries. Exact-view control tokens include the whole private set.
Two-shell tests retain peer text and private drafts; no source file is saved by
these editing journeys.

The first native rectangle journey failed because Shift+Alt selected terminal
output and delivered no mouse event. That gesture was removed. Alt-click adds or
removes a caret and Alt-drag forms the rectangle through the existing captured
gesture. Ctrl-click navigation remains unchanged. Replacement Linux captures at
160×40 and 80×24 verify all eleven phases, source selection paint, header count,
Ctrl+D, Undo, rectangle editing, Escape, unchanged disk and joined cleanup. Representative frames at both sizes were inspected; no capture window remains. This is native input evidence,
not installed language-server or Windows acceptance.

The full ordinary repository inventory passes. Affected editor/UI/shell/client
and connected history/snippet/completion race paths pass; final geometry regressions
also pass under race. Build, zero-issue lint, design/docs and Windows/macOS component
cross-builds pass. Initial lint findings were corrected, not suppressed. The
existing allocation gate passes unchanged; new dense-edit measurements and budgets
are recorded in the performance reference. The extended allocation gate also
passes, including all twelve new multiple-caret workload/stage budgets.

## Selection formatting (2026-11-11)

Format selection now follows the selected language server's advertised standard
range-format capability. Native Alt+F8, menu, searchable Editor actions and the
exact-view semantic action share source/selection fencing. Complete ranges are
validated before a simultaneous edit and one author Undo group. Whole-document
catalog formatting is not a fallback; unsupported/empty/invalid responses do not
write text or disk. Strict raw parsing, UTF-16 positions, outside-range refusal,
multiple selections, private drafts, stale menus and changed selections have tests.

Fault injection found that passive LSP reads occupied the file-effect lane. A
blocked formatting response held a second shell's edit until the test deadline.
The core now uses its existing bounded/joined jobs independently for passive reads;
mutating/lifecycle/review operations stay ordered. The peer's real key now commits
before the delayed reply is released, and that obsolete reply cannot change text.
A second failure exposed a fast answer ahead of its context publication. Retaining
the exact answer until its clock arrives fixes that ordering without a sleep.
Applying that retained answer must precede document-command draining in Update;
otherwise the author changed while the peer waited for another input.

Installed clangd 18 passes range formatting and Undo. Linux native captures at
160×40 and 80×24 verify six phases, including actual selection, Alt+F8, Undo,
action-picker filtering and a second Undo. The other two unformatted lines stay
unchanged, disk stays unchanged, private drafts remain and provider shutdown joins.
The initial capture failed because Alacritty's default Shift+End binding sent no
key; recorded input proved that boundary. The replacement selects through native
Shift+Right. Representative frames at both sizes were inspected and no capture window remains.
Build, lint, generated-wire checks and Windows/macOS component builds pass.
The full ordinary inventory is accounted for by the initial passing groups and the
remaining viewmodel/workspace groups after correcting an obsolete test that required
Format selection to be absent. That regression now checks disabled-without-selection
and useful no-server feedback, not absence of the implemented feature. Affected
race paths and the unchanged allocation gate pass. The final dirty-buffer/menu race
command first expired during build without a test result; its separate completion
passes both regressions with the original 90-second test deadline.
This is not native Windows acceptance or a resolution of the open GC finding.

## Editor orientation (2026-11-11)

Minimap and indentation guides now consume the existing Editor page's saved
preferences and share the source geometry and scrollbar owner. Native 160×40 and
80×24 captures pass eight phases: change preferences, Save/Close, source paint,
click, captured drag, typing and Undo. Representative final frames were inspected
at both sizes: labels and controls fit, guides stay in indentation, the viewport
tint follows the map, and Undo returns to the retained selection. Disk and private drafts
stay unchanged, shutdown joins and no capture window remains.

Tests first reproduced an inert minimap preference and a wrapped scrollbar offset
being copied into a logical source-line field. The shared visual-row conversion
fixes both ordinary scrollbar and map navigation. The connected journey then found
that author Undo could restore an off-screen caret after an independent scroll;
canonical history now reveals its restored view, like standalone Undo. Linked and
peer views keep their own carets.

Additional coverage catches blank-looking wrapped continuations, hidden rows,
wide/combining text, tab policy, narrow layouts, captured release outside the bar,
cache retirement, live peer preferences and Reset. Prefix-limited samples beyond
their budget show an ellipsis, not false empty source. Preparation and warm paint
costs are measured separately in the performance reference. The first exhaustive
inventory exposed an unintended foreground change in inactive scrollbar cells.
Keeping the source surface's existing blank fill fixed both affected golden tests;
no baseline changed. Final affected race, build/lint/docs and component cross-build
checks pass. The complete ordinary inventory is accounted for by its initially
passing packages and the corrected golden/editor plus app/session/viewmodel/workspace
groups. The allocation gate exposed repeated line copies in multi-caret Undo;
profiling and a failing zero-copy regression led to the shared byte-slice grapheme
path, not a higher ceiling or disabled worker. Its focused Unicode/editor tests
pass and Undo allocations fell by more than half. A separate wrapped-typing gate
failure was traced to copying the entire source for a one-grapheme Backspace read.
The source-range owner now copies only the requested bytes; its regression fails on
the old implementation and preserves Unicode, CRLF and detached results. Five
isolated measurements are below the unchanged limit. The complete extended gate
passes: wrapped typing and paint measured 6,021,472 B and 157 allocations against
the unchanged 6,950,000 B and 207 allocation ceilings. Final affected tests and
race checks include the source-range correction.
This is Linux native evidence, not native Windows acceptance or a GC-crash fix.

## Remaining acceptance

Verify actual browser clipboard input and the affected installed language servers
on the tester's workload after building the changed source. The complete
folding/expansion/preferences/semantic-workflow acceptance contract is in the
[editor parity handoff](../plans/editor-parity.md), without a phased rollout claim.
