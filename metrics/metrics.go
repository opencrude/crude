// Package metrics records bounded, per-second performance history for one
// process. The shell and the core each own a registry; the Performance
// surface reads both (the core's through coreprotocol.CoreMetricsMethod).
//
// Recording is cheap and lock-scoped to one series: an observation is a
// clock read, a ring-slot update and, past the series' stall threshold, one
// entry in a bounded event ring. History is fixed-size (HistorySeconds), so
// a long-lived process never grows its metrics memory. Nothing here performs
// I/O or starts goroutines except Start's one-hertz runtime sampler.
package metrics

import (
	"slices"
	"strings"
	"sync"
	"time"
)

// HistorySeconds is the retained per-second history of every series.
const HistorySeconds = 300

// tableSeconds is the shorter history kept per keyed-table row; rows are
// numerous (message types, RPC methods, tools) and only need recent totals.
const tableSeconds = 120

// maxTableKeys bounds a table's distinct keys; later keys fold into "other".
const maxTableKeys = 192

// Kind tells the dashboard how to read and chart a series.
type Kind string

const (
	KindLatency Kind = "latency"
	KindCounter Kind = "counter"
	KindGauge   Kind = "gauge"
	KindTable   Kind = "table"
)

// Meta describes a series so a snapshot is self-describing: a shell can
// present core series it has no code for.
type Meta struct {
	Name  string `json:"name"`
	Group string `json:"group"`
	Title string `json:"title"`
	// Unit is the gauge/counter unit ("bytes", "items", "%"); latencies are
	// always microseconds.
	Unit string `json:"unit,omitempty"`
	Help string `json:"help,omitempty"`
	// Warn and Crit are health thresholds: microseconds for latency and
	// table series (applied to p95 and to single observations), and plain
	// values for gauges. Zero disables the threshold.
	Warn int64 `json:"warn,omitempty"`
	Crit int64 `json:"crit,omitempty"`
	// Stall records an event for any single observation at or above this
	// many microseconds (latency and table series). Zero uses Crit.
	Stall int64 `json:"stall,omitempty"`
}

func (m Meta) stallMicros() int64 {
	if m.Stall > 0 {
		return m.Stall
	}
	return m.Crit
}

type registry struct {
	mu        sync.Mutex
	latencies []*Latency
	counters  []*Counter
	gauges    []*Gauge
	tables    []*Table
	byName    map[string]any
}

var reg = registry{byName: make(map[string]any)}

// lookup returns an existing series of the requested type so independent
// registrations of one name (tests, repeated init) share a single series.
func lookup[T any](name string) (T, bool) {
	value, ok := reg.byName[name]
	if !ok {
		var zero T
		return zero, false
	}
	typed, ok := value.(T)
	return typed, ok
}

// now is the clock seam; tests replace it to place observations in exact
// seconds.
var now = time.Now

// --- Latency ---------------------------------------------------------------

// bucketBounds are inclusive upper bounds in microseconds. The final bucket
// is unbounded. Chosen around human-visible frame and input budgets.
var bucketBounds = [...]int64{100, 250, 500, 1_000, 2_000, 4_000, 8_000, 16_000, 33_000, 66_000, 125_000, 250_000, 500_000, 1_000_000, 2_000_000}

const bucketCount = len(bucketBounds) + 1

type histogram [bucketCount]uint32

func bucketOf(micros int64) int {
	for i, bound := range bucketBounds {
		if micros <= bound {
			return i
		}
	}
	return len(bucketBounds)
}

func (h *histogram) add(o *histogram) {
	for i := range h {
		h[i] += o[i]
	}
}

// quantile estimates the q-quantile as its bucket's upper bound, clamped to
// the observed maximum so a single sample reports its true value.
func (h *histogram) quantile(q float64, count uint64, maximum int64) int64 {
	if count == 0 {
		return 0
	}
	target := max(uint64(q*float64(count)+0.5), 1)
	var seen uint64
	for i, n := range h {
		seen += uint64(n)
		if seen >= target {
			if i < len(bucketBounds) {
				return min(bucketBounds[i], maximum)
			}
			return maximum
		}
	}
	return maximum
}

type latencySlot struct {
	sec     int64
	count   uint32
	sum     int64
	max     int64
	buckets histogram
}

// Latency is a duration series: per-second count, sum, maximum and a
// coarse histogram for percentiles.
type Latency struct {
	meta  Meta
	mu    sync.Mutex
	slots [HistorySeconds]latencySlot
	total uint64
}

// NewLatency registers (or returns the existing) latency series.
func NewLatency(meta Meta) *Latency {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if existing, ok := lookup[*Latency](meta.Name); ok {
		return existing
	}
	l := &Latency{meta: meta}
	reg.latencies = append(reg.latencies, l)
	reg.byName[meta.Name] = l
	return l
}

// Observe records one duration.
func (l *Latency) Observe(d time.Duration) { l.ObserveLabeled(d, "") }

