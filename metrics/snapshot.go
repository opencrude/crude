package metrics

import (
	"context"
	"os"
	"slices"
	"strings"
	"time"
)

// Query selects a snapshot window: the last Seconds seconds folded into at
// most Points points (Step = ceil(Seconds/Points) seconds each).
type Query struct {
	Seconds int `json:"seconds"`
	Points  int `json:"points"`
}

func (q Query) normalized() (seconds, step, points int) {
	seconds = q.Seconds
	if seconds <= 0 || seconds > HistorySeconds {
		seconds = min(max(seconds, 0), HistorySeconds)
		if seconds == 0 {
			seconds = 120
		}
	}
	points = q.Points
	if points <= 0 || points > seconds {
		points = seconds
	}
	step = (seconds + points - 1) / points
	points = (seconds + step - 1) / step
	return seconds, step, points
}

// Summary condenses a set of latency observations. Durations are
// microseconds.
type Summary struct {
	Count uint64 `json:"n,omitempty"`
	Sum   int64  `json:"sum,omitempty"`
	P50   int64  `json:"p50,omitempty"`
	P95   int64  `json:"p95,omitempty"`
	P99   int64  `json:"p99,omitempty"`
	Max   int64  `json:"max,omitempty"`
}

// LatencyPoint is one step of a latency series (microseconds).
type LatencyPoint struct {
	Count uint64 `json:"n,omitempty"`
	Sum   int64  `json:"sum,omitempty"`
	P95   int64  `json:"p95,omitempty"`
	Max   int64  `json:"max,omitempty"`
}

type LatencySeries struct {
	Meta   Meta           `json:"meta"`
	Points []LatencyPoint `json:"points"`
	// Window covers every point; Recent covers the last five seconds.
	Window Summary `json:"window"`
	Recent Summary `json:"recent"`
	Total  uint64  `json:"total"`
}

type CounterSeries struct {
	Meta   Meta     `json:"meta"`
	Points []uint64 `json:"points"`
	Window uint64   `json:"window"`
	Total  uint64   `json:"total"`
}

type GaugePoint struct {
	Last int64 `json:"last,omitempty"`
	Max  int64 `json:"max,omitempty"`
	Set  bool  `json:"set,omitempty"`
}

type GaugeSeries struct {
	Meta           Meta         `json:"meta"`
	Points         []GaugePoint `json:"points"`
	Current        int64        `json:"current"`
	Peak           int64        `json:"peak"`
	Observed       bool         `json:"observed"`
	CurrentAt      time.Time    `json:"current_at,omitzero"`
	WindowFirst    int64        `json:"window_first"`
	WindowLast     int64        `json:"window_last"`
	WindowObserved bool         `json:"window_observed"`
}

// TableRow aggregates one key over the window; Points is its time consumed
// per step (microseconds), present for the top rows only.
type TableRow struct {
	Key    string  `json:"key"`
	Count  uint64  `json:"n"`
	Sum    int64   `json:"sum"`
	Max    int64   `json:"max"`
	Recent int64   `json:"recent,omitempty"` // time consumed in the last five seconds
	Total  uint64  `json:"total"`
	Points []int64 `json:"points,omitempty"`
}

type TableSeries struct {
	Meta          Meta       `json:"meta"`
	Rows          []TableRow `json:"rows"`
	RowsAvailable int        `json:"rows_available"`
	RowsOmitted   int        `json:"rows_omitted"`
	Folded        uint64     `json:"folded"`
	// DiagnosticRows preserves all bounded row summaries when presentation keeps
	// only top rows. No points: diagnosis must not miss a rare slow owner merely
	// because it ranks below a display cap.
	DiagnosticRows []TableRow `json:"diagnostic_rows,omitempty"`
}

// Snapshot is a self-describing, JSON-serializable view of one process.
type Snapshot struct {
	Process           string          `json:"process"`
	PID               int             `json:"pid"`
	Started           time.Time       `json:"started"`
	Taken             time.Time       `json:"taken"`
	Completed         time.Time       `json:"completed"`
	Incarnation       string          `json:"incarnation"`
	Hostname          string          `json:"hostname,omitempty"`
	HostBootID        string          `json:"host_boot_id,omitempty"`
	HostIdentity      string          `json:"host_identity"`
	First             int64           `json:"first"`
	Seconds           int             `json:"seconds"`
	EventsOverwritten uint64          `json:"events_overwritten"`
	End               int64           `json:"end"` // unix second of the last point's last second
	Step              int             `json:"step"`
	Latencies         []LatencySeries `json:"latencies,omitempty"`
	Counters          []CounterSeries `json:"counters,omitempty"`
	Gauges            []GaugeSeries   `json:"gauges,omitempty"`
	Tables            []TableSeries   `json:"tables,omitempty"`
	Events            []Event         `json:"events,omitempty"`
	Resources         *Resources      `json:"resources,omitempty"`
	UIBusyPeak        float64         `json:"ui_busy_peak"`
}

