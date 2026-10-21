# TUI Performance Baselines

> Status: Current. Two enforcement layers ship in `make bench-gate`: leaf
> allocation ceilings from `scripts/perf_ceilings.txt`, and per-frame
> work-budget tests in `tui/viewmodel/perf_budget_test.go`. Timing is recorded
> but never machine-gated.

Recorded: 2026-08-30 (previous captures: 2026-07-17, 2026-07-31)

## Bounded question reading and review (2026-10-21)

`BenchmarkQuestionLongReflow` alternates 48/49-column layouts over a roughly
64 KiB question plus a similarly long choice. Initial Windows/amd64 Go 1.25.8
i7-9850H measurements found 74 ms per reflow: the shared `WordWrap` rescanned
the entire remaining suffix for every row. It now maintains the remaining width
incrementally, preserving existing line-break semantics. Three ten-iteration
samples measure 9.8–10.8 ms, about 16.7 MB and 12,185 allocations; the source is
reflowed only on content/geometry/calibration changes. These are diagnostic
component measurements, not a portable latency guarantee or an unbounded-size
promise.

`BenchmarkQuestionLongPaint` includes repeated Host layout and paint at 80×16
with the same long source. Caching the rail's short labels removes a per-frame
full-question width scan: 2.1–2.2 ms becomes 52–80 µs, 2,232 B and 23 allocations
in three ten-iteration samples. Complete source and tail remain reachable in the
bounded reader and editable final review. Existing allocation ceilings are not
raised.

Native sequential Windows 160×40, 80×24 and 60×18 captures exercise mixed choices,
wrapped questions, write-ins, explicit review/edit/Submit, workspace-panel
Hide, and preserved composer drafts. Another 80×24 run reads a 600-line question
and a long option through its final marker, reviews its complete answer and
submits through the connected core; a 60×18 run verifies the same long-content
review and submission with the rail hidden. Native pointer slash selection opens the
actual Sessions destination. This is selected-journey evidence, not every
possible terminal/agent scenario. The full native suite and bench gate still
report unrelated startup/session-platform failures and existing recovery-image
and database-startup allocation failures; no timeout or ceiling was relaxed.

## Command disclosure (2026-10-20)

`BenchmarkCommandOutputDisclosure` measures the first tool-body projection of a
1,000-line, 26 KiB command result, not provider execution or a cached full frame.
Three Linux/amd64, Go 1.25.9 i7-10750H samples at 120 columns
(`-benchtime=20x -count=3`) measured:

| Projection | Before | After |
|---|---|---|
| Compact | 1.09–1.21 ms; ~256 KB; 1,104–1,105 allocations | 87–101 µs; ~59 KB; 38 allocations |
| Expanded | 1.31 ms; ~571 KB; 5,091 allocations | 1.12–1.20 ms; ~563 KB; 5,090 allocations |

Compact rows now omit the output body rather than preparing an invisible
head/tail teaser. Expanded rows retain the complete supplied evidence. These
short component measurements are diagnostic, not portable latency guarantees;
no existing allocation ceiling or frame-work budget was raised.

## Native release schema startup (2026-10-19)

The full race suite exhausted the existing five-minute session-package budget
inside repeated schema initialization, not a deadlocked test. The initializer
autocommitted every table/index and reparsed the schema for 21 mostly failing
legacy column additions on every open. Current schemas now initialize in one
transaction; failed validation rolls back without changing existing rows.
Outdated schemas require manual alignment, not automatic upgrades.

`BenchmarkSessionDatabaseStartup` measures fresh on-disk session creation and
close with WAL and durability enabled. Three Linux/amd64 Go 1.25.9 i7-10750H
samples, running alongside validation, measured 44.7–61.9 ms and 289 allocations
before, versus 9.89–10.05 ms and 172–174 allocations after (ten iterations).
Five-iteration race samples measured 308–609 ms before versus 119–131 ms after.
These are startup costs, not frame latency or portable timing guarantees.
The Linux allocation gate is 9,000 B / 215 allocations; no existing ceiling or
test timeout was raised. `TestMigrateFailureDoesNotLeavePartialSchema` also
guards rollback and preservation of preexisting data.

## Git comparison reliability (2026-10-19)

The long-line reproduction uses the real Git dock and review with one 2,000,000-
character line, plus an empty untracked file. The old painter materialized two
complete grapheme arrays on every visible row before clipping, making the shell
unresponsive despite preparation running in a command. Three Linux/amd64,
Go 1.25.9, i7-10750H runs (`GOMAXPROCS=2`, `-benchtime=3x`, `-count=3`) of
`BenchmarkGitDiffLongLinePaint` at 120×12 measured:

| Source line | Before | After |
|---|---|---|
| 128 characters | 32.8–33.5 µs; 25,613 B; 30 allocations | 30.9–49.4 µs; 637 B; 17 allocations |
| 2,000,000 characters | 338–356 ms; 725,698,080 B; 70 allocations | 44.8–48.4 µs; 637 B; 17 allocations |

Other checks were running during the after measurement; this is component paint,
not isolated terminal latency. All source bytes remain available for copy and
reviewed mutation. A 100,000-line addition uses three presentation spans instead
of a second 100,002-row array during install and split. Preparation, I/O and
source search remain distinct background costs. Live typing found another UI
stall in synchronous Find; search now runs on immutable snapshots, cancels old
queries and visibly bounds match storage. Existing allocation gates are unchanged.

Sequential real shell captures at 160×40 and 80×24 cover large-line Find, split,
empty files, a clean repository and historical read-only previews. A separate
100,000-line replacement preserves complete +100,000/−100,000 stats and shows
the explicit first-10,000 search-match limit. A deliberately stalled Git smudge
filter leaves input responsive, settles the preview's timeout, and recovers via
Ctrl+R after the filter is removed. That journey also caught a missing exact
screen payload on the review's keyboard Refresh path.

### Sparse geometry completion on native Windows

The final sparse iterator uses a value cursor instead of escaping nested iterator
closures. Three Windows/amd64, Go 1.25.8, i7-9850H runs at 120×12
(`go test ./tui/screens/gitdiff -run '^$' -bench 'BenchmarkGitDiffLongLine' -benchmem -benchtime=100x -count=3`, default GOMAXPROCS=12) measured:

| Workload | WIP sparse iterator | Completed value cursor |
|---|---|---|
| 128-character line, left edge | 23.3–27.6 µs; 1,145 B; 27 allocations | 25.4–29.5 µs; 632 B; 17 allocations |
| 2,000,000-character line, left edge | 22.4–23.0 µs; 1,145 B; 27 allocations | 23.5–25.4 µs; 632 B; 17 allocations |
| 2,000,000-character line, far right | 20.9–21.1 µs; 1,145 B; 27 allocations | 21.5–22.3 µs; 632 B; 17 allocations |

