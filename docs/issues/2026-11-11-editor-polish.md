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

## Remaining acceptance

Verify actual browser clipboard input and the affected installed language servers
on the tester's workload after building the changed source. The complete
folding/expansion/preferences/semantic-workflow acceptance contract is in the
[editor parity handoff](../plans/editor-parity.md), without a phased rollout claim.
