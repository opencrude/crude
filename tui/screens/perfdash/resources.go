package perfdash

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"crude/metrics"
	"crude/perfdata"
)

type resourceView = perfdata.ResourceView

func resources(d Data) []resourceView   { return perfdata.ResourceViews(perfdata.Data(d)) }
func resourceFindings(d Data) []Finding { return perfdata.ResourceFindings(perfdata.Data(d)) }

func resourceTiles(v resourceView) []tile { return tilesFrom(perfdata.ResourceHeadlines(v)) }
func tilesFrom(headlines []perfdata.Headline) []tile {
	out := make([]tile, 0, len(headlines))
	for _, h := range headlines {
		out = append(out, tile{label: h.Label, value: h.Value, sev: h.Severity})
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
	rows := make([]resourceRow, 0, len(v.Processes))
	for _, p := range v.Processes {
		row := resourceRow{label: p.Name, detail: fmt.Sprintf("%s · pid %d", p.Role, p.PID), share: -1, value: "—", extra: "—"}
		if p.Memory >= 0 {
			row.value = formatBytes(float64(p.Memory))
			if v.Memory > 0 {
				row.share = float64(p.Memory) / float64(v.Memory)
			}
		}
		if p.CPU >= 0 {
			row.extra = formatValue(p.CPU, "%")
		}
		if row.label == "" {
			row.label = p.Role
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