The WIP sample used ten iterations; the completed sample used 100. These small
timing differences are not a speedup claim. The removed ten allocations bring
all three workloads below the unchanged 800 B / 22-allocation ceilings; far-right
paint now has its own gate. A later run during integrated checks measured up to
36.6 µs and 634 B, still 17 allocations. Component paint is not full-shell or
terminal latency, and source preparation/search remain separate background costs.

Native sequential 160×40 and 80×24 captures exercise far-right Find in real Git
reviews and local disk comparisons of the 2 MB line, including an empty untracked
file and local split/unified fallback. Explicit fixture-oracle tests cover sparse
grapheme/tab/control/CRLF boundaries, exact partial copy, recalibration, in-flight
preparation, replacement, close, workspace switch/failed-switch restoration and
shutdown. The original horizontal-content assertion remains and now goes through
the owned reflow command/result lifecycle rather than expecting a synchronous scan.

This is not an all-repository-gates-passing baseline. The native full bench gate
passes the comparison and frame workloads but fails the unchanged large-image
recovery publication allocation ceiling (8,693 allocations versus 190). A separate
three-iteration sample reproduces roughly 10,456–10,462 allocations; short samples
include additional database startup costs. No recovery-publication implementation
or ceiling was changed by the comparison completion. An allocation profile locates
the dominant object count in modernc's Windows ReadFile/WriteFile adapters during
SQLite page I/O, not in comparison traversal. The native normal suite also
reproduces the directory-watch rename failure in
`TestDocumentWatchRemovedDirectoryReleasesAndReopens`; core/session journey timeouts
in the concurrent full run passed when rerun in isolation. These failures remain
open, not hidden by skipped tests or relaxed limits.

The full native race run (`CC=gcc`, `go test -race -p=2 ./... -count=1 -timeout=600s`)
completed without a data-race warning, but failed the existing 1100×1100 clipboard
image response wait, one temporary-directory cleanup in
`TestEditorAgentSwitchWorkBounded`, and the same directory-watch rename test. The
control visual harness did not fail. After the final cancellation hardening,
complete normal core/viewmodel packages pass, all five comparison screen packages
pass normally and under race, and three repeated focused App/control race runs
pass. Build, whole-repo lint, design/docs and non-cgo Linux/macOS cross-builds pass.
Native Windows capture covers the selected comparison journeys, not every lifecycle
or another platform's terminal. Zig's Windows race executable failed before tests
with a ThreadSanitizer allocation error; the successful native race checks use GCC.

## Connected clipboard image preparation (2026-10-18)

The real connected clipboard journey with an uncompressed 1100×1100 PNG failed
its unchanged eight-second post-admission response wait under `-race`. A CPU profile located
repeated large context/recovery writes and repeated multipart JSON decoding before
the provider request, not a detected memory race or corrupted clipboard bytes.
Starting a committed input no longer republishes its initial recovery envelope;
receipt-only publications update the revision without rewriting unchanged context
payloads; token estimation decodes multipart once instead of three times.
Three repeated race runs pass all twelve combinations of Ctrl+V/bracketed paste
and 91-byte/4,843,302-byte images, checking exact bytes and one provider request.
For the large image, capture takes 3.16–3.28 s and admission 3.33–3.48 s; Enter to
visible response takes 9.70–10.16 s, including admission. The remaining response
wait is 6.31–6.69 s, within the unchanged eight-second bound. Race instrumentation
is deliberately included here; these are not normal-build latency claims.

Three Linux/amd64, Go 1.25.9, i7-10750H runs (`GOMAXPROCS=2`, `-benchtime=3x`,
`-count=3`) measure the leaf costs separately from the connected user journey:

| Benchmark | Before | After |
|---|---|---|
| `BenchmarkEstimateMultipartImage/bytes_4194304` | 63.1–64.9 ms; 25,217,048–25,217,080 B; 50 allocations | 21.6–23.0 ms; 8,405,688–8,405,720 B; 17 allocations |
| `BenchmarkPublishContextRecoveryImage/bytes_4194304` | 170.0–175.2 ms; 43.4–46.2 MB; 156–162 allocations | 162.4–169.0 ms; 33.6–37.8 MB; 143–153 allocations |

SQLite still retains the complete archive and recovery context; this is not a
claim that large-media persistence is constant-cost. Trigger-based tests prohibit
rewriting unchanged source payloads while checking exact recovery bytes, rollback
and stale-revision rejection. No timeout or existing allocation ceiling was raised.

## Document-owned syntax acceptance (2026-10-18)

The former 256-line viewport restart inverted Python docstring/code and long
block-comment boundaries. Lexing now runs once per document revision off the UI
thread; visible cell maps project compact source spans without re-lexing on scroll.
Three Linux/amd64, Go 1.25.9, i7-10750H runs (`GOMAXPROCS=2`, ten iterations,
`go test ./tui/editor -run '^$' -bench '^BenchmarkSyntax(LongPythonViewport|DocumentPreparation)' -benchmem -benchtime=10x -count=3`)
measured 376–404 ns / 0 B / 0 allocations for a warm 40-line viewport inside a
20,000-line Python string. The old, incorrect warm path measured 1.60–2.17 µs /
640 B / 40 allocations (1,000 iterations, GOMAXPROCS=12); timings are not an
isolated same-configuration speedup claim.

Complete source preparation is separate: 200 lines measured 0.72–0.75 ms /
369,072–369,073 B / 5,878 allocations; 20,000 lines measured 60.1–62.1 ms /
35,525,028–35,525,046 B / 560,290 allocations. These are cumulative allocation,
not retained heap or input-to-terminal latency. Chroma's regex machinery remains
allocation-heavy. Removing its repeated string coalescing reduced the 20,000-line
preparation from 209.9 MB to 35.5 MB; two concurrent workers bound active parses,
and superseded revisions cancel before replacement admission. New allocation
ceilings retain roughly 20% headroom; no existing ceiling is raised.

Real shell captures at 160×40 and 80×24 cover direct jumps deep into the string,
the far-away closing delimiter and subsequent executable code, plus dark/light
themes. Regression journeys cover offscreen delimiter deletion/undo, tab changes,
CRLF/Unicode, pending navigation and stale close/replacement results. Existing
paint goldens remain unchanged; cold source work is not hidden in warm-paint
benchmarks.

### Typing presentation regression (2026-10-19, native Windows)

The first async-syntax implementation cleared every visible colour map on each
source edit. A deliberately blocked replacement lexer reproduces 678 unchanged
visible cells changing to plain ink after a single identifier character. Waiting
for completion before painting hid this regression in the original acceptance.
The same pending-frame assertion now observes zero unrelated colour changes.

Visible decorations now map through the existing 32-revision line journal. Inline
edits retain current grapheme/tab geometry; line insertion/deletion shifts surviving
rows. New parse results atomically replace this provisional ink, so an offscreen
delimiter edit still updates the complete downstream context. Missing history,
whole-source replacement, theme and width changes do not reuse unrelated maps.
The journal retains at most 4 KiB per inline edit, never large paste payloads.
The native capture additionally caught diagnostics dropping underlines on
unchanged lines after every edit. Diagnostic paint now retains only mapped
unchanged rows; touched rows discard old ranges, and hover/navigation still
refuse prior-revision diagnostics. The connected presentation test includes
diagnostic ink and checks complete cell attributes, not just syntax colours.

