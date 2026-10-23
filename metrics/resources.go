package metrics

import (
	"context"
	"os"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
	gproc "github.com/shirou/gopsutil/v4/process"
)

// Resources is what Crude occupies on this machine when a snapshot is taken:
// memory and CPU of this process and every process it started (language
// servers, terminals, commands), the machine's totals, and, from the process
// that owns Crude's data, disk usage. It is read only by Take, which runs off
// the UI goroutine and only while the Performance surface is visible.
type Resources struct {
	// Processes lists this process first, then its descendants by memory.
	Processes []ProcessUsage `json:"processes,omitempty"`
	System    SystemUsage    `json:"system"`
	Disk      *DiskUsage     `json:"disk,omitempty"`
}

type ProcessUsage struct {
	PID    int32  `json:"pid"`
	Parent int32  `json:"parent,omitempty"`
	Name   string `json:"name"`
	// Memory is resident memory in bytes; -1 when the OS would not say.
	Memory int64 `json:"memory"`
	// CPU is the share of one core since the previous snapshot, in percent;
	// -1 until two snapshots exist.
	CPU float64 `json:"cpu"`
}

type SystemUsage struct {
	MemoryTotal     int64 `json:"memory_total,omitempty"`
	MemoryAvailable int64 `json:"memory_available,omitempty"`
	// CPU is the machine's busy share of all cores since the previous
	// snapshot, in percent; -1 until two snapshots exist.
	CPU  float64 `json:"cpu"`
	CPUs int     `json:"cpus,omitempty"`
}

// DiskUsage is the space Crude's data takes. Measuring walks every saved
// file, so its owner refreshes it in the background and Take returns the
// latest result.
type DiskUsage struct {
	Path  string `json:"path"`
	Total int64  `json:"total"`
	// Parts is the top-level breakdown of Path, largest first.
	Parts []DiskPart `json:"parts,omitempty"`
	// Workspaces are the largest saved workspaces, largest first;
	// WorkspaceCount and WorkspaceBytes cover all of them.
	Workspaces     []WorkspaceDisk `json:"workspaces,omitempty"`
	WorkspaceCount int             `json:"workspace_count,omitempty"`
	WorkspaceBytes int64           `json:"workspace_bytes,omitempty"`
	// Orphaned counts saved workspaces whose project folder no longer exists.
	Orphaned      int   `json:"orphaned,omitempty"`
	OrphanedBytes int64 `json:"orphaned_bytes,omitempty"`
	// DeviceTotal and DeviceFree describe the disk holding Path.
	DeviceTotal int64 `json:"device_total,omitempty"`
	DeviceFree  int64 `json:"device_free,omitempty"`
	// MeasuredAt is zero until the first measurement finishes.
	MeasuredAt time.Time `json:"measured_at,omitzero"`
	Measuring  bool      `json:"measuring,omitempty"`
	Err        string    `json:"err,omitempty"`
}

type DiskPart struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

type WorkspaceDisk struct {
	ID      string `json:"id"`
	Project string `json:"project,omitempty"`
	Bytes   int64  `json:"bytes"`
	// Sessions, History and Worktrees are the parts of Bytes in saved
	// sessions, Local History and managed worktrees.
	Sessions  int64 `json:"sessions,omitempty"`
	History   int64 `json:"history,omitempty"`
	Worktrees int64 `json:"worktrees,omitempty"`
	// Missing: the project folder no longer exists.
	Missing bool `json:"missing,omitempty"`
}

var (
	diskMu     sync.Mutex
	diskSource func() *DiskUsage
)

// SetDiskSource installs the reader of Crude's disk usage. The process that
// owns the data installs it; it must return promptly with its latest result.
func SetDiskSource(source func() *DiskUsage) {
	diskMu.Lock()
	diskSource = source
	diskMu.Unlock()
}

// processLimit bounds the descendants a snapshot reports.
const processLimit = 24

type cpuSample struct {
	busy float64 // CPU seconds
	at   time.Time
}

