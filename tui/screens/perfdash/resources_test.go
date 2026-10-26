package perfdash

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/metrics"
	"crude/perfdata"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const gib = 1 << 30

func resourceData() Data {
	now := time.Now()
	shell := metrics.Snapshot{Taken: now, HostIdentity: "boot_id", HostBootID: "test-boot", Resources: &metrics.Resources{
		TreeComplete: true,
		System:       metrics.SystemUsage{MemoryTotal: 16 * gib, MemoryAvailable: 8 * gib, CPU: 12, CPUs: 8},
		Processes: []metrics.ProcessUsage{
			{PID: 10, CreatedMS: 100, Name: "crude", Memory: 80 << 20, CPU: 5},
			// The shell started the core, so its tree repeats the core.
			{PID: 20, CreatedMS: 200, Parent: 10, Name: "crude-core-abc123", Memory: 120 << 20, CPU: 2},
		},
	}}
	core := metrics.Snapshot{Taken: now, HostIdentity: "boot_id", HostBootID: "test-boot", Resources: &metrics.Resources{
		TreeComplete: true,
		System:       metrics.SystemUsage{MemoryTotal: 16 * gib, MemoryAvailable: 8 * gib, CPU: 12, CPUs: 8},
		Processes: []metrics.ProcessUsage{
			{PID: 20, CreatedMS: 200, Name: "crude-core-abc123", Memory: 120 << 20, CPU: 2},
			{PID: 30, CreatedMS: 300, Parent: 20, Name: "zsh", Memory: 10 << 20, CPU: 0},
			{PID: 31, CreatedMS: 310, Parent: 20, Name: "gopls", Memory: 600 << 20, CPU: 40},
		},
		Disk: &metrics.DiskUsage{Path: "/home/me/.crude", Total: 15 * gib, MeasuredAt: now,
			DeviceTotal: 1000 * gib, DeviceFree: 30 * gib,
			Parts:          []metrics.DiskPart{{Name: "Workspaces", Bytes: 13 * gib}, {Name: "Backups", Bytes: 2 * gib}},
			WorkspaceCount: 4753, WorkspaceBytes: 13 * gib, Orphaned: 4476, OrphanedBytes: 3 * gib,
			Workspaces: []metrics.WorkspaceDisk{
				{ID: "a", Project: "/home/me/crude", Bytes: 10 * gib, Sessions: 9 * gib, History: gib},
				{ID: "b", Project: "/tmp/TestOld/001", Bytes: 1 << 20, History: 1 << 20, Missing: true},
			}},
	}}
	return Data{Shell: shell, Core: &core, Fetched: now}
}

// Both processes' samples merge into one list without counting the core
// twice; started processes name who started them, largest first.
func TestResourcesMergeShellAndCore(t *testing.T) {
	views := resources(resourceData())
	if len(views) != 1 {
		t.Fatal("no resources")
	}
	view := views[0]
	var got []string
	for _, p := range view.Processes {
		got = append(got, p.Name+"/"+p.Role)
	}
	want := "crude/Shell crude-core/Core gopls/started by Core zsh/started by Core"
	if strings.Join(got, " ") != want {
		t.Fatalf("processes = %v", got)
	}
	if view.Memory != (80+120+10+600)<<20 || view.CPU != 47 {
		t.Fatalf("totals memory=%d cpu=%v", view.Memory, view.CPU)
	}
}

func TestLowDiskIsAFinding(t *testing.T) {
	findings := resourceFindings(resourceData())
	if len(findings) != 1 || findings[0].Severity != SeverityCrit || findings[0].Section != SectionResources || !strings.Contains(findings[0].Detail, "30.0 GiB free of 1000.0 GiB") {
		t.Fatalf("findings = %+v", findings)
	}
}

func TestResourcesTabPaintsAtWideAndNarrowWidths(t *testing.T) {
	for _, width := range []int{120, 44} {
		screen := New(theme.DefaultDark())
		screen.SetData(resourceData())
		screen.SetSection(SectionResources)
		host := ui.NewHost(theme.DefaultDark(), screen)
		host.Resize(width, 40)
		buf := surface.NewCellBuffer(width, 40)
		host.Paint(buf)
		text := paintedText(buf)
		for _, want := range []string{"Crude memory", "Processes", "gopls", "started by Core", "Disk · ", "Largest workspaces", "folder deleted", "4,476 saved workspaces", "no longer exist"} {
			if !strings.Contains(text, want) {
				t.Fatalf("width %d lacks %q:\n%s", width, want, text)
			}
		}
		if width == 120 && (!strings.Contains(text, "600 MiB") || !strings.Contains(text, "40%") || !strings.Contains(text, "sessions 9.0 GiB")) {
			t.Fatalf("wide layout lacks values:\n%s", text)
		}
	}
}

