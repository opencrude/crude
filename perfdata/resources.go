package perfdata

import (
	"fmt"
	"slices"
	"strings"

	"crude/metrics"
)

// ResourceProcess retains process lifetime identity alongside its human role.
type ResourceProcess struct {
	metrics.ProcessUsage
	Role string
}

// ResourceView is one attributable resource sample. Different or unknown hosts
// never share totals. Partial totals are explicitly marked and never called zero.
type ResourceView struct {
	Label          string
	Processes      []ResourceProcess
	Memory         int64
	CPU            float64
	MemoryKnown    int
	MemoryComplete bool
	CPUComplete    bool
	System         metrics.SystemUsage
	Disk           *metrics.DiskUsage
}

// SameHost requires positive machine/boot provenance, never hostname or PID.
func SameHost(a, b metrics.Snapshot) bool {
	return a.HostIdentity == "boot_id" && b.HostIdentity == "boot_id" && a.HostBootID != "" && a.HostBootID == b.HostBootID
}

// ResourceViews keeps shell and core distinct unless both process trees are fully
// listed and every PID can be qualified by OS creation time on a proven host.
func ResourceViews(d Data) []ResourceView {
	var views []ResourceView
	merge := d.Core != nil && SameHost(d.Shell, *d.Core) && mergeable(d.Shell.Resources) && mergeable(d.Core.Resources)
	if merge {
		views = append(views, resourceView("Crude", []resourceSource{{ProcessCore, d.Core}, {ProcessShell, &d.Shell}}))
		return views
	}
	for _, p := range d.Processes() {
		if s := d.Snapshot(p); s.Resources != nil {
			views = append(views, resourceView(p.Label(), []resourceSource{{p, s}}))
		}
	}
	return views
}

type resourceSource struct {
	process  Process
	snapshot *metrics.Snapshot
}
type processIdentity struct {
	pid     int32
	created int64
}

func mergeable(r *metrics.Resources) bool {
	if r == nil || !r.TreeComplete || r.ProcessesOmitted > 0 || len(r.Processes) == 0 {
		return false
	}
	for _, p := range r.Processes {
		if p.CreatedMS <= 0 {
			return false
		}
	}
	return true
}
func resourceView(label string, sources []resourceSource) ResourceView {
	v := ResourceView{Label: label, CPU: -1, System: metrics.SystemUsage{CPU: -1}, MemoryComplete: true, CPUComplete: true}
	seen := map[processIdentity]bool{}
	names := map[int32]string{}
	roots := map[processIdentity]string{}
	for _, src := range sources {
		if len(src.snapshot.Resources.Processes) > 0 {
			root := src.snapshot.Resources.Processes[0]
			key := processIdentity{root.PID, root.CreatedMS}
			if roots[key] == "" {
				roots[key] = src.process.Label()
			}
			names[root.PID] = roots[key]
		}
	}
	var children []ResourceProcess
	for _, src := range sources {
		r := src.snapshot.Resources
		v.System = r.System
		if r.Disk != nil {
			v.Disk = r.Disk
		}
		if !r.TreeComplete || r.ProcessesOmitted > 0 {
			v.MemoryComplete = false
			v.CPUComplete = false
		}
		for _, p := range r.Processes {
			p.Name = DisplayProcessName(p.Name)
			key := processIdentity{p.PID, p.CreatedMS}
			if seen[key] {
				continue
			}
			seen[key] = true
			if role := roots[key]; role != "" {
				v.Processes = append(v.Processes, ResourceProcess{p, role})
			} else {
				names[p.PID] = p.Name
				children = append(children, ResourceProcess{ProcessUsage: p})
			}
		}
	}
	slices.SortStableFunc(v.Processes, func(a, b ResourceProcess) int { return strings.Compare(b.Role, a.Role) })
	for i := range children {
		name := names[children[i].Parent]
		if name == "" {
			name = fmt.Sprintf("pid %d", children[i].Parent)
		}
		children[i].Role = "started by " + name
	}
	slices.SortStableFunc(children, func(a, b ResourceProcess) int {
		if a.Memory > b.Memory {
			return -1
		}
		if a.Memory < b.Memory {
			return 1
		}
		return 0
	})
	v.Processes = append(v.Processes, children...)
	for _, p := range v.Processes {
		if p.Memory >= 0 {
			v.MemoryKnown++
			v.Memory += p.Memory
		} else {
			v.MemoryComplete = false
		}
		if p.CPU >= 0 {
			v.CPU = max(v.CPU, 0) + p.CPU
		} else {
			v.CPUComplete = false
		}
	}
	if len(sources) == 1 {
		r := sources[0].snapshot.Resources
		// The sampler totals include descendants omitted from the displayed top rows.
		if r.ProcessCount > 0 {
			v.MemoryKnown = r.MemoryKnown
			v.Memory = r.MemoryKnownBytes
			v.CPU = -1
			if r.CPUKnown > 0 {
				v.CPU = r.CPUKnownPercent
			}
			v.MemoryComplete = r.TreeComplete && r.MemoryKnown == r.ProcessCount
			v.CPUComplete = r.TreeComplete && r.CPUKnown == r.ProcessCount
		}
	}
	if len(v.Processes) == 0 {
		v.MemoryComplete = false
		v.CPUComplete = false
	}
	return v
}

