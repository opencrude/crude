# TUI Performance Baselines

Status: Phase 0 reference baseline

Recorded: 2026-07-17

Canonical plan: [`TUI_COMPONENT_SYSTEM_PLAN.md`](../../TUI_COMPONENT_SYSTEM_PLAN.md)

## Purpose

These numbers provide a reproducible local reference for component rendering,
event dispatch, Surface operations, and compositor output. They are not portable
wall-clock promises across different CPUs. Allocation counts are expected to remain
stable across comparable Go versions; timing changes must be compared on the same
machine and investigated when they leave the expected reference range.

No benchmark threshold is allowed to justify violating rendering correctness,
terminal-width invariants, component ownership, or accessibility semantics.

## Reference Environment

```text
OS: linux/amd64
Kernel: Linux 6.17.0-29-generic
Go: go1.25.9
CPU: Intel Core i7-10750H @ 2.60GHz
Cores/threads: 6/12
GOMAXPROCS: 12
```

CPU frequency scaling was enabled during capture. Five samples are recorded so the
observed range includes normal scheduler and frequency variation.

## Reproduction

Run from the repository root with no other intentional CPU-heavy workload:

```bash
go clean -testcache
GOMAXPROCS=12 go test -run '^$' \
  -bench 'Benchmark(DemoGallery|Blit|WriteString|Flatten|FullRender|GenerateANSI)' \
  -benchmem -count=5 \
  ./tui/ui ./tui/surface ./tui/compositor
```

Record environment information with:

```bash
go version
go env GOOS GOARCH GOVERSION
uname -srmo
lscpu
```

For statistically rigorous before/after comparisons, capture both revisions with the
same command and compare the outputs with `benchstat`. Do not compare results from
different power modes or materially different Go versions without noting that fact.

## Recorded Results

| Benchmark | Observed time range | Bytes/op | Allocs/op |
|-----------|---------------------|----------|-----------|
| `DemoGalleryPaint_80x24` | 37,661-38,770 ns | 2,816 | 59 |
| `DemoGalleryPaint_120x40` | 59,229-61,525 ns | 4,160 | 59 |
| `DemoGalleryHandleKeyEditCycle` | 433.9-457.1 ns | 48 | 3 |
| `Blit_80x24` | 8,439-8,769 ns | 0 | 0 |
| `WriteString_Line` | 2,805-2,861 ns | 0 | 0 |
| `FlattenZOrder_Nested` | 779.3-800.6 ns | 2,168 | 8 |
| `FullRender_80x24` | 102,508-104,310 ns | 49,460-49,461 | 7 |
| `GenerateANSI_80x24` | 39,908-40,519 ns | 8,228 | 2 |

`DemoGalleryHandleKeyEditCycle` measures two events per operation: one printable-rune
insertion followed by Backspace. The pair keeps input length constant so later
iterations do not benchmark progressively larger text.

## Expected Reference Ranges

The following ranges are review gates on the reference environment. A result outside
them is not automatically rejected, but it requires an explanation and a before/after
`benchstat` comparison before merge.

| Benchmark | Expected upper time | Expected allocation ceiling |
|-----------|---------------------|-----------------------------|
| `DemoGalleryPaint_80x24` | 60,000 ns/op | 3,000 B/op, 65 allocs/op |
| `DemoGalleryPaint_120x40` | 90,000 ns/op | 4,500 B/op, 65 allocs/op |
| `DemoGalleryHandleKeyEditCycle` | 750 ns/op | 64 B/op, 4 allocs/op |
| `Blit_80x24` | 15,000 ns/op | 0 B/op, 0 allocs/op |
| `WriteString_Line` | 5,000 ns/op | 0 B/op, 0 allocs/op |
| `FlattenZOrder_Nested` | 1,500 ns/op | 2,300 B/op, 8 allocs/op |
| `FullRender_80x24` | 175,000 ns/op | 52,000 B/op, 8 allocs/op |
| `GenerateANSI_80x24` | 70,000 ns/op | 9,000 B/op, 2 allocs/op |

The timing ceilings intentionally allow normal laptop frequency variation while still
surfacing regressions large enough to threaten interactive rendering. Allocation
ceilings are tighter because they are less hardware-dependent.

## Interpretation Rules

1. Compare like-for-like benchmark names, terminal sizes, Go versions, and CPU power
   modes.
2. Treat allocation growth as a regression unless the change documents why retained
   state or correctness requires it.
3. Treat a repeated timing increase above 15 percent as requiring investigation even
   if it remains under the broad reference ceiling.
4. Do not optimize by bypassing `TERMINAL_WIDTH.md`, Surface compositing, theme tokens,
   component semantics, or proper invalidation.
5. Add a benchmark when introducing a materially new hot path such as variable-height
   virtualization, root Host paint, or product-screen component composition.

## Known Baseline Costs

- Gallery paint currently allocates 59 times per frame. Phase 13 retained product-tree
  work should reduce or explain this count rather than treating it as an ideal target.
- `FlattenZOrder_Nested` allocates eight times per call. Surface reuse and retained
  traversal work may improve it, but current correctness tests take precedence.
- `FullRender_80x24` intentionally creates a Surface each iteration and generates a
  complete ANSI frame. It measures the current complete path, not the desired final
  retained-screen cost.
