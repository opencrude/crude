package perfdata

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/internal/metrics"
)

// A saved stall keeps its label only for series whose labels are code-defined
// categories; a label that can name a file never reaches the disk.
func TestArchiveSavesOnlyCodeDefinedStallLabels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "performance.db")
	ctx := t.Context()
	db, err := openArchive(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	at := time.Now().Truncate(time.Minute).Add(-2 * time.Minute)
	restore := metrics.SetClockForTest(func() time.Time { return at })
	defer restore()
	named := metrics.NewTable(metrics.Meta{Name: "report.fixture.requests", Group: metrics.GroupRPC, Title: "Fixture requests", Stall: 10, SaveLabels: true})
	private := metrics.NewLatency(metrics.Meta{Name: "report.fixture.private", Group: metrics.GroupFiles, Title: "Fixture files", Stall: 10})
	named.Observe("workspace.open", 40*time.Millisecond)
	private.ObserveLabeled(50*time.Millisecond, `C:\Users\someone\secret.txt`)
	src := ArchiveSource{ArchiveProcess: metrics.ArchiveProcess{Incarnation: "label-test"}, Role: "shell", Build: "label-build", BucketUpperUS: metrics.BucketUpperMicros()}
	if err := storeArchiveWindow(ctx, db, src, metrics.CaptureArchiveWindow(at.Unix(), at.Unix()+1)); err != nil {
		t.Fatal(err)
	}
	history, err := ReadHistory(ctx, path, "shell", 2)
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{}
	for _, stall := range history.Stalls {
		labels[stall.Series] = stall.Label
	}
	if labels["report.fixture.requests"] != "workspace.open" {
		t.Fatalf("the request method was not saved with its stall: %+v", history.Stalls)
	}
	if label, ok := labels["report.fixture.private"]; !ok || label != "" {
		t.Fatalf("a file label was saved, or the stall is missing: %+v", history.Stalls)
	}
	raw, err := os.ReadFile(path)
	if err == nil && strings.Contains(string(raw), "secret.txt") {
		t.Fatal("the archive file holds a private label")
	}
}