`TestConnectedEditorTypingKeepsUnchangedPresentation` exercises real framed editor
edits and paints before and after their commands at 160×40 and 80×24. It checks
unchanged source/sidebar cells and replays the exact views through Bubble Tea's
real terminal renderer. One sample emits 40–83 bytes for character frames and 19
bytes for acknowledgement-only frames (including a test title marker), without
clear-screen or alternate-screen resets. Key-to-View measured roughly 0.6–1.8 ms
for this 2,000-line fixture; that is not physical terminal latency. Sequential
native captures cover paced individual characters at both sizes with real gopls
diagnostics; static screenshots alone do not establish transient-frame behavior.
Final before/after captures have zero changed pixels in the untouched source-body
regions at both sizes. The narrow stable-geometry check types inside a comment:
typing past the right edge legitimately scrolls the viewport, and changing Go
syntax can legitimately replace the server's diagnostic set.

Three Windows/amd64, Go 1.25.8, i7-9850H runs (`GOMAXPROCS=2`) measured
`BenchmarkSyntaxMapInlineDecorations` at 3.14–3.20 µs, 688 B and two allocations.
Its new allocation gate is 830 B / three allocations; no existing ceiling changes.
`BenchmarkSyntaxTypingPendingPaint` separately includes source mutation, buffer
cache rebuild and viewport paint with the parser blocked: about 0.32–0.36 ms /
204–206 KB for 200 lines and 1.08–1.23 ms / 1.89 MB for 20,000 lines. Both are about
943–956 allocations, not a constant-cost editing claim; piece-table rebuilding and
history still contribute. These are repeated steady samples, not a direct timing
comparison with the shorter ten-iteration reproduction.

Mapping preserves coloured cells that were previously blanked; converting their
existing hex colours directly through the shared rendering conversion avoids a
per-cell boxed colour allocation. Warm editor paint remains below all unchanged
ceilings (about 754 B / 84 allocations at 80×24 and 1,378 B / 146 at 120×40).
Final affected editor/shell/viewmodel packages, repeated focused race checks,
build, lint, design/docs and affected Linux/macOS cross-builds pass. The full
normal run before the diagnostic-paint refinement fails only the already-recorded
directory-watch rename test; the full bench gate fails only the unchanged large
recovery-image allocation ceiling (8,693 versus 190 allocations). Those repository
failures remain open; they are not evidence of an all-green full-suite result.

## Editor pointer acceptance (2026-10-17)

Stationary selection scrolling uses one owner/tick-fenced 50 ms repeat, not a
motion-event-sized scroll. `BenchmarkPointerScrollMutation` isolates warm
gesture/viewport mutation from paint. Three 1,000-iteration Linux/amd64 runs
(`GOMAXPROCS=2`, Go 1.25.9, i7-10750H) measured 2.90–5.70 µs / 265 B / five
allocations for 200 lines and 2.86–3.08 µs / 326 B / six allocations for 100,000
lines. These include scheduling the next command, not waiting for its timer or
terminal transport. They are local acceptance measurements, not timing ceilings.

The unchanged `make bench-gate` passes: editor paint/scroll repaint remains
3,936 B / 546 allocations at 80×24 and 7,360 B / 993 allocations at 120×40;
large-file 120×40 paint is 7,488 B / 993 allocations. Real Linux captures at
160×40 and 80×24 exercise held edge scrolling, release stop, gutter/thumb drags,
horizontal scrolling and selection replacement with Undo/Redo.

## Editor transaction acceptance (2026-10-17)

The source-boundary/history correction keeps existing allocation ceilings.
`make bench-gate` on Linux/amd64, Go 1.25.9, i7-10750H reports 3,936 B / 546
allocations for 80×24 editor paint and 7,360 B / 993 allocations for 120×40
paint and scroll repaint. Large-file 120×40 paint reports 7,488 B / 993
allocations. These measure component paint, not terminal transport latency.

Three ten-iteration runs of
`GOMAXPROCS=2 go test ./workspace -run '^$' -bench '^BenchmarkDocumentDurableEditAfterHundredOps$' -benchtime=10x -count=3`
include SQLite publication after 100 accepted edits. The 4 MiB case measures
2.99–4.85 ms / 4.236 MB / 135–137 allocations per edit, within its unchanged
16 ms gate; SQLite page growth is 1,638 B/op. Other repository checks were
running concurrently, so these are acceptance measurements, not an isolated
before/after speedup. Undo suffix normalization is bounded by retained history
but can be quadratic; these ordinary-edit numbers do not measure its worst case.

## Semantic control acceptance (2026-10-12)

The initial control integration failed existing allocation gates: idle tick
44,416 B, warm switch 46,042 B, and two-agent presentation 123,371 B per operation.
Profiles located unnecessary semantic mount construction on ordinary Host layout
and a per-lookup glyph table escape. Semantic inventory now starts only on
explicit observation; observed lifetimes still reconcile unmounts. Glyph lookup
uses allocation-free value defaults and retains theme overrides. No frame or
model mutations were suppressed and no ceiling was raised.

The complete `make bench-gate` rerun on Linux/i7-10750H with `GOMAXPROCS=2`
measured 12,612 B / 204 allocations for idle tick, 14,238 B / 202 for warm agent
switch, 39,770 B / 560 for editor-plus-agent presentation, and 46,464 B / 489 for
two-agent presentation. All current gates passed. These are local benchmark
measurements, not network/terminal latency. Explicit inspection remains source-sized
hashing work and returns at most 128 records within a 64-KiB serialized page;
it never exports every virtual row to find a mounted control after the first 4,096.

## Core/shell resource checkpoint (2026-10-09)

Parent-supplied real-process acceptance sampled twenty retained workspaces and
ten independently rendered CLI tmux shells. The initial core sample recorded
138,440,704 B RSS, 222 file descriptors and roughly 10% CPU; ten shell processes
aggregated 740,962,304 B RSS, with roughly 10–13% CPU each during a two-second
sample. These are initial workload/process observations, not settled idle rates,
a prior-architecture comparison, final optimized numbers or proof of a smaller
footprint. Retained ownership alone is not a performance result.

Final isolated binary rerun measured 139,075,584 B core RSS, 222 FDs, 9% of one
CPU over two seconds; ten shells aggregated 730,644,480 B RSS and about 9–11.5%
CPU each. This remains an observed short post-startup sample, not settled idle
or a comparison against the pre-extraction architecture.