func paintedText(buf *surface.CellBuffer) string {
	var b strings.Builder
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			r := buf.Get(x, y).Char
			if r == 0 {
				r = ' '
			}
			b.WriteRune(r)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func TestResourcesNeverPaintCrossHostAggregate(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		for _, width := range []int{120, 44} {
			d := resourceData()
			// Identical hostnames and colliding PIDs must never establish identity.
			d.Shell.Hostname = "same-name"
			d.Core.Hostname = "same-name"
			d.Core.HostBootID = "remote-boot"
			if unknown {
				d.Shell.HostIdentity = "unknown"
				d.Core.HostIdentity = "unknown"
				d.Shell.HostBootID = ""
				d.Core.HostBootID = ""
			}
			d.Core.Resources.Processes[0].PID = 10
			screen := New(theme.DefaultDark())
			screen.SetData(d)
			screen.SetSection(SectionResources)
			host := ui.NewHost(theme.DefaultDark(), screen)
			host.Resize(width, 50)
			buf := surface.NewCellBuffer(width, 50)
			host.Paint(buf)
			text := paintedText(buf)
			for _, want := range []string{"Shell memory", "Core memory", "Processes · Shell", "Processes · Core", "gopls"} {
				if !strings.Contains(text, want) {
					t.Fatalf("unknown=%v width=%d lacks %q:\n%s", unknown, width, want, text)
				}
			}
			if strings.Contains(text, "Crude memory") || strings.Contains(text, "Crude CPU") {
				t.Fatalf("mixed machines painted aggregate:\n%s", text)
			}
			report := screen.Report()
			if !strings.Contains(report, "Shell memory: 200 MiB") || !strings.Contains(report, "Core memory: 730 MiB") || strings.Contains(report, "Crude memory:") {
				t.Fatalf("report mixed host totals:\n%s", report)
			}
		}
	}
}

func TestDashboardAndInspectionShareFindingsAndHeadlines(t *testing.T) {
	d := resourceData()
	d.Shell.Seconds = 120
	d.Shell.Latencies = []metrics.LatencySeries{{Meta: metrics.Meta{Name: "ui.input", Warn: 10_000, Crit: 100_000}, Total: 1, Window: metrics.Summary{Count: 1, P95: 150_000, Max: 150_000}, Recent: metrics.Summary{Count: 1, P95: 150_000}}}
	// Single source compares the exact same sample consumed by inspection.
	d.Core = nil
	screen := New(theme.DefaultDark())
	screen.SetData(d)
	host := ui.NewHost(theme.DefaultDark(), screen)
	host.Resize(120, 40)
	buf := surface.NewCellBuffer(120, 40)
	host.Paint(buf)
	text := paintedText(buf)
	result := perfdata.Analyze(d.Shell, "shell")
	if !reflect.DeepEqual(screen.Findings(), result.Findings) {
		t.Fatalf("UI findings differ from core evidence: %+v / %+v", screen.Findings(), result.Findings)
	}
	for _, want := range []string{"Input latency 150ms", "Input 150ms", "Shell memory"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing canonical reading %q:\n%s", want, text)
		}
	}
	if got := overviewTiles(d); len(got) != len(result.Headlines) {
		t.Fatalf("headlines count=%d neutral=%d", len(got), len(result.Headlines))
	} else {
		for i, tile := range got {
			h := result.Headlines[i]
			if tile.label != h.Label || tile.value != h.Value || tile.sev != h.Severity {
				t.Fatalf("headline %d differs", i)
			}
		}
	}
}

func TestUnknownCoverageIsNotPaintedAsHealthy(t *testing.T) {
	screen := New(theme.DefaultDark())
	screen.SetData(Data{Shell: metrics.Snapshot{Taken: time.Now()}})
	host := ui.NewHost(theme.DefaultDark(), screen)
	host.Resize(100, 30)
	buf := surface.NewCellBuffer(100, 30)
	host.Paint(buf)
	text := paintedText(buf)
	if !strings.Contains(text, "? Unknown") || strings.Contains(text, "✓ Responsive") {
		t.Fatalf("unknown evidence shown healthy:\n%s", text)
	}
}
