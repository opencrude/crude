package metrics

import (
	"os"
	"slices"
	"sort"
	"time"
)

// Rollup keeps the original histogram counts, not calculated percentiles.
// Counts are wide enough to merge many processes and hours.
type Rollup struct {
	Kind    Kind     `json:"kind"`
	Unit    string   `json:"unit"`
	Count   uint64   `json:"count"`
	Sum     int64    `json:"sum"`
	Max     int64    `json:"max"`
	Buckets []uint64 `json:"buckets,omitempty"`
}

func BucketUpperMicros() []int64 { return slices.Clone(bucketBounds[:]) }

func (r Rollup) Percentile(bounds []int64, fraction float64) int64 {
	if r.Kind != KindLatency || len(bounds)+1 != len(r.Buckets) {
		return 0
	}
	return latencyQuantile(r.Buckets, bounds, fraction, r.Count, r.Max)
}

// Merge combines only samples of the same measurement and bucket schema.
func (r *Rollup) Merge(next Rollup) bool {
	if r.Kind == "" {
		r.Kind, r.Unit = next.Kind, next.Unit
		r.Buckets = make([]uint64, len(next.Buckets))
	}
	if r.Kind != next.Kind || r.Unit != next.Unit || len(r.Buckets) != len(next.Buckets) {
		return false
	}
	if r.Count == 0 || next.Max > r.Max {
		r.Max = next.Max
	}
	r.Count += next.Count
	r.Sum += next.Sum
	for i, count := range next.Buckets {
		r.Buckets[i] += count
	}
	return true
}

type ArchiveWindow struct {
	From, Until          int64 // half-open Unix seconds, already closed
	Series               map[string]Rollup
	Stalls               []ArchiveStall
	StallsOmitted        int
	EventRingOverwritten uint64
}

type ArchiveStall struct {
	At       time.Time `json:"at"`
	Series   string    `json:"series"`
	Micros   int64     `json:"us"`
	Severity string    `json:"severity"`
	// Label names the cause for series with code-defined labels: a message
	// type, request method, actor step or key chord (Meta.SaveLabels).
	Label               string        `json:"label,omitempty"`
	Stack               []string      `json:"stack,omitempty"` // code locations only
	Context             *StallContext `json:"stall_context,omitempty"`
	WatchdogSampled     bool          `json:"watchdog_sampled,omitempty"`
	WatchdogDelayMicros int64         `json:"watchdog_delay_us,omitempty"`
}

type ArchiveProcess struct {
	Incarnation string    `json:"incarnation"`
	Started     time.Time `json:"started"`
	PID         int       `json:"pid"`
	Hostname    string    `json:"hostname,omitempty"`
	HostBootID  string    `json:"host_boot_id,omitempty"`
}

func ArchiveProcessIdentity() ArchiveProcess {
	host, boot, _ := hostProvenance()
	return ArchiveProcess{Incarnation: processIncarnation, Started: processStart, PID: os.Getpid(), Hostname: host, HostBootID: boot}
}

// CaptureArchiveWindow reads existing slots only. It never samples resources
// or keyed table rows. A stall keeps its stack, and its label only when the
// series marks its labels as code-defined (Meta.SaveLabels). An event's
// free-form detail, which can hold paths or errors, is never saved.
func CaptureArchiveWindow(from, until int64) ArchiveWindow {
	w := ArchiveWindow{From: from, Until: until, Series: make(map[string]Rollup)}
	reg.mu.Lock()
	latencies, counters, gauges := slices.Clone(reg.latencies), slices.Clone(reg.counters), slices.Clone(reg.gauges)
	labeled := make(map[string]bool)
	for _, l := range reg.latencies {
		labeled[l.meta.Name] = l.meta.SaveLabels
	}
	for _, t := range reg.tables {
		labeled[t.meta.Name] = t.meta.SaveLabels
	}
	reg.mu.Unlock()
	for _, l := range latencies {
		var r Rollup
		l.mu.Lock()
		for sec := from; sec < until; sec++ {
			s := &l.slots[sec%HistorySeconds]
			if s.sec != sec || s.count == 0 {
				continue
			}
			if r.Buckets == nil {
				r.Buckets = make([]uint64, bucketCount)
			}
			r.Count += uint64(s.count)
			r.Sum += s.sum
			r.Max = max(r.Max, s.max)
			for i, count := range s.buckets {
				r.Buckets[i] += uint64(count)
			}
		}
		l.mu.Unlock()
		if r.Count > 0 {
			r.Kind, r.Unit = KindLatency, "µs"
			w.Series[l.meta.Name] = r
		}
	}
	for _, c := range counters {
		var r Rollup
		c.mu.Lock()
		for sec := from; sec < until; sec++ {
			s := &c.slots[sec%HistorySeconds]
			if s.sec == sec && s.n > 0 {
				r.Count++
				r.Sum += int64(s.n)
				r.Max = max(r.Max, int64(s.n))
			}
		}
		c.mu.Unlock()
		if r.Count > 0 {
			r.Kind, r.Unit = KindCounter, c.meta.Unit
			w.Series[c.meta.Name] = r
		}
	}
	for _, g := range gauges {
		var r Rollup
		g.mu.Lock()
		for sec := from; sec < until; sec++ {
			s := &g.slots[sec%HistorySeconds]
			if s.sec == sec && s.set {
				if r.Count == 0 || r.Max < s.max {
					r.Max = s.max
				}
				r.Count++
				r.Sum += s.last
			}
		}
		g.mu.Unlock()
		if r.Count > 0 {
			r.Kind, r.Unit = KindGauge, g.meta.Unit
			w.Series[g.meta.Name] = r
		}
	}
	events.mu.Lock()
	w.EventRingOverwritten = events.dropped
	for i := range events.n {
		event := events.ring[(events.next-events.n+i+maxEvents)%maxEvents]
		if sec := event.At.Unix(); sec >= from && sec < until && event.Series != "" {
			stall := ArchiveStall{At: event.At, Series: event.Series, Micros: event.Micros, Severity: event.Severity, Stack: event.Stack,
				Context: event.Context, WatchdogSampled: event.WatchdogSampled, WatchdogDelayMicros: event.WatchdogDelayMicros}
			if labeled[event.Series] {
				stall.Label = event.Label
			}
			w.Stalls = append(w.Stalls, stall)
		}
	}
	events.mu.Unlock()
	const maxStalls = 16
	if len(w.Stalls) > maxStalls {
		sort.SliceStable(w.Stalls, func(i, j int) bool {
			a, b := w.Stalls[i], w.Stalls[j]
			if (len(a.Stack) > 0) != (len(b.Stack) > 0) {
				return len(a.Stack) > 0
			}
			return a.Micros > b.Micros
		})
		w.StallsOmitted = len(w.Stalls) - maxStalls
		w.Stalls = w.Stalls[:maxStalls]
	}
	return w
}
