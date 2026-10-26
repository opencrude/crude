package perfdata

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/metrics"
)

func latency(name string, n uint64, p95 int64) metrics.LatencySeries {
	return metrics.LatencySeries{Meta: metrics.Meta{Name: name, Warn: 10_000, Crit: 100_000}, Total: n,
		Window: metrics.Summary{Count: n, P95: p95, Max: p95}, Recent: metrics.Summary{Count: n, P95: p95, Max: p95}}
}

func TestInspectionUsesCanonicalFindingsForExactRole(t *testing.T) {
	for _, role := range []string{"shell", "core"} {
		t.Run(role, func(t *testing.T) {
			name := "ui.input"
			if role == "core" {
				name = "core.actor"
			}
			s := metrics.Snapshot{Taken: time.Now(), Seconds: 120, Step: 1, Latencies: []metrics.LatencySeries{latency(name, 4, 200_000)}}
			d := Data{Shell: s}
			if role == "core" {
				d = Data{Core: &s}
			}
			want := Diagnose(d)
			var got []Finding
			for _, r := range InspectionRecords(s, role) {
				if r.Kind == "finding" {
					var f Finding
					if err := json.Unmarshal(r.Value, &f); err != nil {
						t.Fatal(err)
					}
					got = append(got, f)
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("inspection=%+v canonical=%+v", got, want)
			}
			if len(got) == 0 || got[0].Severity != SeverityCrit {
				t.Fatalf("lost primary latency finding: %+v", got)
			}
			if role == "core" {
				for _, f := range got {
					if strings.Contains(f.Title, "Shell") || strings.Contains(f.Title, "Input latency") {
						t.Fatalf("core misattributed to UI: %+v", f)
					}
				}
			}
		})
	}
}

func TestUnobservedAndMissingEvidenceIsUnknown(t *testing.T) {
	for _, d := range []Data{{}, {CoreErr: "offline"}, {Shell: metrics.Snapshot{Taken: time.Now()}}, {Core: &metrics.Snapshot{Taken: time.Now(), Latencies: []metrics.LatencySeries{latency("core.actor", 0, 0)}}}} {
		findings := Diagnose(d)
		sev, verdict := Verdict(findings)
		if sev != SeverityUnknown || !strings.Contains(verdict, "Unknown") {
			t.Fatalf("missing evidence claimed health: %+v => %v %q", d, sev, verdict)
		}
	}
}

func TestCoreUnavailableDoesNotUseShellCoreSeries(t *testing.T) {
	d := Data{CoreErr: "core disconnected", Shell: metrics.Snapshot{Taken: time.Now(), Latencies: []metrics.LatencySeries{latency("core.actor", 1, 200_000)}}}
	for _, f := range Diagnose(d) {
		if strings.Contains(f.Title, "Workspace actor") {
			t.Fatalf("shell data used as missing core evidence: %+v", f)
		}
	}
}

func TestCanonicalRulesPreserveRankAndThresholds(t *testing.T) {
	shell := metrics.Snapshot{Taken: time.Now(), Seconds: 5, Step: 1, Latencies: []metrics.LatencySeries{latency("ui.input", 3, 15_000)}, Counters: []metrics.CounterSeries{{Meta: metrics.Meta{Name: "ui.blocking"}, Window: 1}}, Events: []metrics.Event{{Series: "ui.view", Micros: 500_000, Label: "large paint"}}}
	core := metrics.Snapshot{Taken: shell.Taken, Latencies: []metrics.LatencySeries{latency("core.actor", 1, 15_000)}, Tables: []metrics.TableSeries{{Meta: metrics.Meta{Name: "git.commands", Warn: 10_000, Crit: 100_000}, Rows: []metrics.TableRow{{Key: "status", Count: 1, Sum: 15_000, Max: 15_000}}}}}
	findings := Diagnose(Data{Shell: shell, Core: &core})
	titles := []string{}
	for i, f := range findings {
		titles = append(titles, f.Title)
		if i > 0 && findings[i-1].Severity < f.Severity {
			t.Fatalf("unordered findings: %+v", findings)
		}
	}
	for _, want := range []string{"blocking call(s)", "UI froze", "Input latency 15ms", "Workspace actor is slow", "Slow Git: status"} {
		if !strings.Contains(strings.Join(titles, "\n"), want) {
			t.Fatalf("missing %q in %v", want, titles)
		}
	}
}

func TestBusyUsesMetricsProjectionAndNotProcessCPU(t *testing.T) {
	s := metrics.Snapshot{Taken: time.Now(), Seconds: 2, Step: 1, UIBusyPeak: 80, Latencies: []metrics.LatencySeries{
		{Meta: metrics.Meta{Name: "ui.update"}, Total: 2, Window: metrics.Summary{Count: 2, Sum: 800_000}, Recent: metrics.Summary{Count: 2, Sum: 800_000}, Points: []metrics.LatencyPoint{{Sum: 800_000}, {}}},
		{Meta: metrics.Meta{Name: "ui.blocking"}, Total: 1, Window: metrics.Summary{Count: 1, Sum: 800_000}},
	}, Gauges: []metrics.GaugeSeries{{Meta: metrics.Meta{Name: "runtime.cpu"}, Current: 99}}}
	d := Data{Shell: s}
	busy, ok := metrics.UIBusy(s)
	points, gotOK := d.BusySeries()
	if !ok || !gotOK || !reflect.DeepEqual(points, busy.Points) || d.RecentBusy() != busy.Recent || d.RecentBusy() != 40 {
		t.Fatalf("busy differs from metrics: %v %v / %+v", points, d.RecentBusy(), busy)
	}
	f := busyFinding(d)
	if f.Severity != SeverityCrit || !strings.Contains(f.Title, "80%") {
		t.Fatalf("peak busy finding=%+v", f)
	}
}

func TestDiagnosisIncludesRareOwnersOutsideDisplayAndGeometry(t *testing.T) {
	rows := make([]metrics.TableRow, 25)
	for i := range 24 {
		rows[i] = metrics.TableRow{Key: "frequent", Count: 10000, Sum: 100000, Max: 10}
	}
	rows[24] = metrics.TableRow{Key: "rare-stalled-owner", Count: 1, Sum: 90000, Max: 90000}
	table := metrics.TableSeries{Meta: metrics.Meta{Name: "git.commands", Warn: 10000, Crit: 50000}, Rows: rows[:24], RowsAvailable: 25, RowsOmitted: 1, DiagnosticRows: rows}
	core := metrics.Snapshot{Taken: time.Now(), Tables: []metrics.TableSeries{table}, Gauges: []metrics.GaugeSeries{{Meta: metrics.Meta{Name: "runtime.goroutines"}, WindowObserved: true, WindowFirst: 100, WindowLast: 1400}}}
	f := Diagnose(Data{Core: &core})
	titles := ""
	for _, finding := range f {
		titles += finding.Title + "\n"
	}
	if !strings.Contains(titles, "rare-stalled-owner") || !strings.Contains(titles, "100 → 1400") {
		t.Fatal("diagnosis depended on top-row/chart geometry", titles)
	}
}