type resourceSampler struct {
	mu        sync.Mutex
	processes map[int32]cpuSample
	names     map[int32]string
	system    struct {
		busy, total float64
		set         bool
	}
}

var sampler = resourceSampler{processes: map[int32]cpuSample{}, names: map[int32]string{}}

func takeResources() *Resources {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out := &Resources{System: SystemUsage{CPU: -1, CPUs: runtime.NumCPU()}}
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		out.System.MemoryTotal, out.System.MemoryAvailable = int64(vm.Total), int64(vm.Available)
	}
	self := int32(os.Getpid())
	tree, _ := descendants(self)
	sampler.mu.Lock()
	defer sampler.mu.Unlock()
	if times, err := cpu.TimesWithContext(ctx, false); err == nil && len(times) == 1 {
		t := times[0]
		// Guest time is already counted in User on Linux.
		busy := t.User + t.Nice + t.System + t.Irq + t.Softirq + t.Steal
		total := busy + t.Idle + t.Iowait
		if sampler.system.set && total > sampler.system.total {
			out.System.CPU = clampPercent((busy-sampler.system.busy)/(total-sampler.system.total)*100, 100)
		}
		sampler.system.busy, sampler.system.total, sampler.system.set = busy, total, true
	}
	now := time.Now()
	seen := make(map[int32]bool, len(tree)+1)
	read := func(pid, parent int32) ProcessUsage {
		usage := ProcessUsage{PID: pid, Parent: parent, Memory: -1, CPU: -1}
		seen[pid] = true
		p := &gproc.Process{Pid: pid}
		if info, err := p.MemoryInfoWithContext(ctx); err == nil {
			usage.Memory = int64(info.RSS)
		}
		if times, err := p.TimesWithContext(ctx); err == nil {
			busy := times.User + times.System
			if last, ok := sampler.processes[pid]; ok && now.After(last.at) && busy >= last.busy {
				usage.CPU = (busy - last.busy) / now.Sub(last.at).Seconds() * 100
			}
			sampler.processes[pid] = cpuSample{busy: busy, at: now}
		}
		name, ok := sampler.names[pid]
		if !ok {
			name, _ = p.NameWithContext(ctx)
			sampler.names[pid] = name
		}
		usage.Name = name
		return usage
	}
	out.Processes = append(out.Processes, read(self, 0))
	children := make([]ProcessUsage, 0, len(tree))
	for _, node := range tree {
		children = append(children, read(node.pid, node.parent))
	}
	slices.SortStableFunc(children, func(a, b ProcessUsage) int {
		switch {
		case a.Memory > b.Memory:
			return -1
		case a.Memory < b.Memory:
			return 1
		}
		return int(a.PID - b.PID)
	})
	if len(children) > processLimit {
		children = children[:processLimit]
	}
	out.Processes = append(out.Processes, children...)
	for pid := range sampler.processes {
		if !seen[pid] {
			delete(sampler.processes, pid)
			delete(sampler.names, pid)
		}
	}
	diskMu.Lock()
	source := diskSource
	diskMu.Unlock()
	if source != nil {
		out.Disk = source()
	}
	return out
}

func clampPercent(v, limit float64) float64 {
	return min(max(v, 0), limit)
}

type pidParent struct{ pid, parent int32 }

// descendants lists every process below root, parents before children.
func descendants(root int32) ([]pidParent, error) {
	parents, err := processParents()
	if err != nil {
		return nil, err
	}
	children := make(map[int32][]int32)
	for pid, parent := range parents {
		if pid != parent {
			children[parent] = append(children[parent], pid)
		}
	}
	var out []pidParent
	queue := []int32{root}
	visited := map[int32]bool{root: true}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		kids := children[parent]
		slices.Sort(kids)
		for _, pid := range kids {
			if visited[pid] {
				continue
			}
			visited[pid] = true
			out = append(out, pidParent{pid: pid, parent: parent})
			queue = append(queue, pid)
		}
	}
	return out, nil
}
