package metrics

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

func fixedClock(t *testing.T, start time.Time) *time.Time {
	t.Helper()
	current := start
	restore := SetClockForTest(func() time.Time { return current })
	t.Cleanup(restore)
	t.Cleanup(ResetForTest)
	ResetForTest()
	return &current
}

func TestLatencyWindowPercentilesAndRollover(t *testing.T) {
	clock := fixedClock(t, time.Unix(1_000_000, 0))
	l := NewLatency(Meta{Name: "test.latency", Title: "Test", Crit: 50_000})
	for i := 0; i < 99; i++ {
		l.Observe(200 * time.Microsecond)
	}
	l.ObserveLabeled(80*time.Millisecond, "slow-message")
	*clock = clock.Add(HistorySeconds * time.Second) // same ring slot, new second
	l.Observe(3 * time.Millisecond)

	snapshot := Take(Query{Seconds: HistorySeconds, Points: HistorySeconds})
	series := findLatency(t, snapshot, "test.latency")
	if series.Window.Count != 1 || series.Window.Max != 3_000 {
		t.Fatalf("rolled slot must forget the old second: %+v", series.Window)
	}
	*clock = time.Unix(1_000_000, 0)
	snapshot = Take(Query{Seconds: 10, Points: 10})
	series = findLatency(t, snapshot, "test.latency")
	if series.Window.Count != 0 {
		t.Fatalf("old second was overwritten and must not reappear: %+v", series.Window)
	}
}

func TestLatencySummaryAndStallEvent(t *testing.T) {
	_ = fixedClock(t, time.Unix(2_000_000, 0))
	l := NewLatency(Meta{Name: "test.summary", Title: "Summary", Crit: 50_000})
	for i := 0; i < 99; i++ {
		l.Observe(200 * time.Microsecond)
	}
	l.ObserveLabeled(80*time.Millisecond, "slow-message")
	series := findLatency(t, Take(Query{Seconds: 60, Points: 60}), "test.summary")
	if series.Window.Count != 100 || series.Window.Max != 80_000 {
		t.Fatalf("summary = %+v", series.Window)
	}
	if series.Window.P50 != 250 || series.Window.P95 != 250 {
		t.Fatalf("percentiles should sit in the 250µs bucket: %+v", series.Window)
	}
	if series.Window.P99 != 250 {
		t.Fatalf("p99 of 100 samples is the 99th (fast) sample: %+v", series.Window)
	}
	events := Take(Query{Seconds: 60}).Events
	if len(events) != 1 || events[0].Label != "slow-message" || events[0].Micros != 80_000 {
		t.Fatalf("events = %+v", events)
	}
}

func TestSnapshotStepsFoldSeconds(t *testing.T) {
	clock := fixedClock(t, time.Unix(3_000_000, 0))
	c := NewCounter(Meta{Name: "test.counter", Title: "Counter"})
	for i := 0; i < 6; i++ {
		c.Add(uint64(i + 1))
		*clock = clock.Add(time.Second)
	}
	*clock = clock.Add(-time.Second)
	series := findCounter(t, Take(Query{Seconds: 6, Points: 3}), "test.counter")
	if len(series.Points) != 3 || series.Points[0] != 3 || series.Points[1] != 7 || series.Points[2] != 11 {
		t.Fatalf("points = %v", series.Points)
	}
	if series.Window != 21 || series.Total != 21 {
		t.Fatalf("window=%d total=%d", series.Window, series.Total)
	}
}

func TestGaugeStepReportsLatestSecond(t *testing.T) {
	clock := fixedClock(t, time.Unix(4_000_000, 0))
	g := NewGauge(Meta{Name: "test.gauge", Title: "Gauge"}, nil)
	g.Set(9)
	*clock = clock.Add(time.Second)
	g.Set(4)
	series := findGauge(t, Take(Query{Seconds: 2, Points: 1}), "test.gauge")
	if series.Points[0].Last != 4 || series.Points[0].Max != 9 || series.Current != 4 || series.Peak != 9 {
		t.Fatalf("gauge = %+v", series)
	}
}

