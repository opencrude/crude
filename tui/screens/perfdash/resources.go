package perfdash

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"crude/metrics"
)

// resourceProcess is one process Crude runs, with the role a person
// recognizes.
type resourceProcess struct {
	metrics.ProcessUsage
	role string
}

// resourceView merges both processes' resource samples: what Crude occupies
// on this machine and what the machine has left.
type resourceView struct {
	processes []resourceProcess
	// memory is the resident memory of every listed process; cpu their
	// combined share of one core (-1 until a second sample).
	memory int64
	cpu    float64
	system metrics.SystemUsage
	disk   *metrics.DiskUsage
}

func resources(d Data) (resourceView, bool) {
	view := resourceView{cpu: -1, system: metrics.SystemUsage{CPU: -1}}
	seen := map[int32]bool{}
	names := map[int32]string{}
	var started []resourceProcess
	found := false
	add := func(p process, snapshot *metrics.Snapshot) {
		r := snapshot.Resources
		if r == nil {
			return
		}
		found = true
		if r.System.MemoryTotal > 0 {
			view.system = r.System
		}
		if r.Disk != nil {
			view.disk = r.Disk
		}
		for i, usage := range r.Processes {
			usage.Name = displayProcessName(usage.Name)
			if !seen[usage.PID] {
				names[usage.PID] = usage.Name
			}
			if i == 0 {
				role := p.label()
				if d.Core == nil {
					role = "Crude"
				}
				names[usage.PID] = role
				if !seen[usage.PID] {
					view.processes = append(view.processes, resourceProcess{ProcessUsage: usage, role: role})
				}
				seen[usage.PID] = true
				continue
			}
			if seen[usage.PID] {
				continue
			}
			seen[usage.PID] = true
			started = append(started, resourceProcess{ProcessUsage: usage})
		}
	}
	// The core first: if the shell started it, the shell's tree repeats it.
	if d.Core != nil {
		add(processCore, d.Core)
	}
	add(processShell, &d.Shell)
	slices.SortStableFunc(view.processes, func(a, b resourceProcess) int {
		if a.role == processShell.label() {
			return -1
		}
		if b.role == processShell.label() {
			return 1
		}
		return 0
	})
	for i := range started {
		parent := names[started[i].Parent]
		if parent == "" {
			parent = fmt.Sprintf("pid %d", started[i].Parent)
		}
		started[i].role = "started by " + parent
	}
	slices.SortStableFunc(started, func(a, b resourceProcess) int { return compareInt64(b.Memory, a.Memory) })
	view.processes = append(view.processes, started...)
	for _, p := range view.processes {
		if p.Memory > 0 {
			view.memory += p.Memory
		}
		if p.CPU >= 0 {
			view.cpu = max(view.cpu, 0) + p.CPU
		}
	}
	return view, found
}

// displayProcessName drops the build hash from the installed core's name.
func displayProcessName(name string) string {
	if strings.HasPrefix(name, "crude-core-") {
		return "crude-core"
	}
	return name
}

func compareInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// resourceTiles summarize the machine view in one row.
func resourceTiles(v resourceView) []tile {
	tiles := []tile{{label: "Crude memory", value: formatBytes(float64(v.memory))}}
	if v.cpu >= 0 {
		tiles = append(tiles, tile{label: "Crude CPU", value: formatValue(v.cpu, "%")})
	}
	if s := v.system; s.MemoryTotal > 0 {
		used := s.MemoryTotal - s.MemoryAvailable
		tiles = append(tiles, tile{label: "System memory", sev: memorySeverity(s),
			value: fmt.Sprintf("%s of %s", formatBytes(float64(used)), formatBytes(float64(s.MemoryTotal)))})
	}
	if v.system.CPU >= 0 {
		tiles = append(tiles, tile{label: "System CPU", value: fmt.Sprintf("%s of %d cores", formatValue(v.system.CPU, "%"), v.system.CPUs)})
	}
	if disk := v.disk; disk != nil {
		if disk.MeasuredAt.IsZero() {
			tiles = append(tiles, tile{label: "Crude data", value: "measuring…"})
		} else {
			tiles = append(tiles, tile{label: "Crude data", value: formatBytes(float64(disk.Total))})
		}
		if disk.DeviceTotal > 0 {
			tiles = append(tiles, tile{label: "Disk free", sev: diskSeverity(disk),
				value: fmt.Sprintf("%s of %s", formatBytes(float64(disk.DeviceFree)), formatBytes(float64(disk.DeviceTotal)))})
		}
	}
	return tiles
}

func memorySeverity(s metrics.SystemUsage) Severity {
	if s.MemoryTotal <= 0 {
		return SeverityOK
	}
	switch free := float64(s.MemoryAvailable) / float64(s.MemoryTotal); {
	case free < 0.05:
		return SeverityCrit
	case free < 0.10:
		return SeverityWarn
	}
	return SeverityOK
}

func diskSeverity(disk *metrics.DiskUsage) Severity {
	if disk == nil || disk.DeviceTotal <= 0 {
		return SeverityOK
	}
	switch free := float64(disk.DeviceFree) / float64(disk.DeviceTotal); {
	case free < 0.05:
		return SeverityCrit
	case free < 0.10:
		return SeverityWarn
	}
	return SeverityOK
}

