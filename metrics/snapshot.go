package metrics

import (
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
	Meta    Meta         `json:"meta"`
	Points  []GaugePoint `json:"points"`
	Current int64        `json:"current"`
	Peak    int64        `json:"peak"`
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
	Meta Meta       `json:"meta"`
	Rows []TableRow `json:"rows"`
}

// Snapshot is a self-describing, JSON-serializable view of one process.
type Snapshot struct {
	Process   string          `json:"process"`
	PID       int             `json:"pid"`
	Started   time.Time       `json:"started"`
	Taken     time.Time       `json:"taken"`
	End       int64           `json:"end"` // unix second of the last point's last second
	Step      int             `json:"step"`
	Latencies []LatencySeries `json:"latencies,omitempty"`
	Counters  []CounterSeries `json:"counters,omitempty"`
	Gauges    []GaugeSeries   `json:"gauges,omitempty"`
	Tables    []TableSeries   `json:"tables,omitempty"`
	Events    []Event         `json:"events,omitempty"`
}

const recentSeconds = 5
const tableRowLimit = 24
const tablePointRows = 8

// Take reads every registered series for the query window. It is safe to
// call from any goroutine and holds each series lock only while copying it.
func Take(q Query) Snapshot {
	_, step, points := q.normalized()
	at := now()
	end := at.Unix()
	first := end - int64(points*step) + 1
	snapshot := Snapshot{Process: processName(), PID: os.Getpid(), Started: processStart, Taken: at, End: end, Step: step}

	reg.mu.Lock()
	latencies := slices.Clone(reg.latencies)
	counters := slices.Clone(reg.counters)
	gauges := slices.Clone(reg.gauges)
	tables := slices.Clone(reg.tables)
	reg.mu.Unlock()

	index := func(sec int64) int { return int((sec - first) / int64(step)) }
	for _, l := range latencies {
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
		if series.Total == 0 {
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
	for _, c := range counters {
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
		if series.Total == 0 {
			continue
		}
		snapshot.Counters = append(snapshot.Counters, series)
	}
	for _, g := range gauges {
		series := GaugeSeries{Meta: g.meta, Points: make([]GaugePoint, points)}
		series.Meta.Group = groupOf(g.meta)
		any := false
		g.mu.Lock()
		series.Current = g.current
		for i := range g.slots {
			slot := &g.slots[i]
			if !slot.set || slot.sec < first || slot.sec > end {
				continue
			}
			any = true
			p := &series.Points[index(slot.sec)]
			p.Max = max(p.Max, slot.max)
			p.Set = true
			series.Peak = max(series.Peak, slot.max)
		}
		g.mu.Unlock()
		if !any {
			continue
		}
		fillGaugeLast(series.Points, g, first, step)
		snapshot.Gauges = append(snapshot.Gauges, series)
	}
	for _, t := range tables {
		series := TableSeries{Meta: t.meta}
		series.Meta.Group = groupOf(t.meta)
		rows := make([]TableRow, 0)
		rowPoints := make(map[string][]int64)
		t.mu.Lock()
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
		if len(rows) == 0 {
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
		if len(rows) > tableRowLimit {
			rows = rows[:tableRowLimit]
		}
		for i := range rows[:min(len(rows), tablePointRows)] {
			rows[i].Points = rowPoints[rows[i].Key]
		}
		series.Rows = rows
		snapshot.Tables = append(snapshot.Tables, series)
	}
	snapshot.Events = eventsSince(time.Unix(first, 0))
	snapshot.Latencies = sortedByName(snapshot.Latencies, func(s LatencySeries) string { return s.Meta.Name })
	snapshot.Counters = sortedByName(snapshot.Counters, func(s CounterSeries) string { return s.Meta.Name })
	snapshot.Gauges = sortedByName(snapshot.Gauges, func(s GaugeSeries) string { return s.Meta.Name })
	snapshot.Tables = sortedByName(snapshot.Tables, func(s TableSeries) string { return s.Meta.Name })
	return snapshot
}

// fillGaugeLast makes each step report the value of its latest set second.
func fillGaugeLast(points []GaugePoint, g *Gauge, first int64, step int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i := range points {
		latest := int64(-1)
		for sec := first + int64(i*step); sec < first+int64((i+1)*step); sec++ {
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