// Since records the time elapsed since start.
func (l *Latency) Since(start time.Time) { l.ObserveLabeled(now().Sub(start), "") }

// ObserveLabeled records one duration; label names the cause in a stall
// event and is otherwise ignored.
func (l *Latency) ObserveLabeled(d time.Duration, label string) {
	if l == nil {
		return
	}
	at := now()
	micros := max(int64(d/time.Microsecond), 0)
	sec := at.Unix()
	l.mu.Lock()
	slot := &l.slots[sec%HistorySeconds]
	if slot.sec != sec {
		*slot = latencySlot{sec: sec}
	}
	slot.count++
	slot.sum += micros
	slot.max = max(slot.max, micros)
	if b := &slot.buckets[bucketOf(micros)]; *b < ^uint32(0) {
		*b++
	}
	l.total++
	l.mu.Unlock()
	if stall := l.meta.stallMicros(); stall > 0 && micros >= stall {
		recordEvent(Event{At: at, Group: l.meta.Group, Series: l.meta.Name, Title: l.meta.Title, Label: label, Micros: micros})
	}
}

// --- Counter ---------------------------------------------------------------

type counterSlot struct {
	sec int64
	n   uint64
}

// Counter is a monotonically added quantity reported per second.
type Counter struct {
	meta  Meta
	mu    sync.Mutex
	slots [HistorySeconds]counterSlot
	total uint64
}

// NewCounter registers (or returns the existing) counter series.
func NewCounter(meta Meta) *Counter {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if existing, ok := lookup[*Counter](meta.Name); ok {
		return existing
	}
	c := &Counter{meta: meta}
	reg.counters = append(reg.counters, c)
	reg.byName[meta.Name] = c
	return c
}

// Add records n occurrences in the current second.
func (c *Counter) Add(n uint64) {
	if c == nil || n == 0 {
		return
	}
	sec := now().Unix()
	c.mu.Lock()
	slot := &c.slots[sec%HistorySeconds]
	if slot.sec != sec {
		*slot = counterSlot{sec: sec}
	}
	slot.n += n
	c.total += n
	c.mu.Unlock()
}

// Inc records one occurrence.
func (c *Counter) Inc() { c.Add(1) }

// --- Gauge -----------------------------------------------------------------

type gaugeSlot struct {
	sec  int64
	last int64
	max  int64
	set  bool
}

// Gauge is a sampled level (queue depth, heap bytes). Each second keeps its
// last and maximum value.
type Gauge struct {
	meta    Meta
	mu      sync.Mutex
	slots   [HistorySeconds]gaugeSlot
	current int64
	sample  func() int64
}

// NewGauge registers (or returns the existing) gauge series. A non-nil
// sample function is polled once per second by the runtime sampler; it must
// be cheap and safe to call from any goroutine.
func NewGauge(meta Meta, sample func() int64) *Gauge {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if existing, ok := lookup[*Gauge](meta.Name); ok {
		if sample != nil {
			existing.mu.Lock()
			existing.sample = sample
			existing.mu.Unlock()
		}
		return existing
	}
	g := &Gauge{meta: meta, sample: sample}
	reg.gauges = append(reg.gauges, g)
	reg.byName[meta.Name] = g
	return g
}

// Set records the current level.
func (g *Gauge) Set(v int64) {
	if g == nil {
		return
	}
	sec := now().Unix()
	g.mu.Lock()
	slot := &g.slots[sec%HistorySeconds]
	if slot.sec != sec {
		*slot = gaugeSlot{sec: sec, max: v}
	}
	slot.last, slot.set = v, true
	slot.max = max(slot.max, v)
	g.current = v
	g.mu.Unlock()
}

// Add moves the current level by delta (for in-flight counts).
func (g *Gauge) Add(delta int64) {
	if g == nil {
		return
	}
	g.mu.Lock()
	v := g.current + delta
	g.mu.Unlock()
	g.Set(v)
}

// --- Table -----------------------------------------------------------------

type tableSlot struct {
	sec   int64
	count uint32
	sum   int64
	max   int64
}

type tableRow struct {
	slots    [tableSeconds]tableSlot
	total    uint64
	totalSum int64
	lastAt   int64
}

// Table is a keyed latency table (per message type, RPC method, tool): the
// dashboard ranks keys by time consumed so the owner of a cost is named.
type Table struct {
	meta Meta
	mu   sync.Mutex
	rows map[string]*tableRow
}

// NewTable registers (or returns the existing) keyed table.
func NewTable(meta Meta) *Table {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if existing, ok := lookup[*Table](meta.Name); ok {
		return existing
	}
	t := &Table{meta: meta, rows: make(map[string]*tableRow)}
	reg.tables = append(reg.tables, t)
	reg.byName[meta.Name] = t
	return t
}