func TestTableRanksByTimeAndFoldsExcessKeys(t *testing.T) {
	_ = fixedClock(t, time.Unix(5_000_000, 0))
	table := NewTable(Meta{Name: "test.table", Title: "Table"})
	table.Observe("cheap", time.Millisecond)
	table.Observe("cheap", time.Millisecond)
	table.Observe("costly", 9*time.Millisecond)
	for i := 0; i < maxTableKeys+5; i++ {
		table.Observe(fmt.Sprintf("key-%d", i), time.Microsecond)
	}
	series := findTable(t, Take(Query{Seconds: 10}), "test.table")
	if series.Rows[0].Key != "costly" || series.Rows[1].Key != "cheap" || series.Rows[1].Count != 2 {
		t.Fatalf("rows = %+v", series.Rows[:2])
	}
	table.mu.Lock()
	keys := len(table.rows)
	_, folded := table.rows["other"]
	table.mu.Unlock()
	if keys > maxTableKeys+1 || !folded {
		t.Fatalf("keys=%d folded=%v", keys, folded)
	}
}

func TestSnapshotRoundTripsAsJSON(t *testing.T) {
	_ = fixedClock(t, time.Unix(6_000_000, 0))
	NewLatency(Meta{Name: "test.json", Title: "JSON"}).Observe(time.Millisecond)
	raw, err := json.Marshal(Take(Query{Seconds: 30, Points: 30}))
	if err != nil {
		t.Fatal(err)
	}
	var decoded Snapshot
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if findLatency(t, decoded, "test.json").Window.Count != 1 {
		t.Fatal("decoded snapshot lost the observation")
	}
}

func TestBlockingRecordsOnlyOnUIGoroutine(t *testing.T) {
	_ = fixedClock(t, time.Unix(7_000_000, 0))
	ResetUIGoroutineForTest()
	t.Cleanup(ResetUIGoroutineForTest)
	MarkUIGoroutine()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); Blocking("core request", "background", time.Millisecond) }()
	wg.Wait()
	Blocking("core request", "settings.load", 12*time.Millisecond)
	events := Take(Query{Seconds: 10}).Events
	if len(events) != 1 || events[0].Label != "settings.load" {
		t.Fatalf("events = %+v", events)
	}
}

func TestConcurrentRecordingIsRaceFree(t *testing.T) {
	_ = fixedClock(t, time.Unix(8_000_000, 0))
	l := NewLatency(Meta{Name: "test.concurrent", Title: "Concurrent"})
	table := NewTable(Meta{Name: "test.concurrent.table", Title: "Concurrent"})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				l.Observe(time.Duration(j) * time.Microsecond)
				table.Observe("k", time.Microsecond)
				_ = Take(Query{Seconds: 5})
			}
		}()
	}
	wg.Wait()
	if got := findLatency(t, Take(Query{Seconds: 5}), "test.concurrent").Window.Count; got != 4000 {
		t.Fatalf("count = %d", got)
	}
}

func BenchmarkLatencyObserve(b *testing.B) {
	l := NewLatency(Meta{Name: "bench.latency", Title: "Bench"})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		l.Observe(time.Duration(i%5000) * time.Microsecond)
	}
}

func BenchmarkTableObserve(b *testing.B) {
	table := NewTable(Meta{Name: "bench.table", Title: "Bench"})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		table.Observe("tea.KeyPressMsg", time.Microsecond)
	}
}

func findLatency(t *testing.T, s Snapshot, name string) LatencySeries {
	t.Helper()
	for _, series := range s.Latencies {
		if series.Meta.Name == name {
			return series
		}
	}
	t.Fatalf("latency %q missing", name)
	return LatencySeries{}
}

func findCounter(t *testing.T, s Snapshot, name string) CounterSeries {
	t.Helper()
	for _, series := range s.Counters {
		if series.Meta.Name == name {
			return series
		}
	}
	t.Fatalf("counter %q missing", name)
	return CounterSeries{}
}

func findGauge(t *testing.T, s Snapshot, name string) GaugeSeries {
	t.Helper()
	for _, series := range s.Gauges {
		if series.Meta.Name == name {
			return series
		}
	}
	t.Fatalf("gauge %q missing", name)
	return GaugeSeries{}
}

func findTable(t *testing.T, s Snapshot, name string) TableSeries {
	t.Helper()
	for _, series := range s.Tables {
		if series.Meta.Name == name {
			return series
		}
	}
	t.Fatalf("table %q missing", name)
	return TableSeries{}
}
