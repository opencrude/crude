package perfdash

import (
	"strings"
	"testing"
	"time"

	"crude/metrics"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const gib = 1 << 30

func resourceData() Data {
	now := time.Now()
	shell := metrics.Snapshot{Taken: now, Resources: &metrics.Resources{
		System: metrics.SystemUsage{MemoryTotal: 16 * gib, MemoryAvailable: 8 * gib, CPU: 12, CPUs: 8},
		Processes: []metrics.ProcessUsage{
			{PID: 10, Name: "crude", Memory: 80 << 20, CPU: 5},
			// The shell started the core, so its tree repeats the core.
			{PID: 20, Parent: 10, Name: "crude-core-abc123", Memory: 120 << 20, CPU: 2},
		},
	}}
	core := metrics.Snapshot{Taken: now, Resources: &metrics.Resources{
		System: metrics.SystemUsage{MemoryTotal: 16 * gib, MemoryAvailable: 8 * gib, CPU: 12, CPUs: 8},
		Processes: []metrics.ProcessUsage{
			{PID: 20, Name: "crude-core-abc123", Memory: 120 << 20, CPU: 2},
			{PID: 30, Parent: 20, Name: "zsh", Memory: 10 << 20, CPU: 0},
			{PID: 31, Parent: 20, Name: "gopls", Memory: 600 << 20, CPU: 40},
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
	view, ok := resources(resourceData())
	if !ok {
		t.Fatal("no resources")
	}
	var got []string
	for _, p := range view.processes {
		got = append(got, p.Name+"/"+p.role)
	}
	want := "crude/Shell crude-core/Core gopls/started by Core zsh/started by Core"
	if strings.Join(got, " ") != want {
		t.Fatalf("processes = %v", got)
	}
	if view.memory != (80+120+10+600)<<20 || view.cpu != 47 {
		t.Fatalf("totals memory=%d cpu=%v", view.memory, view.cpu)
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