const recentSeconds = 5
const tableRowLimit = 24
const tablePointRows = 8

// Take reads every registered series for the query window. It is safe to
// call from any goroutine and holds each series lock only while copying it.
func Take(q Query) Snapshot {
	snapshot, _ := TakeContext(context.Background(), q)
	return snapshot
}

// TakeContext is the dashboard snapshot path with caller-owned cancellation.
// Resources remain demand sampled; no read starts a polling loop.
func TakeContext(ctx context.Context, q Query) (Snapshot, error) {
	return takeContext(ctx, q, snapshotOptions{resources: true})
}

type snapshotOptions struct {
	all       bool // include unobserved registrations, every retained row and its points
	resources bool
	filter    string
}

func takeContext(ctx context.Context, q Query, options snapshotOptions) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	seconds, step, points := q.normalized()
	at := now()
	end := at.Unix()
	first := end - int64(seconds) + 1
	hostname, bootID, identity := hostProvenance()
	snapshot := Snapshot{Process: processName(), PID: os.Getpid(), Started: processStart, Taken: at, End: end, Step: step,
		First: first, Seconds: seconds, Incarnation: processIncarnation, Hostname: hostname, HostBootID: bootID, HostIdentity: identity}

	reg.mu.Lock()
	latencies := slices.Clone(reg.latencies)
	counters := slices.Clone(reg.counters)
	gauges := slices.Clone(reg.gauges)
	tables := slices.Clone(reg.tables)
	reg.mu.Unlock()

	index := func(sec int64) int { return int((sec - first) / int64(step)) }
	// UI utilization is an owner-level one-second fact, independent of chart
	// geometry and requested delivery points. Coarser charts cannot hide a peak.
	var busy [HistorySeconds]int64
	for _, l := range latencies {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		if !matchesMetric(l.meta, snapshot.Process, options.filter) {
			continue
		}
		series := LatencySeries{Meta: l.meta, Points: make([]LatencyPoint, points)}
		series.Meta.Group = groupOf(l.meta)
		var window, recent histogram
		var windowMax, recentMax int64
		stepHist := make([]histogram, points)
		l.mu.Lock()
		series.Total = l.total
		for i := range l.slots {
			slot := &l.slots[i]
			if slot.count == 0 || slot.sec < first || slot.sec > end {
				continue
			}
			p := &series.Points[index(slot.sec)]
			if l.meta.Name == "ui.update" || l.meta.Name == "ui.view" {
				busy[slot.sec%HistorySeconds] += slot.sum
			}
			p.Count += uint64(slot.count)
			p.Sum += slot.sum
			p.Max = max(p.Max, slot.max)
			stepHist[index(slot.sec)].add(&slot.buckets)
			window.add(&slot.buckets)
			series.Window.Count += uint64(slot.count)
			series.Window.Sum += slot.sum
			windowMax = max(windowMax, slot.max)
			if slot.sec > end-recentSeconds {
				recent.add(&slot.buckets)
				series.Recent.Count += uint64(slot.count)
				series.Recent.Sum += slot.sum
				recentMax = max(recentMax, slot.max)
			}
		}
		l.mu.Unlock()
		if series.Total == 0 && !options.all {
			continue
		}
		for i := range series.Points {
			p := &series.Points[i]
			p.P95 = stepHist[i].quantile(0.95, p.Count, p.Max)
		}
		series.Window = summarize(series.Window, &window, windowMax)
		series.Recent = summarize(series.Recent, &recent, recentMax)
		snapshot.Latencies = append(snapshot.Latencies, series)
	}
	for _, sum := range busy {
		snapshot.UIBusyPeak = max(snapshot.UIBusyPeak, float64(sum)/10_000)
	}
	for _, c := range counters {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		if !matchesMetric(c.meta, snapshot.Process, options.filter) {
			continue
		}
		series := CounterSeries{Meta: c.meta, Points: make([]uint64, points)}
		series.Meta.Group = groupOf(c.meta)
		c.mu.Lock()
		series.Total = c.total
		for i := range c.slots {
			slot := &c.slots[i]
			if slot.n == 0 || slot.sec < first || slot.sec > end {
				continue
			}
			series.Points[index(slot.sec)] += slot.n
			series.Window += slot.n
		}
		c.mu.Unlock()
		if series.Total == 0 && !options.all {
			continue
		}
		snapshot.Counters = append(snapshot.Counters, series)
	}
	for _, g := range gauges {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		if !matchesMetric(g.meta, snapshot.Process, options.filter) {
			continue
		}
		series := GaugeSeries{Meta: g.meta, Points: make([]GaugePoint, points)}
		series.Meta.Group = groupOf(g.meta)
		any := false
		g.mu.Lock()
		series.Current = g.current
		var latest int64
		for i := range g.slots {
			slot := &g.slots[i]
			if slot.set && (!series.Observed || slot.sec > latest) {
				series.Observed = true
				latest = slot.sec
			}
		}
		if series.Observed {
			series.CurrentAt = time.Unix(latest, 0)
		}
		var firstSet, lastSet int64
		for i := range g.slots {
			slot := &g.slots[i]
			if !slot.set || slot.sec < first || slot.sec > end {
				continue
			}
			if !series.WindowObserved || slot.sec < firstSet {
				firstSet = slot.sec
				series.WindowFirst = slot.last
			}
			if !series.WindowObserved || slot.sec > lastSet {
				lastSet = slot.sec
				series.WindowLast = slot.last
			}
			series.WindowObserved = true
			p := &series.Points[index(slot.sec)]
			if !p.Set || slot.max > p.Max {
				p.Max = slot.max
			}
			p.Set = true
			if !any || slot.max > series.Peak {
				series.Peak = slot.max
			}
			any = true
		}
		fillGaugeLast(series.Points, g, first, step, end)
		g.mu.Unlock()
		if !any && !options.all {
			continue
		}
		snapshot.Gauges = append(snapshot.Gauges, series)
	}
	for _, t := range tables {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		if !matchesMetric(t.meta, snapshot.Process, options.filter) {
			continue
		}
		series := TableSeries{Meta: t.meta}
		series.Meta.Group = groupOf(t.meta)
		rows := make([]TableRow, 0)
		rowPoints := make(map[string][]int64)
		t.mu.Lock()
		series.Folded = t.folded
		for key, row := range t.rows {
			out := TableRow{Key: key, Total: row.total}
			stepSums := make([]int64, points)
			for i := range row.slots {
				slot := &row.slots[i]
				if slot.count == 0 || slot.sec < first || slot.sec > end {
					continue
				}
				out.Count += uint64(slot.count)
				out.Sum += slot.sum
				out.Max = max(out.Max, slot.max)
				stepSums[index(slot.sec)] += slot.sum
				if slot.sec > end-recentSeconds {
					out.Recent += slot.sum
				}
			}
			if out.Count == 0 {
				continue
			}
			rows = append(rows, out)
			rowPoints[key] = stepSums
		}
		t.mu.Unlock()
		if len(rows) == 0 && !options.all {
			continue
		}
		slices.SortFunc(rows, func(a, b TableRow) int {
			if a.Sum != b.Sum {
				if a.Sum > b.Sum {
					return -1
				}
				return 1
			}
			return strings.Compare(a.Key, b.Key)
		})
		series.RowsAvailable = len(rows)
		if !options.all && len(rows) > tableRowLimit {
			series.DiagnosticRows = slices.Clone(rows)
			rows = rows[:tableRowLimit]
		}
		pointRows := len(rows)
		if !options.all {
			pointRows = min(pointRows, tablePointRows)
		}
		series.RowsOmitted = series.RowsAvailable - len(rows)
		for i := range rows[:pointRows] {
			rows[i].Points = rowPoints[rows[i].Key]
		}
		series.Rows = rows
		snapshot.Tables = append(snapshot.Tables, series)
	}
	events.mu.Lock()
	for i := 0; i < events.n; i++ {
		event := events.ring[(events.next-events.n+i+maxEvents)%maxEvents]
		if !event.At.Before(time.Unix(first, 0)) && !event.At.After(at) {
			snapshot.Events = append(snapshot.Events, event)
		}
	}
	snapshot.EventsOverwritten = events.dropped
	events.mu.Unlock()
	if options.resources {
		snapshot.Resources = takeResourcesContext(ctx, options.all)
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	snapshot.Completed = time.Now()
	snapshot.Latencies = sortedByName(snapshot.Latencies, func(s LatencySeries) string { return s.Meta.Name })
	snapshot.Counters = sortedByName(snapshot.Counters, func(s CounterSeries) string { return s.Meta.Name })
	snapshot.Gauges = sortedByName(snapshot.Gauges, func(s GaugeSeries) string { return s.Meta.Name })
	snapshot.Tables = sortedByName(snapshot.Tables, func(s TableSeries) string { return s.Meta.Name })
	return snapshot, nil
}

// fillGaugeLast makes each step report the value of its latest set second.
func fillGaugeLast(points []GaugePoint, g *Gauge, first int64, step int, end int64) {
	for i := range points {
		latest := int64(-1)
		for sec := first + int64(i*step); sec < first+int64((i+1)*step) && sec <= end; sec++ {
			slot := &g.slots[sec%HistorySeconds]
			if slot.set && slot.sec == sec && sec > latest {
				latest = sec
				points[i].Last = slot.last
			}
		}
	}
}

func summarize(base Summary, h *histogram, maximum int64) Summary {
	base.Max = maximum
	base.P50 = h.quantile(0.50, base.Count, maximum)
	base.P95 = h.quantile(0.95, base.Count, maximum)
	base.P99 = h.quantile(0.99, base.Count, maximum)
	return base
}