`workspace.BenchmarkDocumentDurableEditAfterHundredOps` measures SQLite-backed
accepted edits after 100 commands at 4 KiB, 1 MiB and 4 MiB, including allocations
and `sqlite-growth-B/op`. The source contains a 16 ms ordinary-edit budget for the
4 MiB case with at least ten iterations. Parent verified three 10-iteration
measurements of the final checkpoint-v2/delta implementation: 4 MiB ordinary
durable edits took 2.20–2.45 ms and about 4.24 MB allocation versus 46–57 ms /
about 27 MB before. Accepted operation deltas, affected-author history metadata
and exact durable SQL receipts commit in one revision-CAS transaction. The SQL
receipt ledger remains durable/growing; only its per-document in-memory cache
is bounded at 256 entries. Rolling history retains at least 512 at its 1,024
operation boundary. These are service/SQLite edit timings, not full-shell
presentation latency or a settled process-resource comparison. Do not substitute an ephemeral text helper benchmark or
omit durable receipt/history growth. Final results must state binary/source,
hardware, workload, command, repetitions and memory/storage/latency separately.
Independent parent rerun on Linux/i7-10750H (`go test ./workspace -run '^$'
-bench '^BenchmarkDocumentDurableEditAfterHundredOps$' -benchtime=10x -count=3`)
measured 0.807–0.832 ms / 48.6–49.3 KiB for 4 KiB, 0.921–0.981 ms /
1.094 MB for 1 MiB, and 2.136–2.238 ms / 4.240 MB for 4 MiB. SQLite page growth
was 1,638 B/op in each case, excluding total WAL bytes. No existing allocation
ceiling was relaxed. Structural checkpoints/cold replay remain separate costs.
See [review workflows](../guides/core-shell-workflows.md) and the
[active acceptance plan](../plans/core-shell-separation.md).

## Main View Panes (2026-10-08)

### Aligned Git/local comparison reader

Three Linux/i7-10750H runs (`GOMAXPROCS=2`, 200 ms, 10,000 source lines,
160×40 viewport) measured 266–279 µs / 149,701 B / 311 allocations for syntax
diff paint and 291–299 µs / 143,624–143,625 B / 539 allocations for aligned
plain-source paint. Preparation is separate, off-UI: 16.4–17.2 ms and about
16.9 MB for the plain 10,000-line fixture. Whole-document fallback lexing and
temporary per-grapheme width slices initially cost 131–135 ms / 265 MB; removing
that unnecessary plain-text work restored bounded preparation without changing
rendering. Paint allocation gates add modest headroom; no prior gate is loosened.
These are reader/component timings, not filesystem, full-shell or terminal
transport latency. Syntax preparation and query changes remain source-sized work.

### Main-group presentation

At 200×50, with 300 mixed-role messages per retained agent, three isolated
Linux/i7-10750H runs (`GOMAXPROCS=2`, 200 ms benchmark duration) measured:

| `BenchmarkMainPane` stage | Time/op | Bytes/op | Allocs/op |
|---|---:|---:|---:|
| Presentation: editor + dense agent | 0.804–0.844 ms | 39,536–39,538 | 554 |
| Presentation: two dense agents | 0.909–0.932 ms | 46,210–46,213 | 486 |
| Divider mutation, no presented frame | 15.8–16.3 µs | 9,751 | 44 |

Presentation includes the real `App.View` compositor/ANSI path, not terminal
transport or emulator latency. Divider mutations and drag previews keep dense
message generations warm; release commits final width using existing lazy
viewport/overscan reflow. Warm agent switching still fits its unchanged
15,000 B / 315 allocation gate: 14,075–14,128 B, 197–198 allocations,
153.8–157.0 µs. Main-pane ceilings add bounded headroom; no existing gate is raised.

## Recovery Disclosure Clock (2026-10-05)

The compact recovery row caches expanded diagnostic evidence separately from
its clock-driven header. `BenchmarkExpandedRecoveryClockShort` and
`BenchmarkExpandedRecoveryClockDense` exercise real renderer export with one
versus 2,000 diagnostic paragraphs, keeping a live countdown and Retry now
target. Three Windows/i7-9850H runs measured 3.75–3.90 µs per clock update:
2,467 B / 23 allocations for the short case and 2,066–2,068 B / 24 allocations
for the dense case. Work does not grow with the diagnostic's retained rows.
These are projection/export measurements, not full-shell paint timings.
The existing repository allocation ceilings remain unchanged.

## Workspace Home (2026-10-02)

`BenchmarkLauncherDenseFrame` paints and relays out a 160×40 home screen with
1,000 recent workspaces. With the original outlined controls, right-pane resource
spacing and aligned edges, three Linux/i7-10750H runs measured 146.8–181.3 µs,
14,930–14,931 B and 96 allocations per frame. The benchmark also asserts that
animation/paint frames never rebuild the resource index. Pending listing loads
use the fast shared clock for the ProgressRule; settled home returns to the
ordinary 400 ms idle tick. These are screen timings, not complete shell timings.

## Capability Integration And Plain-Text Recovery (2026-10-01)

The complete bench gate passes with the capability/runtime/TUI integration.
`BenchmarkDenseChatSingleRowChange_500Msgs` was reproduced at 3,218 allocations,
64 KB and 0.543–0.545 ms. The profile located the work in Markdown rendering,
not the new catalogs or shell painting. A narrow plain-text fast path recognizes
only space-separated ASCII letters without leading/trailing whitespace; markup,
punctuation, newlines, URLs and indented code keep the full Markdown path.
Three isolated Linux/i7-10750H runs now measure 196 allocations, 24,537–24,538 B
and 0.377–0.381 ms per changed-row presentation. Rich hierarchy/color tests stay
unchanged, and the existing 65,000 B / 2,740 allocation ceiling is not loosened.
Earlier Markdown overages recorded below are historical and now resolved.

## Oversized Context Extraction (2026-09-29)

`BenchmarkSplitExtractionOversizedToolResult` splits a 2 MB text tool result into
complete attributed fragments with a 60K-token request budget. Three runs on
Linux/i7-10750H measured 7.26-7.85 ms, 6.37 MB and 134 allocations per complete
split. Work runs on the compaction owner, never the UI thread, and is proportional
to source size rather than allocating a maximum-context buffer per item.
The benchmark excludes provider latency and does not assert summary quality.
See [adaptive context handling](adaptive-context.md) for capacity, transactional
recovery and source-retention contracts.

## Editor Savepoints And Gutter (2026-09-26)

Measured on Linux/i7-10750H, two isolated runs. Immutable piece-span savepoints
make a change/revert identity check on a 100,000-line buffer cost 79.6-81.8 ns,
0 B and 0 allocations (`BenchmarkBufferSavepointManualRoundTripLargeFile`).
This checks bytes, not a probabilistic hash, and is distinct from the cached
`Buffer.IsDirty` query.

`BenchmarkImmediateGitGutterLargeFile` measures a real edit plus undo on a
20,000-line file: 1.66-1.67 ms / 3.59 MB / 91 allocations with a tracked Git
baseline, versus 1.72-1.97 ms / 3.58 MB / 48 allocations without annotations.
The untracked-file control measures 2.71-2.79 ms / 5.73 MB / 56 allocations.
Most allocation remains in existing PieceTable mutation/cache construction;
ordinary tracked changes re-diff only the affected line window. Full replacement
and changes touching a large existing hunk may require a larger exact diff.

