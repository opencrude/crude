package perfdata

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/metrics"
	"crude/types/inspect"
)

func TestArchiveSurvivesReopenAndComparesExactBuilds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "space # evidence.db")
	var pages inspect.Store
	ctx := t.Context()
	missing, err := InspectArchive(ctx, &pages, "owner", path, "core", ArchiveQuery{})
	if err != nil || missing.Total != 0 || !strings.Contains(string(missing.Source), `"available":false`) {
		t.Fatalf("an uncreated archive is not a healthy measurement: %+v %v", missing, err)
	}
	db, err := openArchive(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	at := time.Now().Truncate(time.Hour).Add(-time.Hour).Add(time.Second)
	clock := at
	restore := metrics.SetClockForTest(func() time.Time { return clock })
	defer restore()
	measure := metrics.NewLatency(metrics.Meta{Name: "archive.fixture.latency", Stall: 50})
	write := func(src ArchiveSource, begin, until time.Time) metrics.ArchiveWindow {
		t.Helper()
		obs := metrics.CaptureArchiveWindow(begin.Unix(), until.Unix())
		if err := storeArchiveWindow(ctx, db, src, obs); err != nil {
			t.Fatal(err)
		}
		return obs
	}
	old := ArchiveSource{ArchiveProcess: metrics.ArchiveProcess{Incarnation: "o"}, Role: "core", Build: "first-dev", Cohort: "before", Version: "dev"}
	newer := ArchiveSource{ArchiveProcess: metrics.ArchiveProcess{Incarnation: "n"}, Role: "core", Build: "second-dev", Cohort: "after", Version: "dev"}
	for range 20 {
		measure.Observe(90 * time.Microsecond)
	}
	first := write(old, at, at.Add(time.Second))
	if err := storeArchiveWindow(ctx, db, old, first); err != nil {
		t.Fatal(err)
	} // Retry must not double count.
	clock = at.Add(2 * time.Second)
	measure.ObserveLabeled(700*time.Microsecond, "private-path-not-for-disk")
	write(old, clock, clock.Add(time.Second))
	clock = at.Add(3 * time.Second)
	measure.Observe(30 * time.Microsecond)
	write(newer, clock, clock.Add(time.Second))
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	query := ArchiveQuery{Page: inspect.Request{View: "compare", Filter: "archive.fixture.latency", Limit: 1}, Build: old.Build, Against: newer.Build}
	p, err := InspectArchive(ctx, &pages, "owner", path, "core", query)
	if err != nil || p.Next == nil || p.Total != 2 {
		t.Fatalf("no build comparison: %+v %v", p, err)
	}
	var b struct {
		Measurement archiveMeasurement `json:"measurement"`
		Coverage    int                `json:"observed_process_seconds"`
	}
	if err := json.Unmarshal(p.Records[0].Value, &b); err != nil || b.Coverage != 2 || b.Measurement.Count != 21 || b.Measurement.P95 != 100 || b.Measurement.Max != 700 {
		t.Fatalf("merged observations or coverage are wrong: %+v %v", b, err)
	}
	second, err := InspectArchive(ctx, &pages, "owner", path, "core", ArchiveQuery{Page: *p.Next})
	if err != nil || second.Records[0].ID != newer.Build {
		t.Fatalf("second build lost: %+v %v", second, err)
	}
	if _, err = InspectArchive(ctx, &pages, "other", path, "core", ArchiveQuery{Page: *p.Next}); err == nil {
		t.Fatal("a foreign reader continued the snapshot")
	}
	if _, err = InspectArchive(ctx, &pages, "owner", path, "core", ArchiveQuery{Page: *p.Next, Build: "different"}); err == nil {
		t.Fatal("changed selection continued the snapshot")
	}
	stalls, err := InspectArchive(ctx, &pages, "owner", path, "core", ArchiveQuery{Page: inspect.Request{View: "stalls", Filter: "archive.fixture.latency"}})
	if err != nil || stalls.Total != 17 || strings.Contains(string(stalls.Records[0].Value), "private-path") {
		t.Fatalf("stalls missing, duplicated, or private label escaped: %+v %v", stalls, err)
	}
	builds, err := InspectArchive(ctx, &pages, "owner", path, "core", ArchiveQuery{})
	if err != nil || builds.Total != 2 {
		t.Fatalf("dev builds collapsed: %+v %v", builds, err)
	}
}