func DisplayProcessName(name string) string {
	if strings.HasPrefix(name, "crude-core-") {
		return "crude-core"
	}
	return name
}
func MemorySeverity(s metrics.SystemUsage) Severity {
	if s.MemoryTotal <= 0 || s.Error != "" {
		return SeverityUnknown
	}
	return spaceSeverity(s.MemoryAvailable, s.MemoryTotal)
}
func DiskSeverity(d *metrics.DiskUsage) Severity {
	if d == nil || d.DeviceTotal <= 0 || d.Err != "" || d.MeasuredAt.IsZero() {
		return SeverityUnknown
	}
	return spaceSeverity(d.DeviceFree, d.DeviceTotal)
}
func spaceSeverity(free, total int64) Severity {
	switch share := float64(free) / float64(total); {
	case share < .05:
		return SeverityCrit
	case share < .10:
		return SeverityWarn
	}
	return SeverityOK
}
func ResourceFindings(d Data) []Finding {
	var out []Finding
	for _, v := range ResourceViews(d) {
		if sev := DiskSeverity(v.Disk); sev >= SeverityWarn {
			title := "Disk space low"
			if sev == SeverityCrit {
				title = "Disk almost full"
			}
			out = append(out, Finding{Severity: sev, Title: title, Section: SectionResources, Detail: fmt.Sprintf("%s: %s free of %s on the disk holding Crude's data; saving sessions and history fails when it fills", v.Label, FormatBytes(float64(v.Disk.DeviceFree)), FormatBytes(float64(v.Disk.DeviceTotal)))})
		}
		if sev := MemorySeverity(v.System); sev >= SeverityWarn {
			out = append(out, Finding{Severity: sev, Title: "Memory is running out", Section: SectionResources, Detail: fmt.Sprintf("%s: %s of %s available; the system swaps and every process slows", v.Label, FormatBytes(float64(v.System.MemoryAvailable)), FormatBytes(float64(v.System.MemoryTotal)))})
		}
		if !v.MemoryComplete || !v.CPUComplete || MemorySeverity(v.System) == SeverityUnknown || v.System.CPU < 0 || DiskSeverity(v.Disk) == SeverityUnknown {
			out = append(out, Finding{Severity: SeverityUnknown, Title: v.Label + " resource coverage incomplete", Section: SectionResources, Detail: "unavailable, warming-up or incomplete memory, CPU or disk samples are unknown, not zero or healthy"})
		}
	}
	for _, p := range d.Processes() {
		if d.Snapshot(p).Resources == nil {
			out = append(out, Finding{Severity: SeverityUnknown, Title: p.Label() + " resources unavailable", Section: SectionResources, Detail: "this snapshot contains no OS resource sample"})
		}
	}
	return out
}
