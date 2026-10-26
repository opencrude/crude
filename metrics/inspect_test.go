package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/types/inspect"
)

func allInspectionPages(t *testing.T, store *inspect.Store, query InspectQuery) []inspect.Record {
	t.Helper()
	var records []inspect.Record
	for {
		page, err := Inspect(t.Context(), store, "owner", query)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		budget := query.Page.MaxBytes
		if budget == 0 {
			budget = inspect.DefaultBytes
		}
		if len(raw) > budget || page.Returned != len(page.Records) {
			t.Fatalf("invalid page bytes=%d: %+v", len(raw), page)
		}
		records = append(records, page.Records...)
		if page.Next == nil {
			if !page.Complete || len(records) != page.Total {
				t.Fatal("incomplete pagination")
			}
			break
		}
		query = InspectQuery{Page: *page.Next}
	}
	return records
}

func TestInspectSummaryDefaultDiscoversUnobservedAndUsesSameSnapshot(t *testing.T) {
	_ = fixedClock(t, time.Unix(9_000_000, 0))
	NewLatency(Meta{Name: "inspect.never", Group: "Inspection"})
	NewCounter(Meta{Name: "inspect.counter", Group: "Inspection"}).Add(42)
	var store inspect.Store
	calls := 0
	page, err := InspectWith(t.Context(), &store, "owner", InspectQuery{Page: inspect.Request{Filter: "INSPECTION", Limit: 1}}, func(s Snapshot) []inspect.Record {
		calls++
		if s.PID == 0 || s.Incarnation == "" || s.Seconds != 120 {
			t.Fatal("missing provenance")
		}
		return []inspect.Record{{Kind: "finding", ID: "fixture", Value: json.RawMessage(`{"same_snapshot":true}`)}}
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.View != "summary" || page.Records[0].Kind != "finding" {
		t.Fatalf("summary did not prioritize findings: %+v", page)
	}
	NewCounter(Meta{Name: "inspect.counter", Group: "Inspection"}).Add(100)
	records := append([]inspect.Record{}, page.Records...)
	for page.Next != nil {
		page, err = InspectWith(t.Context(), &store, "owner", InspectQuery{Page: *page.Next}, func(Snapshot) []inspect.Record { t.Fatal("continuation recollected"); return nil })
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, page.Records...)
	}
	seen := false
	for _, record := range records {
		if strings.HasSuffix(record.Kind, "_point") {
			t.Fatal("default summary contains history")
		}
		if record.ID == "inspect.never" {
			var v struct {
				Observed bool `json:"observed"`
			}
			_ = json.Unmarshal(record.Value, &v)
			if v.Observed {
				t.Fatal("unobserved marked observed")
			}
			seen = true
		}
		if record.ID == "inspect.counter" {
			var v CounterSeries
			_ = json.Unmarshal(record.Value, &v)
			if v.Window != 42 || v.Total != 42 {
				t.Fatal("snapshot changed", v)
			}
		}
	}
	if !seen || calls != 1 {
		t.Fatal("missing registration or repeated collector", seen, calls)
	}
}

func TestInspectAllTableRowsAndEveryPointWithinBytes(t *testing.T) {
	clock := fixedClock(t, time.Unix(10_000_000, 0))
	table := NewTable(Meta{Name: "inspect.dense", Group: "Dense"})
	keyPrefix := strings.Repeat("<雪\\\"", 30)
	for i := range 150 {
		table.Observe(fmt.Sprintf("%s-%03d", keyPrefix, i), time.Duration(i+1)*time.Microsecond)
	}
	*clock = clock.Add(time.Second)
	for i := range 150 {
		table.Observe(fmt.Sprintf("%s-%03d", keyPrefix, i), 2*time.Microsecond)
	}
	for _, budget := range []int{inspect.DefaultBytes, inspect.MaxBytes} {
		var store inspect.Store
		records := allInspectionPages(t, &store, InspectQuery{Page: inspect.Request{View: "tables", Filter: "inspect.dense", Limit: inspect.MaxLimit, MaxBytes: budget}, Seconds: 2, Points: 2})
		rows, points := 0, 0
		for _, record := range records {
			switch record.Kind {
			case "table_row":
				rows++
				var row TableRow
				_ = json.Unmarshal(record.Value, &row)
				if row.Count != 2 {
					t.Fatal("row lost observations")
				}
			case "table_point":
				points++
			}
		}
		if rows != 150 || points != 300 {
			t.Fatalf("silent source truncation: rows=%d points=%d", rows, points)
		}
	}
	dashboard := findTable(t, Take(Query{Seconds: 2, Points: 2}), "inspect.dense")
	if len(dashboard.Rows) != 24 || dashboard.RowsAvailable != 150 || dashboard.RowsOmitted != 126 {
		t.Fatalf("dashboard cap/coverage %+v", dashboard)
	}
	if len(dashboard.DiagnosticRows) != 150 {
		t.Fatal("rare diagnostic owners omitted")
	}
	for _, row := range dashboard.DiagnosticRows {
		if len(row.Points) != 0 {
			t.Fatal("diagnostic summaries include display points")
		}
	}
	for i, row := range dashboard.Rows {
		if (len(row.Points) > 0) != (i < 8) {
			t.Fatal("dashboard point behavior changed")
		}
	}
}

func TestInspectStrictSelectorsAndContinuationBinding(t *testing.T) {
	_ = fixedClock(t, time.Unix(11_000_000, 0))
	NewCounter(Meta{Name: "inspect.binding"}).Inc()
	var store inspect.Store
	for _, query := range []InspectQuery{{Seconds: -1}, {Seconds: 301}, {Points: -1}, {Points: 61}, {Page: inspect.Request{View: "bogus"}}, {Page: inspect.Request{View: "history"}}, {Page: inspect.Request{Offset: 1}}, {Page: inspect.Request{Limit: 65}}} {
		if _, err := Inspect(t.Context(), &store, "owner", query); err == nil {
			t.Fatalf("invalid query accepted: %+v", query)
		}
	}
	page, err := Inspect(t.Context(), &store, "owner", InspectQuery{Page: inspect.Request{View: "history", Filter: "inspect.binding", Limit: 1}, Seconds: 10, Points: 2})
	if err != nil || page.Next == nil {
		t.Fatal(page, err)
	}
	for _, query := range []InspectQuery{{Page: *page.Next, Seconds: 20}, {Page: *page.Next, Points: 3}} {
		if _, err := Inspect(t.Context(), &store, "owner", query); err == nil {
			t.Fatal("changed window accepted")
		}
	}
	if _, err := Inspect(t.Context(), &store, "other", InspectQuery{Page: *page.Next}); err == nil {
		t.Fatal("foreign continuation accepted")
	}
	if _, err := Inspect(t.Context(), &store, "owner", InspectQuery{Page: inspect.Request{SnapshotID: "expired"}}); err == nil {
		t.Fatal("unknown snapshot accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Inspect(ctx, &store, "owner", InspectQuery{}); err == nil {
		t.Fatal("cancelled read accepted")
	}
}

func TestExactWindowEdgeAndUIBusyExcludeBlocking(t *testing.T) {
	clock := fixedClock(t, time.Unix(12_000_000, 0))
	c := NewCounter(Meta{Name: "inspect.edge"})
	c.Add(900)
	*clock = clock.Add(time.Second)
	c.Add(1)
	*clock = clock.Add(4 * time.Second)
	c.Add(2)
	NewLatency(Meta{Name: "ui.update"}).Observe(time.Second)
	NewLatency(Meta{Name: "ui.view"}).Observe(500 * time.Millisecond)
	NewLatency(Meta{Name: "ui.blocking"}).Observe(time.Second)
	s, err := takeContext(t.Context(), Query{Seconds: 5, Points: 2}, snapshotOptions{all: true})
	if err != nil {
		t.Fatal(err)
	}
	series := findCounter(t, s, "inspect.edge")
	if series.Window != 3 || len(series.Points) != 2 || series.Points[0] != 1 || series.Points[1] != 2 || s.First != s.End-4 {
		t.Fatalf("window escaped requested interval %+v", series)
	}
	busy, ok := UIBusy(s)
	if !ok || busy.Window != 30 || busy.Recent != 30 || len(busy.Points) != 2 || busy.Points[1] != 75 {
		t.Fatalf("busy double counted or edge width wrong %+v", busy)
	}
}

func TestDiagnosticFactsDoNotDependOnDeliveredPoints(t *testing.T) {
	clock := fixedClock(t, time.Unix(16_000_000, 0))
	NewLatency(Meta{Name: "ui.update"}).Observe(800 * time.Millisecond)
	g := NewGauge(Meta{Name: "runtime.goroutines"}, nil)
	g.Set(100)
	*clock = clock.Add(10 * time.Second)
	g.Set(1400)
	one, err := takeContext(t.Context(), Query{Seconds: 15, Points: 1}, snapshotOptions{all: true})
	if err != nil {
		t.Fatal(err)
	}
	many, err := takeContext(t.Context(), Query{Seconds: 15, Points: 15}, snapshotOptions{all: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []Snapshot{one, many} {
		busy, _ := UIBusy(s)
		if busy.Peak != 80 {
			t.Fatal("chart folding hid a one-second busy peak", busy)
		}
		g := findGauge(t, s, "runtime.goroutines")
		if !g.WindowObserved || g.WindowFirst != 100 || g.WindowLast != 1400 {
			t.Fatalf("chart folding hid gauge growth: %+v", g)
		}
	}
}

func TestGaugeConcurrentAddsAndNegativePeak(t *testing.T) {
	_ = fixedClock(t, time.Unix(13_000_000, 0))
	g := NewGauge(Meta{Name: "inspect.add"}, nil)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 1000 {
				g.Add(1)
			}
		})
	}
	wg.Wait()
	s, _ := takeContext(t.Context(), Query{Seconds: 1}, snapshotOptions{all: true})
	if findGauge(t, s, "inspect.add").Current != 8000 {
		t.Fatal("lost concurrent gauge deltas")
	}
	g.Set(-10)
	negative := NewGauge(Meta{Name: "inspect.negative"}, nil)
	negative.Set(-42)
	s, _ = takeContext(t.Context(), Query{Seconds: 1}, snapshotOptions{all: true})
	v := findGauge(t, s, "inspect.negative")
	if v.Peak != -42 || v.Points[0].Max != -42 || v.Points[0].Last != -42 || !v.Observed {
		t.Fatalf("negative gauge became zero %+v", v)
	}
}

func TestEventsExposeRingOverwriteAndOwnerFilter(t *testing.T) {
	_ = fixedClock(t, time.Unix(14_000_000, 0))
	for i := range 270 {
		Note("Inspection", "event", fmt.Sprint(i), "detail", "warn")
	}
	var store inspect.Store
	page, err := Inspect(t.Context(), &store, "owner", InspectQuery{Page: inspect.Request{View: "events", Filter: "iNsPeCtIoN"}})
	if err != nil {
		t.Fatal(err)
	}
	var source inspectionSource
	if err := json.Unmarshal(page.Source, &source); err != nil {
		t.Fatal(err)
	}
	if source.EventsOverwritten != 14 || page.Total != 256 {
		t.Fatal("event retention hidden", source.EventsOverwritten, page.Total)
	}
	records := allInspectionPages(t, &store, InspectQuery{Page: inspect.Request{View: "events", Filter: strings.ToUpper(processName())}})
	if len(records) != 256 {
		t.Fatal("owner filter failed")
	}
}

func TestInspectMaximumHistoryPointsAndIndivisibleRecord(t *testing.T) {
	_ = fixedClock(t, time.Unix(15_000_000, 0))
	table := NewTable(Meta{Name: "inspect.maximum"})
	for i := range 40 {
		table.Observe(fmt.Sprint(i), time.Microsecond)
	}
	var store inspect.Store
	records := allInspectionPages(t, &store, InspectQuery{Page: inspect.Request{View: "tables", Filter: "inspect.maximum", Limit: 64, MaxBytes: inspect.MaxBytes}, Seconds: 300, Points: 60})
	points := 0
	for _, r := range records {
		if r.Kind == "table_point" {
			points++
		}
	}
	if points != 2400 {
		t.Fatal("maximum history points missing", points)
	}
	oversized := NewCounter(Meta{Name: "inspect.oversized", Help: strings.Repeat("<雪", 10000)})
	oversized.Inc()
	t.Cleanup(func() { reg.mu.Lock(); oversized.meta.Help = ""; reg.mu.Unlock() })
	_, err := Inspect(t.Context(), &store, "owner", InspectQuery{Page: inspect.Request{View: "series", Filter: "inspect.oversized"}})
	if err == nil || !strings.Contains(err.Error(), "record") {
		t.Fatal("indivisible oversize record missing actionable failure", err)
	}
}
