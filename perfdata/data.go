// Package perfdata projects metrics snapshots into shared, presentation-neutral findings.
// It performs no sampling, I/O, retention or background work.
package perfdata

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
type Process uint8

const (
	ProcessShell Process = iota
	ProcessCore
)

func (p Process) Label() string {
	if p == ProcessCore {
		return "Core"
	}
	return "Shell"
}

// Snapshot returns only the requested process; absent core evidence is never
// silently relabeled shell evidence.
func (d Data) Snapshot(p Process) *metrics.Snapshot {
	if p == ProcessCore {
		return d.Core
	}
	return &d.Shell
}

// processes lists the snapshots present, shell first.
func (d Data) Processes() []Process {
	var out []Process
	if !d.Shell.Taken.IsZero() || d.Shell.Resources != nil || len(d.Shell.Latencies)+len(d.Shell.Gauges)+len(d.Shell.Counters)+len(d.Shell.Events)+len(d.Shell.Tables) > 0 {
		out = append(out, ProcessShell)
	}
	if d.Core != nil {
		out = append(out, ProcessCore)
	}
	return out
}

func (d Data) Latency(p Process, name string) (metrics.LatencySeries, bool) {
	s := d.Snapshot(p)
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

func (d Data) Counter(p Process, name string) (metrics.CounterSeries, bool) {
	s := d.Snapshot(p)
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

func (d Data) Gauge(p Process, name string) (metrics.GaugeSeries, bool) {
	s := d.Snapshot(p)
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

func (d Data) Table(p Process, name string) (metrics.TableSeries, bool) {
	s := d.Snapshot(p)
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

// ProcessedEvent is an event tagged with its process.
type ProcessedEvent struct {
	metrics.Event
	Process Process
}

// events returns both processes' events, newest first.
func (d Data) Events() []ProcessedEvent {
	var out []ProcessedEvent
	for _, p := range d.Processes() {
		for _, event := range d.Snapshot(p).Events {
			out = append(out, ProcessedEvent{Event: event, Process: p})
		}
	}
	slices.SortStableFunc(out, func(a, b ProcessedEvent) int { return b.At.Compare(a.At) })
	return out
}

// busySeries derives the UI goroutine's busy share per point from the
// Update and paint sums: microseconds of loop work per microsecond of wall.
func (d Data) BusySeries() ([]float64, bool) {
	busy, ok := metrics.UIBusy(d.Shell)
	return busy.Points, ok
}

// RecentBusy is event-loop work, not process CPU, over the recent window.
func (d Data) RecentBusy() float64 {
	busy, _ := metrics.UIBusy(d.Shell)
	return busy.Recent
}

// windowSeconds is the span the snapshots cover.
func (d Data) WindowSeconds() int {
	if d.Shell.Seconds > 0 {
		return d.Shell.Seconds
	}
	if d.Core != nil && d.Core.Seconds > 0 {
		return d.Core.Seconds
	}
	points := 0
	for _, series := range d.Shell.Latencies {
		points = max(points, len(series.Points))
	}
	return points * max(d.Shell.Step, 1)
}

// tableShare is a row's share of its table's window time.
func TableShare(table metrics.TableSeries, row metrics.TableRow) float64 {
	var total int64
	for _, r := range DiagnosticRows(table) {
		total += r.Sum
	}
	if total == 0 {
		return 0
	}
	return float64(row.Sum) / float64(total)
}

func DiagnosticRows(table metrics.TableSeries) []metrics.TableRow {
	if len(table.DiagnosticRows) > 0 {
		return table.DiagnosticRows
	}
	return table.Rows
}

// shortKey trims Go package paths from message type names so the owner is
// readable in a narrow column: "crude/shellport.ReplicaChangesMsg" →
// "shellport.ReplicaChangesMsg".
func ShortKey(key string) string {
	key = strings.TrimPrefix(key, "*")
	if i := strings.LastIndex(key, "/"); i >= 0 && !strings.Contains(key, " ") {
		key = key[i+1:]
	}
	return key
}