// Observe records one duration against key.
func (t *Table) Observe(key string, d time.Duration) {
	if t == nil {
		return
	}
	at := now()
	micros := max(int64(d/time.Microsecond), 0)
	sec := at.Unix()
	t.mu.Lock()
	row := t.rows[key]
	if row == nil {
		if len(t.rows) >= maxTableKeys {
			key = "other"
			row = t.rows[key]
		}
		if row == nil {
			row = &tableRow{}
			t.rows[key] = row
		}
	}
	slot := &row.slots[sec%tableSeconds]
	if slot.sec != sec {
		*slot = tableSlot{sec: sec}
	}
	slot.count++
	slot.sum += micros
	slot.max = max(slot.max, micros)
	row.total++
	row.totalSum += micros
	row.lastAt = sec
	t.mu.Unlock()
	if stall := t.meta.stallMicros(); stall > 0 && micros >= stall {
		recordEvent(Event{At: at, Group: t.meta.Group, Series: t.meta.Name, Title: t.meta.Title, Label: key, Micros: micros})
	}
}

// Since records the time elapsed since start against key.
func (t *Table) Since(key string, start time.Time) { t.Observe(key, now().Sub(start)) }

// --- Events ----------------------------------------------------------------

// Event is one notable occurrence: a stall over a series' threshold, a
// blocking call on the UI goroutine, or an explicit Note.
type Event struct {
	At     time.Time `json:"at"`
	Group  string    `json:"group"`
	Series string    `json:"series,omitempty"`
	Title  string    `json:"title"`
	Label  string    `json:"label,omitempty"`
	Micros int64     `json:"us,omitempty"`
	Detail string    `json:"detail,omitempty"`
	// Severity is "warn" or "crit"; stalls are "crit" when past Crit.
	Severity string `json:"severity,omitempty"`
}

const maxEvents = 256

var events struct {
	mu   sync.Mutex
	ring [maxEvents]Event
	next int
	n    int
	seq  uint64
}

func recordEvent(event Event) {
	if event.Severity == "" {
		event.Severity = "crit"
	}
	if len(event.Label) > 160 {
		event.Label = event.Label[:160]
	}
	if len(event.Detail) > 400 {
		event.Detail = event.Detail[:400]
	}
	events.mu.Lock()
	events.ring[events.next] = event
	events.next = (events.next + 1) % maxEvents
	events.n = min(events.n+1, maxEvents)
	events.seq++
	events.mu.Unlock()
}

// Note records an explicit event (a crash, a restart, a recovery).
func Note(group, title, label, detail string, severity string) {
	recordEvent(Event{At: now(), Group: group, Title: title, Label: label, Detail: detail, Severity: severity})
}

// EventSequence increases with every recorded event; presentation compares
// it to decide whether anything new happened.
func EventSequence() uint64 {
	events.mu.Lock()
	defer events.mu.Unlock()
	return events.seq
}

// EventsSince returns recorded events at or after from, oldest first.
func EventsSince(from time.Time) []Event { return eventsSince(from) }

func eventsSince(from time.Time) []Event {
	events.mu.Lock()
	defer events.mu.Unlock()
	out := make([]Event, 0, events.n)
	for i := 0; i < events.n; i++ {
		event := events.ring[(events.next-events.n+i+maxEvents)%maxEvents]
		if !event.At.Before(from) {
			out = append(out, event)
		}
	}
	return out
}

// --- Reset (tests) ---------------------------------------------------------

// ResetForTest clears every series' history and the event ring while keeping
// registrations, so package-level series remain valid.
func ResetForTest() {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	for _, l := range reg.latencies {
		l.mu.Lock()
		l.slots, l.total = [HistorySeconds]latencySlot{}, 0
		l.mu.Unlock()
	}
	for _, c := range reg.counters {
		c.mu.Lock()
		c.slots, c.total = [HistorySeconds]counterSlot{}, 0
		c.mu.Unlock()
	}
	for _, g := range reg.gauges {
		g.mu.Lock()
		g.slots, g.current = [HistorySeconds]gaugeSlot{}, 0
		g.mu.Unlock()
	}
	for _, t := range reg.tables {
		t.mu.Lock()
		t.rows = make(map[string]*tableRow)
		t.mu.Unlock()
	}
	events.mu.Lock()
	events.ring, events.next, events.n = [maxEvents]Event{}, 0, 0
	events.mu.Unlock()
}

// SetClockForTest replaces the clock and returns a restore function.
func SetClockForTest(clock func() time.Time) func() {
	previous := now
	now = clock
	return func() { now = previous }
}

// groupOf derives a display group from a dotted name when Meta.Group is
// empty: "lsp.request" → "lsp".
func groupOf(meta Meta) string {
	if meta.Group != "" {
		return meta.Group
	}
	head, _, _ := strings.Cut(meta.Name, ".")
	return head
}

func sortedByName[T any](values []T, name func(T) string) []T {
	out := slices.Clone(values)
	slices.SortFunc(out, func(a, b T) int { return strings.Compare(name(a), name(b)) })
	return out
}
