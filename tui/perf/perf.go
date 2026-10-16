// Package perf exposes work-accounting counters for the TUI render pipeline.
//
// A CPU-hammering regression in a tick-driven TUI is almost never a single
// function getting fatter; it is a call-frequency regression (a cache dropped
// every tick, a stream of full re-renders) that multiplies per-frame cost by
// 25Hz. Leaf allocation benches cannot see that class: nothing they benchmark
// gets slower, the pipeline around them just does 25x the work. These
// counters measure the consequence: render volume at the pipeline chokepoints,
// so whatever path re-dirties cached state pays a visible price, regardless
// of which file it lives in. Work-budget tests (tui/viewmodel perf_budget)
// and the live sentinel (CRUDE_PERF_LOG) gate on them.
package perf

import (
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

var (
	itemsRendered      atomic.Uint64 // full message re-renders (glamour/WordWrap path)
	linesRendered      atomic.Uint64 // body lines produced by those re-renders
	statusRenders      atomic.Uint64 // role-status re-renders (per-tick by design, tiny)
	paneRenders        atomic.Uint64 // subpane item re-renders (per-frame by design)
	cacheInvalidations atomic.Uint64 // whole-projection cache drops
	itemInvalidations  atomic.Uint64 // single-item cache drops
	ansiFrames         atomic.Uint64 // frames that regenerated full-frame ANSI
	ansiCells          atomic.Uint64 // cells covered by those regenerations
)

// Stats is a point-in-time snapshot of the counters.
type Stats struct {
	ItemsRendered      uint64
	LinesRendered      uint64
	StatusRenders      uint64
	PaneRenders        uint64
	CacheInvalidations uint64
	ItemInvalidations  uint64
	ANSIFrames         uint64
	ANSICells          uint64
}

// Sub returns s-o saturating at zero, for delta windows.
func (s Stats) Sub(o Stats) Stats {
	sub := func(a, b uint64) uint64 {
		if a < b {
			return 0
		}
		return a - b
	}
	return Stats{
		ItemsRendered:      sub(s.ItemsRendered, o.ItemsRendered),
		LinesRendered:      sub(s.LinesRendered, o.LinesRendered),
		StatusRenders:      sub(s.StatusRenders, o.StatusRenders),
		PaneRenders:        sub(s.PaneRenders, o.PaneRenders),
		CacheInvalidations: sub(s.CacheInvalidations, o.CacheInvalidations),
		ItemInvalidations:  sub(s.ItemInvalidations, o.ItemInvalidations),
		ANSIFrames:         sub(s.ANSIFrames, o.ANSIFrames),
		ANSICells:          sub(s.ANSICells, o.ANSICells),
	}
}

func Snapshot() Stats {
	return Stats{
		ItemsRendered:      itemsRendered.Load(),
		LinesRendered:      linesRendered.Load(),
		StatusRenders:      statusRenders.Load(),
		PaneRenders:        paneRenders.Load(),
		CacheInvalidations: cacheInvalidations.Load(),
		ItemInvalidations:  itemInvalidations.Load(),
		ANSIFrames:         ansiFrames.Load(),
		ANSICells:          ansiCells.Load(),
	}
}

// Reset zeroes all counters and probe sites. Tests call it to delimit a
// measurement window; counters within a window should be read via the
// returned deltas of Snapshot rather than Reset for concurrency safety.
func Reset() {
	itemsRendered.Store(0)
	linesRendered.Store(0)
	statusRenders.Store(0)
	paneRenders.Store(0)
	cacheInvalidations.Store(0)
	itemInvalidations.Store(0)
	ansiFrames.Store(0)
	ansiCells.Store(0)
	probeMu.Lock()
	probeSites = probeSites[:0]
	probeMu.Unlock()
}

// Markers -------------------------------------------------------------------

// MarkItemRendered records one full message re-render (a cache miss through
// renderSingleMessage or the stale-native re-render path) and the body-line
// volume it produced. This is the glamour/WordWrap-class CPU cost.
func MarkItemRendered(lines int) {
	itemsRendered.Add(1)
	linesRendered.Add(uint64(lines))
	probe("item-rendered")
}

// MarkStatusRerender records a role-status re-render. InvalidateCache drops
// status rows every tick and they are never cached, so status churn is
// pre-existing bounded churn (short bodies) that scales with historical
// status-row count, not the re-render class these budgets hunt. Counted
// separately so it cannot drown the expensive-work signal.
func MarkStatusRerender() {
	statusRenders.Add(1)
	probe("status-rerender")
}

// MarkPaneRendered records a subpane item re-render. Nested panes intentionally
// re-render per frame to reflect wheel/drag offsets, so work-budget tests are
// expected to budget for these separately from ItemsRendered.
func MarkPaneRendered() {
	paneRenders.Add(1)
}

// MarkCacheInvalidation records a whole-projection cache drop.
func MarkCacheInvalidation() {
	cacheInvalidations.Add(1)
	probe("cache-invalidate")
}

// MarkItemInvalidation records a single-item cache drop.
func MarkItemInvalidation() {
	itemInvalidations.Add(1)
}

// MarkANSIFrame records one compositor Present that regenerated full-frame
// ANSI (cells = w*h of the layer). Full-frame regeneration is a known,
// documented per-frame cost; the counter is for visibility, and budgets
// should treat it relative to layer size, not in isolation.
func MarkANSIFrame(cells int) {
	ansiFrames.Add(1)
	ansiCells.Add(uint64(max(0, cells)))
}

// Probe ---------------------------------------------------------------------

// probeEnabled gates the probe ring. runtime.Caller costs microseconds, so
// production runs with it off; the work-budget tests and the sentinel enable
// it to attribute a budget violation to the code that caused it.
var probeEnabled atomic.Bool

func EnableProbe()  { probeEnabled.Store(true) }
func DisableProbe() { probeEnabled.Store(false) }
func ProbeEnabled() bool {
	return probeEnabled.Load()
}

// probeSite is one distinct (marker, caller) pair seen while the probe was on.
type probeSite struct {
	key   string
	count uint64
}

var (
	probeMu    sync.Mutex
	probeSites []probeSite
)

const maxProbeSites = 64

// probe records one hit attributed to the caller of the public marker.
func probe(marker string) {
	if !probeEnabled.Load() {
		return
	}
	pc, _, _, ok := runtime.Caller(2)
	name := "?"
	if ok && runtime.FuncForPC(pc) != nil {
		name = runtime.FuncForPC(pc).Name()
	}
	key := marker + ": " + name
	probeMu.Lock()
	defer probeMu.Unlock()
	for i := range probeSites {
		if probeSites[i].key == key {
			probeSites[i].count++
			return
		}
	}
	if len(probeSites) >= maxProbeSites {
		return
	}
	probeSites = append(probeSites, probeSite{key: key, count: 1})
}

// ProbeDump renders the recorded probe sites by frequency, most offending
// first. Budget tests append it to failure messages so a regression reports
// the exact call site that re-dirtied the pipeline.
func ProbeDump() string {
	probeMu.Lock()
	defer probeMu.Unlock()
	if len(probeSites) == 0 {
		return "(no probe sites recorded — probe disabled or no marked work)"
	}
	sorted := make([]probeSite, len(probeSites))
	copy(sorted, probeSites)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].count > sorted[j].count })
	var b strings.Builder
	for _, s := range sorted {
		b.WriteString("\t")
		b.WriteString(strconv.FormatUint(s.count, 10))
		b.WriteString("x ")
		b.WriteString(s.key)
		b.WriteString("\n")
	}
	return b.String()
}