Known independent gate failure: `BenchmarkDenseChatSingleRowChange_500Msgs`
reports 3,217 allocations against its 2,740 ceiling. Three isolated runs of
both untouched commit `a348e22` and this worktree reproduce exactly 3,217
allocations with `CGO_ENABLED=0`. Its Markdown rendering profile is unrelated to
the editor/settings/history changes. The ceiling remains unchanged.

The 2026-09-28 Markdown restoration preserves that allocation profile: three
isolated `BenchmarkDenseChatSingleRowChange_500Msgs` runs measured 3,216
allocations and 0.586-0.598 ms per update. Native bridging now shares the colour
map across each rendered block and retains styles across line breaks. The
existing 2,740-allocation gate overage remains; no ceiling was loosened.

## Warm Agent Switching (2026-10-07)

`BenchmarkAgentSwitchWarm_80Msgs` retains renderer-source generation checks while
reusing the active screen's item, row-start and prefix storage. Indexed payloads
avoid boxing complete rows and always resolve against the current snapshot; no
second per-source cache or frame suppression was introduced.

| Measurement | Before | After |
|-------------|--------|-------|
| Bytes/op | 31,661-31,663 | 14,491-14,504 |
| Allocs/op | 270 | 193-194 |
| Time/op | 172.4-178.1 us | 158.7-181.6 us |

Linux/i7-10750H measurements use `GOMAXPROCS=2`, three normal benchmark runs and a
bounded validation run. Timing ranges overlap; the verified improvement is about
54% fewer allocated bytes and 28-29% fewer allocations, not a guaranteed latency
speedup. Profiles locate the removed allocations in list installation. A warmed
list-installation regression fails with 84 allocations before and passes with zero
after. Source collisions, changed-row paint, snapshot lifetime, selection, scroll
and disclosure contracts remain guarded. The existing 15,000 B / 315 allocations
gate is unchanged. This benchmark paints the shell, not the ANSI/terminal path.

## Context Publication (2026-10-07)

The revision-fenced context publisher is covered by the canonical benchmark gate.
Three Linux runs with `GOMAXPROCS=2` measured:

| Benchmark | Time/op | Bytes/op | Allocs/op | Allocation ceiling |
|-----------|---------|----------|-----------|--------------------|
| `BenchmarkPublishContextReplaceUnrelatedArchiveHistory/unrelated_0` | 0.82-0.84 ms | 8,095-8,100 | 156 | 10,000 B / 190 allocations |
| `BenchmarkPublishContextReplaceUnrelatedArchiveHistory/unrelated_2000` | 0.71-0.74 ms | 8,087 | 157 | 10,000 B / 190 allocations |

This measures a real SQLite replacement transaction including the immutable
identity lookup, not a proxy counter. Unrelated archive size adds no payload
scan or allocation growth. The removed historical scan comparison cost about
6.5-6.7 ms, 2.32 MB and 34,010 allocations for 2,000 unrelated rows; no legacy
scanner remains in the product or benchmark suite. Timing is reported, not
hardware-gated. Allocation limits are machine-readable in
`scripts/perf_ceilings.txt`; the gate parses named sub-benchmarks as well as leaf
names. Owner-source size and dense UI presentation have separate existing gates.

## Purpose

A CPU-hammering regression in a tick-driven TUI is almost never one
function getting fatter — it is a **call-frequency regression**: cached state
re-dirtied every tick until steady state becomes a full-session re-render.
Per-call benchmarks are nearly blind to it (every individual call still looks
cheap), so the performance system is deliberately layered:

1. **Leaf gate** — per-call paint/alloc benchmarks with allocation ceilings,
   catching a function getting fatter.
2. **Scenario gate** — work-budget tests over whole frames driven through
   `app.handleTick` + `paintShellFrame`, with render work counted at pipeline
   chokepoints, catching anything that re-dirties cached state per tick.

No benchmark threshold is allowed to justify violating rendering correctness,
terminal-width invariants, component ownership, or accessibility semantics.

## Resume And Lifecycle Freezes (2026-09-10)

Activity tree follow-up (2026-09-25, Linux/i7-10750H):
`BenchmarkDenseActivityPresentedFrame`, 4,000 settled file reads plus one active
command at 100 columns, measures each blinking-dot presentation separately from
source mutation. Caching summary/visible membership at source-change boundaries
reduced 199-201 us / 36 KB per frame to 6.6 us / 3.3 KB for the original one-leaf
window. Retaining two request rounds measures 10.7 us / 5.1 KB, including the
additional completed leaf. Stable action/operand/metadata color spans and
span-preserving wrapping measure 12.2-12.9 us / 6.1 KB for the same retained
window. The six-tool / whole-request-round retention floor measures 30.7-31.0 us
/ 14.2 KB per frame (three isolated runs), now painting six leaves instead of
two. Completed source messages are not rescanned on ticks; model updates are not
deferred or discarded. Live local-provider acceptance exercised nine promoted
thought/read requests, stop, expanded tree disclosure, and passive resume at
160x40 and 80x24. Long thoughts rejoin the tree on completion; pane-to-tree
boundaries retain exactly one transcript-owned spacer.

`BenchmarkTextTidePaint` measures the shared paint-only summary/activity ink
sweep at 15.8-16.3 us and 192 B / 2 allocations for a 120-column line (three
isolated runs). The clock does not invalidate summary Markdown or transcript
row snapshots. Live captures covered semantic chunk replacement, expanded
history, stop, passive resume, model-accounting disclosure, and dark/light
desktop/narrow terminal layouts.

`BenchmarkLargeOpenDiff` covers a fully expanded 3,000-line Go diff, without
omitting any source rows. A bounded render-local syntax cache avoids tokenizing
identical segments repeatedly: repeated source fell from 130-143 ms / 39 MB to
13.6-13.8 ms / 8.9 MB. The all-unique control remains 130-132 ms / 39 MB. The
existing race-instrumented `TestProviderWaitWithLargeDiffRemainsResponsive` now
passes its unchanged 500 ms first-frame ceiling; before this correction it took
6.7 seconds even in isolation. The cache is per file/render and capped at 256
entries, with independent gutter/background spans for each painted row.

Measured on Linux/i7-10750H. The archive reproduction reads a session DB in
read-only mode without starting a backend or transmitting its contents.

| Stage / workload | Before | After |
|---|---:|---:|
| Archive first paint, 694 display rows / 15,108 parsed diff rows | 23.9 s | 625 ms |
| Archive hydration, same transcript | 145 ms | 1.4 ms |
| `BenchmarkLargeDiffPreview`, 3,000 source rows | 8.36 s / 39.7 MB | 8.0 ms / 2.67 MB |
| `BenchmarkOpenIndexedHistory`, 500 snapshots x 1,400 entries | 5.19 s / 939 MB | 12.1 ms / 368 KB |
| Shutdown during memory maintenance delay | 3.05 s | Immediate cancellation and join |