// A report is one readable Markdown file: environment, verdict, every
// category with measurements, the saved history and each stall with the code
// it was running. It holds no event details or private labels.
func TestReportIsCategorizedAndShareable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "performance.db")
	ctx := t.Context()
	db, err := openArchive(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	at := time.Now().Truncate(time.Minute).Add(-3 * time.Minute)
	restore := metrics.SetClockForTest(func() time.Time { return at })
	defer restore()
	update := metrics.NewLatency(metrics.Meta{Name: "ui.update", Group: metrics.GroupUI, Title: "Update", Crit: 50_000, SaveLabels: true})
	update.ObserveStack(120*time.Millisecond, "viewmodel.TickMsg", []string{"crude/tui/viewmodel.(*App).tick (tick.go:42)", "crude/tui/viewmodel.(*App).Update (update.go:100)"})
	for range 50 {
		update.Observe(2 * time.Millisecond)
	}
	src := ArchiveSource{ArchiveProcess: metrics.ArchiveProcess{Incarnation: "report-test"}, Role: "shell", Build: "build-new", Version: "1.2.3",
		BucketUpperUS: metrics.BucketUpperMicros()}
	if err := storeArchiveWindow(ctx, db, src, metrics.CaptureArchiveWindow(at.Unix(), at.Unix()+1)); err != nil {
		t.Fatal(err)
	}

	live := Data{Fetched: at, CoreErr: "raw connection error /home/someone/socket", Shell: metrics.Snapshot{PID: 41, Started: at.Add(-time.Hour), Seconds: 120,
		Latencies: []metrics.LatencySeries{
			{Meta: metrics.Meta{Name: "ui.input", Group: metrics.GroupUI, Title: "Input latency", Warn: 16_000, Crit: 50_000}, Total: 9,
				Window: metrics.Summary{Count: 9, P50: 1_000, P95: 3_000, P99: 3_000, Max: 3_100}},
			{Meta: metrics.Meta{Name: "git.status", Group: metrics.GroupGit, Title: "Git status"}, Total: 1, Window: metrics.Summary{Count: 1, P50: 9_000, P95: 9_000, P99: 9_000, Max: 9_000}},
		},
		Tables: []metrics.TableSeries{{Meta: metrics.Meta{Name: "link.requests", Group: metrics.GroupRPC, Title: "Shell requests by method", SaveLabels: true},
			Rows: []metrics.TableRow{{Key: "session.list", Count: 2, Sum: 400_000, Max: 300_000}}},
			{Meta: metrics.Meta{Name: "custom.operations", Group: metrics.GroupFiles, Title: "Some work"}, Rows: []metrics.TableRow{{Key: "raw context /some/private/token", Count: 1, Sum: 800, Max: 800}}}},
		Events: []metrics.Event{{At: at, Group: metrics.GroupFiles, Series: "ui.blocking", Title: "Blocking file on UI thread",
			Label: `C:\private\notes.txt`, Detail: "open C:\\private\\notes.txt: denied", Micros: 70_000}},
	}}
	r := Report{Created: at, Host: ReportHost{Version: "1.2.3", Commit: "abc", Build: "build-new", Platform: "windows/amd64", CPUs: 8, Terminal: "Windows Terminal"}, Live: &live}
	ReadReportHistory(ctx, path, &r)
	var out strings.Builder
	if err := WriteReport(&out, r); err != nil {
		t.Fatal(err)
	}
	report := out.String()
	for _, want := range []string{
		"# Crude performance report", "| Terminal | Windows Terminal |", "## Summary", "Shell process (pid 41)",
		"### UI thread", "| Input latency | Shell | 9 |", "### Core link", "| session.list | 2 |", "### Git",
		"## Shell · last 24 hours", "Build build-new (Crude 1.2.3)", "### UI thread · Shell", "| Update | 51 |",
		"## Core · last 24 hours", "## Stalls", "viewmodel.TickMsg", "```text", "crude/tui/viewmodel.(*App).tick (tick.go:42)",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("the report lacks %q:\n%s", want, report)
		}
	}
	if strings.Index(report, "### UI thread") > strings.Index(report, "### Core link") || strings.Index(report, "### Core link") > strings.Index(report, "### Git") {
		t.Fatal("categories are not in the dashboard's order")
	}
	for _, private := range []string{"notes.txt", "denied", "raw connection error", "/some/private/token"} {
		if strings.Contains(report, private) {
			t.Fatalf("the report holds %q:\n%s", private, report)
		}
	}
	// Every table row has the cells of its header.
	cells := 0
	for _, line := range strings.Split(report, "\n") {
		if !strings.HasPrefix(line, "|") {
			cells = 0
			continue
		}
		n := strings.Count(strings.ReplaceAll(line, `\|`, ""), "|")
		if cells == 0 {
			cells = n
		} else if n != cells {
			t.Fatalf("a table row has %d separators, its header %d: %q", n, cells, line)
		}
	}

	dir := filepath.Join(t.TempDir(), "reports")
	saved, err := SaveReport(dir, r)
	if err != nil {
		t.Fatal(err)
	}
	second, err := SaveReport(dir, r)
	if err != nil || second == saved {
		t.Fatalf("save in the same second collided: %q %v", second, err)
	}
	data, err := os.ReadFile(saved)
	if err != nil || string(data) != report || !strings.HasSuffix(saved, ".md") {
		t.Fatalf("the saved file differs from the report: %v", err)
	}
}

// Without saved history or a live window the report still says what it
// knows, and never calls an unsaved history healthy.
func TestReportWithoutHistory(t *testing.T) {
	r := Report{Created: time.Now(), Host: CurrentHost(func(name string) string {
		return map[string]string{"TERM_PROGRAM": "WezTerm", "TERM_PROGRAM_VERSION": "2026"}[name]
	})}
	ReadReportHistory(t.Context(), filepath.Join(t.TempDir(), "missing.db"), &r)
	var out strings.Builder
	if err := WriteReport(&out, r); err != nil {
		t.Fatal(err)
	}
	report := out.String()
	for _, want := range []string{"| Terminal | WezTerm 2026 |", "No performance history is saved on this computer yet.", "No stall was recorded in the available evidence."} {
		if !strings.Contains(report, want) {
			t.Fatalf("the report lacks %q:\n%s", want, report)
		}
	}
}
