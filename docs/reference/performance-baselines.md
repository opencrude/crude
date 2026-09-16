# TUI Performance Baselines

> Status: Current. Two enforcement layers ship in `make bench-gate`: leaf
> allocation ceilings from `scripts/perf_ceilings.txt`, and per-frame
> work-budget tests in `tui/viewmodel/perf_budget_test.go`. Timing is recorded
> but never machine-gated.

Recorded: 2026-08-30 (previous captures: 2026-07-17, 2026-07-31)

Canonical plan: [`docs/plans/tui-component-system-plan.md`](../plans/tui-component-system-plan.md)

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
| `TestLifecycleMetricSourceVersionUpdatesExactRow` | Context-upload/progress changes update exactly their canonical row through source versioning. |

Two by-design carve-outs keep the budgets honest:

- **Turn-status rows re-render every tick on purpose** (they are never
  cached). They are counted separately via `StatusRenders` and never counted
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
| `ChatStreamPaint_80x24` | 106,946-109,622 | 6,747 | 122 |
| `ChatStreamPaint_120x40` | 231,827-235,747 | 16,412 | 226 |
| `BenchmarkIdleTick_200Msgs` | 191,188-197,788 | 12,233 | 269 |
| `BenchmarkTabRederive_80Msgs` | 174,624-179,835 | 11,256-11,257 | 248 |
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

1. Compare like-for-like benchmark names, terminal sizes, Go versions, and CPU
   power modes.
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