Diff lexer selection is per file, not per wrapped line. Collapsed previews count
the full visual extent for their footer but tokenize/paint only the preview
budget. Local-history startup recovers unindexed manifests/plans only; indexed
snapshots and their entries were committed atomically. Loading a manifest for
restore or verification still checks its hash. Hydration normalizes stopped
streams and publishes one transcript instead of every growing prefix.

Resume visibility is applied before row geometry: context-only provider envelopes
without display snapshots produce no display rows. Otherwise each recalled-memory
envelope becomes two blank fallback rows, sorted after visible messages by the
later persistence timestamp. `TestAgentView_ResumeContextOnlyMessagesDoNotCreateBlankTail`
covers SQLite round-trip, one and six turns, immediate/settled frames, and narrow
resize. Explicit restore/compaction snapshots remain visible. Lazy width reflow
still touches viewport plus overscan only; its second materialization pass must
invalidate the height index when it changes newly exposed rows.

Memory shutdown no longer starts a new provider request or waits a fixed five
seconds. It cancels optional extraction/maintenance, joins workers before the
workspace DB closes, and preserves durable turn mirrors and extraction watermarks
for recovery, including when reopening the same session.

`TestProviderWaitWithLargeDiffRemainsResponsive` guards the large-diff wait-frame
regression. For local diagnosis, set `CRUDE_TEST_ARCHIVE` to a session DB and run
`go test ./tui/viewmodel -run '^TestResumeLatencyArchive$' -v`.
`TestLiveOllamaWaitLatency` is separately opt-in with `CRUDE_TEST_LIVE_OLLAMA=1`:
it privately reads configured Ollama Cloud credentials, uses `glm-5.3`, synthetic
context, and a temporary session/project. It never sends the archived transcript.
Three measured 425 KB live requests completed in 5.20-6.65 s with 332-443 UI
frames each, a 4.2 ms maximum update, and a 365 ms maximum paint for the large
synthetic input. The intermittent multi-second live-provider pause was not
reproduced in those runs. Isolated archive copies were also checked through
`scripts/tuishot.sh` at 160x40 and 80x24, including scroll and application exit.

### Status overhaul hot paths (2026-09-10)

The active header reads generation-cached request usage, and project status reads
diagnostic counters maintained at diagnostic mutation boundaries. On the same
i7-10750H reference machine:

| Benchmark | Workload | Result |
|---|---|---:|
| `BenchmarkProjectInsightsCached10kDiagnostics` | Project snapshot after 10,000 reported diagnostics | 67 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkUsageSummaryCached1000Requests` | Per-agent summary after 1,000 request records | 108 ns/op, 0 B/op, 0 allocs/op |

Painting, ticks, focus changes, and merely opening another tab must not scan the
diagnostic cache or request ledger. Mutations invalidate the exact aggregate.

## Local History incremental capture

Recorded 2026-09-01 on i7-10750H, Linux, 1,000 small source files with one file
changed per snapshot:

```text
BenchmarkSnapshotAfterOneSmallEdit-12  58.3 ms/op  9.28 MB/op  73,587 allocs/op
```

The initial implementation allocated one 8 MiB chunk buffer plus the chunker
reader buffer per file (about 9 GiB/op). Small files now bypass CDC as one
content-addressed chunk and large-file CDC buffers are pooled. Wall time remains
review-only; the benchmark is retained in `localhistory/benchmark_test.go`.

## Layer 1: Leaf allocation gate

`scripts/tui_bench_gate.sh` (wired into `make bench-gate`) runs the canonical
benchmarks and fails when a result exceeds its ceiling in
**`scripts/perf_ceilings.txt`** — the machine-readable, single source of truth
for allocation ceilings:

```
# One benchmark per line: "<Name> <bytes/op> <allocs/op>"
BenchmarkEditorPaint_120x40  20000  2200
```

Rules for editing that file are documented in its header: tighten on a fix
with ~20% headroom; loosen only with a written explanation of the new cost;
verify on a quiet machine with `-count=3` (a flaky ceiling rots the gate's
credibility).

Wall-clock timing is intentionally NOT gated: CPU frequency variation across
runners makes time thresholds noise. Timing regressions are handled as a
review gate instead (see "Timing review guidance").

## Layer 2: Work budgets (`tui/perf` + `perf_budget_test.go`)

`tui/perf` carries atomic per-frame counters at the render chokepoints:
`ItemsRendered`, `LinesRendered`, `StatusRenders`, `PaneRenders`,
`CacheInvalidations`, `ItemInvalidations`, `ANSIFrames`, `ANSICells`.

`tui/viewmodel/perf_budget_test.go` pins what a frame is allowed to do:

| Test | Pins |
|------|------|
| `TestIdleTicksStayWithinWorkBudget` | 25 warm idle ticks re-render ~nothing (≤4 items / ≤64 lines outside status-row churn). |
| `TestRenderCachePersistsAfterIdleWindow` | After warm-up, every non-exempt message holds `CachedLines` (catches caches written to transient copies). |
| `TestHydrationRendersOnceThenStaysWarm` | A hydrated dead session renders each row once; mid-flight rows persisted streaming/active must be transitioned on hydration. |
| `TestTabRederivationRendersNothing` | Re-deriving the SAME agent tab per tick renders exactly zero. |
| `TestEditorAgentSwitchWorkBounded` | editor↔agent round trips render at most one session pass; steady re-derivation after the switch is zero. |
| `TestIdleWorkScalesSublinearlyWithSessionSize` | Idle work at 500 msgs ≤ 3× work at 50 msgs + constants (virtualization guard). |
| `TestPointerBurstCoalescesPresentationWithoutBufferingGeometry` | 1,000 pointer mutations apply the latest geometry while full presentation stays frame-bounded. |
| `TestAgentToAgentSwitchKeepsBothProjectionsWarm` | Focus-only agent switches render zero message rows. |
| `TestIdleCadenceNeverDropsBelowTwoFPS` | Idle cadence remains 250–500 ms (2–4 FPS), preserving cursor animation without high CPU. |
| `TestRenderDeclaredFrameDependenciesAnimateWithoutStaticInvalidation` | Every rendered frame-dependent row advances; static rows remain cached without role-specific tick logic. |
| `TestTransientLifecycleMetricsDoNotInvalidateTranscript` | Context-upload/progress metric changes do not invalidate transcript rows. |

Two by-design carve-outs keep the budgets honest:

- **Transient turn-status rows re-render every tick on purpose**. Retained
  recovery evidence is cached; only its compact clock header changes.
  Status work is counted separately via `StatusRenders` and never counted
  in the item/line budgets. The seed keeps status-row count FIXED across
  session sizes so the scaling guard measures only real work. Note that
  `workspace.Conversation.insertMessage` keeps the last turn-status row pinned
  at the visual tail; the budget tests call `clearTurnTail` (a thin
  `ClearTurnStatus` wrapper) so steady state matches a completed turn.
- **Streaming rows are exempt while live** (`nilCacheExempt`): the streaming
  cursor, active thinking, and streaming tool output re-render per tick by
  necessity. The hydration test's whole point is that they must NOT stay live
  after hydration.

### Mutation verification

The four historic regressions that motivated this system were re-introduced by
hand and confirmed caught (2026-08-30):

| Regression | Test that fails | Observed churn |
|------------|-----------------|----------------|
| Render cache written to a discarded copy | `TestRenderCachePersistsAfterIdleWindow` | 176/180 messages lost their cache |
| Per-tick tab-switch invalidation (no same-tab guard) | idle, hydration, tab, scaling tests | 4,400 items / 29,625 lines vs ≤100 budget |
| Unconditional trailing-assistant cache drop per tick | idle + hydration | 25 items/550 lines idle; 1,615 lines hydrated |
| Dead-stream cleanup skipped on hydration | `TestHydrationRendersOnceThenStaysWarm` | 50 items / 1,850 lines per idle window |

If you add a new budget test, verify it against a hand-applied regression
another time this happens — a budget tool that has never caught its bug is
cargo cult.

## Layer 3: Runtime sentinel

Live runs can be watched for the same signature by setting
`CRUDE_PERF_LOG=<path>` before launching crude. Every 32 ticks
(`SentinelTick` from `handleTick`) it samples the `tui/perf` counters; if a
window exceeds the work budget while the app is idle, it appends one throttled
line (`crude-perf: idle window exceeds work budget ... top=<site>`) to the
file. The top site comes from the opt-in probe attribution, so a diagnosis
session usually starts, not ends, with that file:

```bash
CRUDE_PERF_LOG=/tmp/crude-perf.log ./crude --resume <session-id>
```

Input and streaming mark the app "active" and suppress warnings — repaints
during interaction are the product working, not a degenerate loop. `main.go`
discards slog to protect the TUI surface, which is why the sentinel writes
directly to the file. For interactive CPU profiling use
`CRUDE_PPROF=":6060"` (pprof endpoint in `main.go`), then
`go tool pprof` against `curl localhost:6060/debug/pprof/profile?seconds=10`
captures.

## Reference Environment

```text
OS: linux/amd64
Kernel: Linux 6.17.0-29-generic
Go: go1.25.9
CPU: Intel Core i7-10750H @ 2.60GHz
Cores/threads: 6/12
GOMAXPROCS: 12
```

CPU frequency scaling was enabled during capture; ranges below include normal
scheduler and frequency variation.

### Windows allocation calibration (2026-10-04)

The allocation gate keeps the Linux reference rows as its generic CI baseline
and applies a narrow `windows` override only where the same Go benchmark has a
different runtime allocation shape. On `windows/amd64`, Go 1.25.8, three
repeated clean-HEAD runs measured:

| Benchmark | Observed time range (ns) | Bytes/op | Allocs/op |
|-----------|--------------------------|----------|-----------|
| `BenchmarkAgentSwitchWarm_80Msgs` | 153,795-157,145 | 34,170-34,174 | 303 |
| `BenchmarkOpenIndexedHistory` | 17,106,233-17,214,597 | 493,349-497,866 | 11,628-11,629 |

The unchanged `HEAD` archive and the edited worktree produced the same agent
switch allocation result. The Windows profile includes UTF-16 environment
lookups and Windows-specific SQLite/runtime paths, so allocations are
hardware-independent but not OS/runtime-independent. The Windows ceilings in
`scripts/perf_ceilings.txt` retain approximately 20% headroom; the generic Linux
ceilings are unchanged.

## Reproduction

```bash
# Leaf benches
GOMAXPROCS=12 go test -run '^$' \
  -bench 'Benchmark(DemoGallery|Blit|WriteString|Flatten|FullRender|GenerateANSI)' \
  -benchmem -count=5 \
  ./tui/ui ./tui/surface ./tui/compositor

