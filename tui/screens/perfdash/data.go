package perfdash

import (
	"slices"
	"strings"
	"time"

	"crude/metrics"
)

// Data is one refresh of the dashboard: the shell's own registry and, when
// the shell is attached to a separate core process, the core's.
type Data struct {
	Shell   metrics.Snapshot
	Core    *metrics.Snapshot
	CoreErr string
	// Fetched is when the refresh completed (shell clock).
	Fetched time.Time
}

// process labels the two registries.
type process uint8

const (
	processShell process = iota
	processCore
)

func (p process) label() string {
	if p == processCore {
		return "Core"
	}
	return "Shell"
}

// snapshot returns a process's registry. Without a separate core (the core
// runs in this process, or its metrics are unavailable) core series are read
// from the shell's registry, where an in-process core records them.
func (d Data) snapshot(p process) *metrics.Snapshot {
	if p == processCore && d.Core != nil {
		return d.Core
	}
	return &d.Shell
}

// processes lists the snapshots present, shell first.
func (d Data) processes() []process {
	if d.Core != nil {
		return []process{processShell, processCore}
	}
	return []process{processShell}
}

func (d Data) latency(p process, name string) (metrics.LatencySeries, bool) {
	s := d.snapshot(p)
	if s == nil {
		return metrics.LatencySeries{}, false
	}
	for _, series := range s.Latencies {
		if series.Meta.Name == name {
			return series, true
		}
	}
	return metrics.LatencySeries{}, false
}

func (d Data) counter(p process, name string) (metrics.CounterSeries, bool) {
	s := d.snapshot(p)
	if s == nil {
		return metrics.CounterSeries{}, false
	}
	for _, series := range s.Counters {
		if series.Meta.Name == name {
			return series, true
		}
	}
	return metrics.CounterSeries{}, false
}

func (d Data) gauge(p process, name string) (metrics.GaugeSeries, bool) {
	s := d.snapshot(p)
	if s == nil {
		return metrics.GaugeSeries{}, false
	}
	for _, series := range s.Gauges {
		if series.Meta.Name == name {
			return series, true
		}
	}
	return metrics.GaugeSeries{}, false
}

func (d Data) table(p process, name string) (metrics.TableSeries, bool) {
	s := d.snapshot(p)
	if s == nil {
		return metrics.TableSeries{}, false
	}
	for _, series := range s.Tables {
		if series.Meta.Name == name {
			return series, true
		}
	}
	return metrics.TableSeries{}, false
}

// processedEvent is an event tagged with its process.
type processedEvent struct {
	metrics.Event
	process process
}

// events returns both processes' events, newest first.
func (d Data) events() []processedEvent {
	var out []processedEvent
	for _, p := range d.processes() {
		for _, event := range d.snapshot(p).Events {
			out = append(out, processedEvent{Event: event, process: p})
		}
	}
	slices.SortStableFunc(out, func(a, b processedEvent) int { return b.At.Compare(a.At) })
	return out
}

// busySeries derives the UI goroutine's busy share per point from the
// Update and paint sums: microseconds of loop work per microsecond of wall.
func (d Data) busySeries() ([]float64, bool) {
	update, okUpdate := d.latency(processShell, "ui.update")
	view, okView := d.latency(processShell, "ui.view")
	if !okUpdate && !okView {
		return nil, false
	}
	step := max(d.Shell.Step, 1)
	n := max(len(update.Points), len(view.Points))
	out := make([]float64, n)
	for i := range out {
		var sum int64
		if i < len(update.Points) {
			sum += update.Points[i].Sum
		}
		if i < len(view.Points) {
			sum += view.Points[i].Sum
		}
		out[i] = float64(sum) / float64(step*1_000_000) * 100
	}
	return out, true
}

// recentBusy is the UI busy share over the last five seconds.
func (d Data) recentBusy() float64 {
	update, _ := d.latency(processShell, "ui.update")
	view, _ := d.latency(processShell, "ui.view")
	return float64(update.Recent.Sum+view.Recent.Sum) / 5_000_000 * 100
}

// windowSeconds is the span the snapshots cover.
func (d Data) windowSeconds() int {
	points := 0
	for _, series := range d.Shell.Latencies {
		points = max(points, len(series.Points))
	}
	return points * max(d.Shell.Step, 1)
}

// tableShare is a row's share of its table's window time.
func tableShare(table metrics.TableSeries, row metrics.TableRow) float64 {
	var total int64
	for _, r := range table.Rows {
		total += r.Sum
	}
	if total == 0 {
		return 0
	}
	return float64(row.Sum) / float64(total)
}

// shortKey trims Go package paths from message type names so the owner is
// readable in a narrow column: "crude/shellport.ReplicaChangesMsg" →
// "shellport.ReplicaChangesMsg".
func shortKey(key string) string {
	key = strings.TrimPrefix(key, "*")
	if i := strings.LastIndex(key, "/"); i >= 0 && !strings.Contains(key, " ") {
		key = key[i+1:]
	}
	return key
}
