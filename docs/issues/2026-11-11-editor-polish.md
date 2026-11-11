# Editor completion, paste and language-server capacity

> Status: IN PROGRESS. Source fixes and focused/native checks are in place.
> Repository validation remains open after a workspace memory-safety crash.
> Evidence is recorded below. Broader editor parity remains
> in [Complete editor experience](../plans/editor-parity.md).

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
caret or first selected field just after an opening parenthesis. Sustained
signature help across all argument editing remains open.

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

## Remaining acceptance

Verify actual browser clipboard input and the affected installed language servers
on the tester's workload after building the changed source. The complete
folding/templates/preferences/semantic-workflow acceptance contract is in the
[editor parity handoff](../plans/editor-parity.md), without a phased rollout claim.