# Product-screen paint benches
GOMAXPROCS=12 go test -run '^$' \
  -bench 'Benchmark(EditorPaint|EditorScrollRepaint|ChatStreamPaint)' \
  -benchmem -count=5 \
  ./tui/screens/shelleditor/ ./tui/screens/shellchat/

GOMAXPROCS=12 go test -run '^$' -bench '^BenchmarkBufferDirtyCheckLargeFile$' \
  -benchmem -count=5 ./tui/editor/

# Scenario benches (steady-state work budgets)
GOMAXPROCS=12 go test -run '^$' \
  -bench 'Benchmark(IdleTick|TabRederive)' -benchmem -count=5 ./tui/viewmodel

# The enforced gates:
make bench-gate                     # leaf + scenario allocation ceilings
go test ./tui/viewmodel -count=1    # work-budget tests
```

Record environment information with `go version`, `go env GOOS GOARCH
GOVERSION`, `uname -srmo`, and `lscpu`. For before/after comparisons capture
both revisions with the same command and compare with `benchstat`; never
compare across power modes without noting it.

## Recorded Results (2026-08-30 capture)

| Benchmark | Observed time range (ns) | Bytes/op | Allocs/op |
|-----------|--------------------------|----------|-----------|
| `DemoGalleryPaint_80x24` | 22,006-23,064 | 280 | 31 |
| `DemoGalleryPaint_120x40` | 34,915-37,772 | 280 | 31 |
| `DemoGalleryHandleKeyEditCycle` | 899-970 | 64 | 3 |
| `Blit_80x24` | 5,219-5,354 | 0 | 0 |
| `WriteString_Line` | 1,590-1,640 | 0 | 0 |
| `FlattenZOrder_Nested` | 766-789 | 2,168 | 8 |
| `FullRender_80x24` | 75,433-76,777 | 8,255 | 4 |
| `GenerateANSI_80x24` | 38,919-41,954 | 8,231 | 2 |
| `EditorPaint_80x24` | 112,558-113,689 | 4,288 | 633 |
| `EditorPaint_120x40` | 249,929-257,167 | 7,952 | 1,144 |
| `EditorPaint_LargeFile_120x40` | 253,526-256,495 | 7,952 | 1,144 |
| `EditorScrollRepaint_120x40` | 249,929-253,725 | 7,952 | 1,144 |
| `BufferDirtyCheckLargeFile` (2026-09-17) | 0.59-0.63 | 0 | 0 |
| `ChatStreamPaint_80x24` | 106,946-109,622 | 6,747 | 122 |
| `ChatStreamPaint_120x40` | 231,827-235,747 | 16,412 | 226 |
| `BenchmarkIdleTick_200Msgs` (2026-10-17) | 142,566-144,123 | 12,664 | 205 |
| `BenchmarkTabRederive_80Msgs` (2026-10-17) | 136,478-139,346 | 11,709-11,710 | 173 |
| `BenchmarkPointerMotionCoalesced` | 12,207 | 9,835 | 42 |
| `BenchmarkAgentSwitchWarm_80Msgs` | 199,537-209,565 | 12,281 | 262 |
| `BenchmarkSidebarResizePresentedFrame_500Msgs` | 325,690-343,457 | 20,963 | 262 |
| `BenchmarkSidebarResizeMutation_500Msgs` | 10,774-11,689 | 8,463 | 48 |
| `BenchmarkDenseChatSingleRowChange_500Msgs` | 483,005-507,837 | 53,711-53,901 | 2,277 |
| `BenchmarkDenseChatWidthCommit_500HeavyMsgs` | 950,978-1,318,798 | 377,352-377,462 | 1,810-1,811 |

`DemoGalleryHandleKeyEditCycle` measures one insertion plus Backspace per op
(keeps input length constant). The scenario benches measure a full
`handleTick` + shell-frame paint, not a single render call: `IdleTick_200Msgs`
pumps idle frames against a hydrated 200-message mixed-role session;
`TabRederive_80Msgs` re-derives the same agent tab per frame.

**Idle frame note (2026-10-17):** both had drifted to 15,049 B / 226 allocs and
14,094 B / 194 allocs, over their byte ceilings. The profile showed panel
chrome probing the terminal environment for Nerd Font support on every paint;
each `os.Getenv` allocates on Windows. The environment is fixed for the process,
so `filetree.DetectNerdFont` now probes once. A root agent's pane also made
`wantsFastTick` hold every idle session at the 16 ms streaming tick; the idle
benches now measure the true idle cadence (bytes are identical on both paths).

**Editor allocation fix note:** the four `EditorPaint`/`EditorScrollRepaint`
benches used to exceed their old byte ceilings (13,520B at 80x24, 25,152B at
120x40) because the per-frame steady path re-materialized the line string
(`string(b)` before the geometry cache check) and rebuilt the grapheme-cluster
slice on every call. `lineGeomInfo` and `lineRenderInfo` now serve cache hits
without allocating (~-68% bytes/op); the ceilings were tightened to the new
measured values +20% headroom.

**Text-edit undo note:** shared TextField undo retains one inverse-edit record.
Insert+immediate-Backspace coalesces that record and measures 64B/3 allocs; its
ceiling is 80B/3 allocs (20% byte headroom), rather than dropping undo semantics
to preserve the obsolete 48B limit.

**Interaction scheduling note:** model mutation remains immediate, but
high-frequency pointer, stream, and terminal-output presentations are
coalesced to one 16 ms deadline. Idle uses the existing 400 ms tick (2.5 FPS).
`BenchmarkPointerMotionCoalesced` measures the cached-ANSI drain path;
`BenchmarkAgentSwitchWarm_80Msgs` measures a complete warm focus transition.
`BenchmarkDenseChatWidthCommit_500HeavyMsgs` measures the release/committed
reflow frame with 500 rich twelve-line Markdown messages; only viewport plus
overscan is synchronously reflowed.

**Dense chat diagnosis:** before the single-authority fix, a 500-message
presented resize frame cost 7.5-7.9 ms, 2.22 MB, and about 20,500 allocations.
`computeContentLayout` wrote the intermediate width into the renderer and
`configureChatScreenWithLayout` immediately wrote the committed width, causing
two full cache invalidations per frame. Layout is now pure; projection
configuration is the only writer. The generation-owned `MessageRowsSnapshot`
and indexed row deltas keep one animated-row frame effectively constant from
50 to 500 messages (~0.49 vs ~0.51 ms, equal allocation count).

## Timing review guidance

Not machine-enforced; a merge reviewer should ask for a benchstat comparison
when measured time moves materially:

| Benchmark | Expected upper time |
|-----------|---------------------|
| `DemoGalleryPaint_80x24` | 45,000 ns/op |
| `DemoGalleryPaint_120x40` | 70,000 ns/op |
| `DemoGalleryHandleKeyEditCycle` | 750 ns/op |
| `Blit_80x24` | 15,000 ns/op |
| `WriteString_Line` | 5,000 ns/op |
| `FlattenZOrder_Nested` | 1,500 ns/op |
| `FullRender_80x24` | 175,000 ns/op |
| `GenerateANSI_80x24` | 70,000 ns/op |
| `EditorPaint_80x24` | 250,000 ns/op |
| `EditorPaint_120x40` | 400,000 ns/op |
| `EditorPaint_LargeFile_120x40` | 400,000 ns/op |
| `EditorScrollRepaint_120x40` | 400,000 ns/op |
| `ChatStreamPaint_80x24` | 250,000 ns/op |
| `ChatStreamPaint_120x40` | 500,000 ns/op |
| `BenchmarkIdleTick_200Msgs` | 500,000 ns/op |
| `BenchmarkTabRederive_80Msgs` | 400,000 ns/op |
| `BenchmarkAgentSwitchWarm_80Msgs` | 350,000 ns/op |
| `BenchmarkSidebarResizeMutation_500Msgs` | 50,000 ns/op |
| `BenchmarkSidebarResizePresentedFrame_500Msgs` | 1,000,000 ns/op |
| `BenchmarkDenseChatSingleRowChange_500Msgs` | 1,000,000 ns/op |
| `BenchmarkDenseChatWidthCommit_500HeavyMsgs` | 2,000,000 ns/op |

## Interpretation Rules

1. Compare like-for-like benchmark names, terminal sizes, GOOS/GOARCH, Go
   versions, and CPU power modes.
2. Treat allocation growth as a regression unless the change documents why.
   The ceilings live in `scripts/perf_ceilings.txt`; there is no second list
   in this file to drift out of sync.
3. Treat a repeated timing increase above 15% as requiring investigation even
   under the expected-time ceiling.
4. Any new steady-state render path needs a work-budget counterpart (or an
   explicit carve-out in `nilCacheExempt` / the budget constants) — not just a
   leaf benchmark.
5. Do not optimize by bypassing `docs/reference/terminal-width.md`, Surface
   compositing, theme tokens, component semantics, or proper invalidation.
6. Add a benchmark when introducing a materially new hot path such as
   variable-height virtualization, root Host paint, or product-screen
   component composition.

## Known Baseline Costs

- Turn-status rows re-render every tick by design (never cached) and are
  counted separately (`StatusRenders`), not budgeted.
- `buildGlobalPaneLineMap` (chatprojection renderer) rebuilds a global
  line map over all messages per frame — O(N) retained when it could be
  per-pane/virtualized. Measured acceptable at current session sizes; revisit
  if the scenario benches move.
- `Present()` regenerates a full ANSI frame (`GenerateANSI(c.back)`) whenever
  any damage exists — no dirty-rect stripping. Counted via `ANSIFrames` /
  `ANSICells` so a change in frame cost is visible in the sentinel.
- Hydration is O(N²) in the current load path (session messages are read and
  re-inserted per row through the append path) and the session DB is loaded
  twice on resume. Both are startup-bound, not frame-bound.
- The remaining steady editor-path allocations (~633-1,144/op) are per-cell
  `cell.NewCell` construction and per-line `PieceTable.Line` buffers; further
  reduction wants a retained line-paint model, not micro-fixes.