// resourceFindings report a machine running out of room.
func resourceFindings(d Data) []Finding {
	v, ok := resources(d)
	if !ok {
		return nil
	}
	var out []Finding
	if sev := diskSeverity(v.disk); sev > SeverityOK {
		title := "Disk space low"
		if sev == SeverityCrit {
			title = "Disk almost full"
		}
		out = append(out, Finding{Severity: sev, Title: title, Section: SectionResources,
			Detail: fmt.Sprintf("%s free of %s on the disk holding Crude's data; saving sessions and history fails when it fills",
				formatBytes(float64(v.disk.DeviceFree)), formatBytes(float64(v.disk.DeviceTotal)))})
	}
	if sev := memorySeverity(v.system); sev > SeverityOK {
		out = append(out, Finding{Severity: sev, Title: "Memory is running out", Section: SectionResources,
			Detail: fmt.Sprintf("%s of %s available; the system swaps and every process slows",
				formatBytes(float64(v.system.MemoryAvailable)), formatBytes(float64(v.system.MemoryTotal)))})
	}
	return out
}

// resourceRow is one line of a Resources list: a name, its context, a share
// bar and its value.
type resourceRow struct {
	label  string
	detail string
	// share is the bar's fill in [0, 1]; negative hides the bar.
	share float64
	value string
	extra string
	sev   Severity
}

func processRows(v resourceView) []resourceRow {
	rows := make([]resourceRow, 0, len(v.processes))
	for _, p := range v.processes {
		row := resourceRow{label: p.Name, detail: fmt.Sprintf("%s · pid %d", p.role, p.PID), share: -1, value: "—", extra: "—"}
		if p.Memory >= 0 {
			row.value = formatBytes(float64(p.Memory))
			if v.memory > 0 {
				row.share = float64(p.Memory) / float64(v.memory)
			}
		}
		if p.CPU >= 0 {
			row.extra = formatValue(p.CPU, "%")
		}
		if row.label == "" {
			row.label = p.role
		}
		rows = append(rows, row)
	}
	return rows
}

func diskPartRows(disk *metrics.DiskUsage) []resourceRow {
	rows := make([]resourceRow, 0, len(disk.Parts))
	for _, part := range disk.Parts {
		if part.Bytes == 0 {
			continue
		}
		rows = append(rows, resourceRow{label: part.Name, share: share(part.Bytes, disk.Total), value: formatBytes(float64(part.Bytes))})
	}
	return rows
}

func workspaceRows(disk *metrics.DiskUsage) []resourceRow {
	rows := make([]resourceRow, 0, len(disk.Workspaces))
	for _, ws := range disk.Workspaces {
		row := resourceRow{label: filepath.Base(ws.Project), share: share(ws.Bytes, disk.WorkspaceBytes), value: formatBytes(float64(ws.Bytes))}
		location := ws.Project
		if ws.Project == "" {
			row.label, location = "Unknown project", ws.ID
		}
		var parts []string
		for _, part := range []struct {
			name  string
			bytes int64
		}{{"sessions", ws.Sessions}, {"history", ws.History}, {"worktrees", ws.Worktrees}} {
			if part.bytes > 0 && part.bytes*10 >= ws.Bytes {
				parts = append(parts, part.name+" "+formatBytes(float64(part.bytes)))
			}
		}
		if ws.Missing {
			parts = append([]string{"folder deleted"}, parts...)
			row.sev = SeverityWarn
		}
		// The breakdown first: a long path truncates, never the sizes.
		row.detail = strings.Join(append(parts, location), " · ")
		rows = append(rows, row)
	}
	return rows
}

func share(part, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return min(1, float64(part)/float64(total))
}

// diskHeading names the data folder and when it was measured.
func diskHeading(disk *metrics.DiskUsage, now time.Time) string {
	heading := "Disk · " + disk.Path
	switch {
	case disk.Err != "":
		return heading + " · " + disk.Err
	case disk.MeasuredAt.IsZero():
		return heading + " · measuring…"
	}
	heading += " · " + formatBytes(float64(disk.Total))
	if now.Sub(disk.MeasuredAt) >= 2*time.Second {
		heading += " · measured " + formatAgo(now, disk.MeasuredAt)
	}
	return heading
}

func workspacesHeading(disk *metrics.DiskUsage) string {
	return fmt.Sprintf("Largest workspaces · %s saved, %s", groupDigits(disk.WorkspaceCount), formatBytes(float64(disk.WorkspaceBytes)))
}

func orphanNote(disk *metrics.DiskUsage) string {
	if disk.Orphaned == 0 {
		return ""
	}
	return fmt.Sprintf("%s saved workspaces belong to folders that no longer exist and take %s.",
		groupDigits(disk.Orphaned), formatBytes(float64(disk.OrphanedBytes)))
}

// groupDigits writes an exact count with thousands separators: 4,476.
func groupDigits(n int) string {
	digits := strconv.Itoa(n)
	var b strings.Builder
	for i, digit := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(digit)
	}
	return b.String()
}
