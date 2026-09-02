// Live sentinel: watches per-window work counters from the running
// application and logs one throttled line when an idle frame busts the work
// budget. It is opt-in via CRUDE_PERF_LOG=<path> (set by app.Main) because
// main.go discards slog to protect the TUI surface, so the sentinel writes
// directly to the given file.
//
// The sentinel is the in-the-wild tripwire for the paths no benchmark covers:
// budget lines name the probe's top call sites, which is the same "which part
// exactly is breaking" answer the work-budget tests print.
package perf

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// sentinelWindowTicks is the granularity of one measurement window:
	// ~1s of ticks at the 25Hz tick loop. Long enough to wash out one-off
	// work, short enough to catch a per-tick invalidation loop within
	// seconds.
	sentinelWindowTicks = 32
	// throttleGap keeps the file readable when something degenerates into a
	// continuous bust — one line per distinct top offender per gap.
	throttleGap = 10 * time.Second
)

// sentinelBudget caps steady-state idle render work per window (32 frames).
// The budget is deliberately generous for legitimate one-off refreshes but
// far below what a cache dropped every tick or a per-frame full re-render
// produces: a 200-message session re-rendered at 25Hz yields tens of
// thousands of lines per window.
var sentinelBudget = Stats{
	ItemsRendered:      96,
	LinesRendered:      2560,
	CacheInvalidations: 320,
}

var (
	sentinelFile *os.File
	sentinelOnce sync.Once
	// activityFlag is set by NoteActivity for windows containing live
	// streaming or recent user input — work in those windows is expected.
	activityFlag atomic.Bool
	// windowBase is the last Snapshot read as a window base.
	windowBase  Stats
	tickCount   atomic.Uint64
	sentinelMu  sync.Mutex
	lastWrite   time.Time
	lastTopSite string
)

// ConfigureSentinel hands the sentinel its log sink and enables it. Used by
// app.Main with CRUDE_PERF_LOG; tests use it directly.
func ConfigureSentinel(path string) error {
	if path == "" {
		return nil
	}
	var openErr error
	sentinelOnce.Do(func() {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			openErr = err
			return
		}
		sentinelFile = f
		EnableProbe()
		windowBase = Snapshot()
	})
	return openErr
}

// SentinelActive reports whether the sentinel was configured.
func SentinelActive() bool { return sentinelFile != nil }

// NoteActivity flags the current window as containing streaming or user
// input, suppressing sentinel warnings for it. Call at streaming start and
// on key/mouse dispatch.
func NoteActivity() { activityFlag.Store(true) }

// SentinelTick advances the sentinel. Call once per tick from the update
// loop; it samples a counter window every sentinelWindowTicks ticks and
// appends a warning line when an idle window's render work exceeds the
// steady-state budget.
func SentinelTick() {
	if sentinelFile == nil {
		return
	}
	if tickCount.Add(1)%sentinelWindowTicks != 0 {
		return
	}
	now := time.Now()
	current := Snapshot()
	window := current.Sub(windowBase)
	windowBase = current
	active := activityFlag.Swap(false)

	if active || window.ItemsRendered <= sentinelBudget.ItemsRendered &&
		window.LinesRendered <= sentinelBudget.LinesRendered &&
		window.CacheInvalidations <= sentinelBudget.CacheInvalidations {
		return
	}

	top := firstProbeLine()
	sentinelMu.Lock()
	defer sentinelMu.Unlock()
	if !lastWrite.IsZero() && now.Sub(lastWrite) < throttleGap && lastTopSite == top {
		return
	}
	lastWrite = now
	lastTopSite = top
	_, _ = fmt.Fprintf(sentinelFile,
		"crude-perf: idle window exceeds work budget (items=%d lines=%d cache_inval=%d) top=%s\n",
		window.ItemsRendered, window.LinesRendered, window.CacheInvalidations, top)
}

// ResetSentinelState clears sentinel bookkeeping between tests (not counters).
func ResetSentinelState() {
	sentinelMu.Lock()
	defer sentinelMu.Unlock()
	lastWrite = time.Time{}
	lastTopSite = ""
}

// firstProbeLine picks the top probe site, if any, for the log line.
func firstProbeLine() string {
	dump := ProbeDump()
	line, _, _ := strings.Cut(dump, "\n")
	if line == "" {
		return "unknown (probe recorded no sites)"
	}
	return strings.TrimSpace(line)
}
