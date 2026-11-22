package metrics

import (
	"context"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
	gproc "github.com/shirou/gopsutil/v4/process"
)

// Resources is what Crude occupies on this machine when a snapshot is taken:
// memory and CPU of this process and every process it started (language
// servers, terminals, commands), the machine's totals, and, from the process
// that owns Crude's data, disk usage. It is demand sampled by Take or Inspect
// off the UI goroutine; hidden presentation never starts a polling loop.
type Resources struct {
	// Complete covers required OS figures and returned source rows, including
	// interval CPU. DiskStatus separately marks an absent or warming disk source.
	Status         string `json:"status"`
	MemoryComplete bool   `json:"memory_complete"`
	CPUComplete    bool   `json:"cpu_complete"`
	// Processes lists this process first, then its descendants by memory.
	Processes        []ProcessUsage `json:"processes,omitempty"`
	System           SystemUsage    `json:"system"`
	Disk             *DiskUsage     `json:"disk,omitempty"`
	Started          time.Time      `json:"started"`
	Completed        time.Time      `json:"completed"`
	ProcessCount     int            `json:"process_count"`
	ProcessesListed  int            `json:"processes_listed"`
	ProcessesOmitted int            `json:"processes_omitted"`
	TreeComplete     bool           `json:"tree_complete"`
	Complete         bool           `json:"complete"`
	Errors           []string       `json:"errors,omitempty"`
	MemoryKnown      int            `json:"memory_known"`
	CPUKnown         int            `json:"cpu_known"`
	// Known totals cover every sampled process, not only the listed top rows.
	MemoryKnownBytes int64   `json:"memory_known_bytes"`
	CPUKnownPercent  float64 `json:"cpu_known_percent"`
	DiskStatus       string  `json:"disk_status"`
	WorkspaceListed  int     `json:"workspace_listed"`
	WorkspaceOmitted int     `json:"workspace_omitted"`
}

type ProcessUsage struct {
	PID    int32  `json:"pid"`
	Parent int32  `json:"parent,omitempty"`
	Name   string `json:"name"`
	// Memory is resident memory in bytes; -1 when the OS would not say.
	Memory int64 `json:"memory"`
	// CPU is the share of one core since the previous snapshot, in percent;
	// -1 until two snapshots exist.
	CPU        float64   `json:"cpu"`
	CreatedMS  int64     `json:"created_ms,omitempty"`
	SampledAt  time.Time `json:"sampled_at"`
	PreviousAt time.Time `json:"previous_at,omitzero"`
	CPUStatus  string    `json:"cpu_status"`
	Error      string    `json:"error,omitempty"`
}

type SystemUsage struct {
	MemoryTotal     int64 `json:"memory_total,omitempty"`
	MemoryAvailable int64 `json:"memory_available,omitempty"`
	// CPU is the machine's busy share of all cores since the previous
	// snapshot, in percent; -1 until two snapshots exist.
	CPU        float64   `json:"cpu"`
	CPUs       int       `json:"cpus,omitempty"`
	SampledAt  time.Time `json:"sampled_at"`
	PreviousAt time.Time `json:"previous_at,omitzero"`
	CPUStatus  string    `json:"cpu_status"`
	Error      string    `json:"error,omitempty"`
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
	busy    float64 // CPU seconds
	at      time.Time
	created int64
}

type resourceSampler struct {
	mu        contextMutex
	processes map[int32]cpuSample
	names     map[int32]string
	system    struct {
		busy, total float64
		set         bool
		at          time.Time
	}
}

var sampler = resourceSampler{processes: map[int32]cpuSample{}, names: map[int32]string{}}

// contextMutex allows a cancelled read to stop waiting for another sampler.
type contextMutex struct {
	once  sync.Once
	token chan struct{}
}

func (m *contextMutex) init() {
	m.once.Do(func() { m.token = make(chan struct{}, 1); m.token <- struct{}{} })
}
func (m *contextMutex) LockContext(ctx context.Context) error {
	m.init()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.token:
		return nil
	}
}
func (m *contextMutex) Lock()   { _ = m.LockContext(context.Background()) }
func (m *contextMutex) Unlock() { m.token <- struct{}{} }

func takeResources() *Resources { return takeResourcesContext(context.Background(), false) }

func takeResourcesContext(parent context.Context, all bool) *Resources {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	out := &Resources{Started: time.Now(), Complete: true, DiskStatus: "unavailable",
		System: SystemUsage{CPU: -1, MemoryTotal: -1, MemoryAvailable: -1, CPUs: runtime.NumCPU(), CPUStatus: "unavailable"}}
	fail := func(err error) {
		if err != nil {
			out.Complete = false
			out.Errors = append(out.Errors, err.Error())
		}
	}
	if err := sampler.mu.LockContext(ctx); err != nil {
		fail(err)
		out.Status = "unavailable"
		out.Completed = time.Now()
		return out
	}
	defer sampler.mu.Unlock()
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		out.System.MemoryTotal, out.System.MemoryAvailable = int64(vm.Total), int64(vm.Available)
	} else {
		fail(err)
		out.System.Error = err.Error()
	}
	self := int32(os.Getpid())
	tree, err := descendantsContext(ctx, self)
	out.TreeComplete = err == nil
	fail(err)
	out.ProcessCount = len(tree) + 1
	if times, err := cpu.TimesWithContext(ctx, false); err == nil && len(times) == 1 {
		t := times[0]
		busy := t.User + t.Nice + t.System + t.Irq + t.Softirq + t.Steal
		total := busy + t.Idle + t.Iowait
		out.System.SampledAt = time.Now()
		out.System.CPUStatus = "warming_up"
		if sampler.system.set && total > sampler.system.total && busy >= sampler.system.busy {
			out.System.CPU = clampPercent((busy-sampler.system.busy)/(total-sampler.system.total)*100, 100)
			out.System.PreviousAt = sampler.system.at
			out.System.CPUStatus = "available"
		}
		sampler.system.busy, sampler.system.total, sampler.system.set, sampler.system.at = busy, total, true, out.System.SampledAt
	} else if err != nil {
		fail(err)
		out.System.Error = err.Error()
	}
	seen := make(map[int32]bool, len(tree)+1)
	read := func(pid, parent int32) ProcessUsage {
		usage := ProcessUsage{PID: pid, Parent: parent, Memory: -1, CPU: -1, CPUStatus: "unavailable"}
		seen[pid] = true
		p := &gproc.Process{Pid: pid}
		var errors []string
		created, identityErr := p.CreateTimeWithContext(ctx)
		if identityErr != nil {
			errors = append(errors, "creation identity: "+identityErr.Error())
		}
		usage.CreatedMS = created
		if last, ok := sampler.processes[pid]; !ok || created == 0 || last.created != created {
			delete(sampler.names, pid)
			delete(sampler.processes, pid)
		}
		if info, err := p.MemoryInfoWithContext(ctx); err == nil {
			usage.Memory = int64(info.RSS)
		} else {
			errors = append(errors, "memory: "+err.Error())
		}
		if times, err := p.TimesWithContext(ctx); err == nil {
			sample := cpuSample{busy: times.User + times.System, at: time.Now(), created: created}
			usage.SampledAt = sample.at
			usage.CPU, usage.PreviousAt = processCPU(sampler.processes[pid], sample)
			usage.CPUStatus = "warming_up"
			if usage.CPU >= 0 {
				usage.CPUStatus = "available"
			}
			if created == 0 {
				usage.CPUStatus = "identity_unavailable"
			}
			sampler.processes[pid] = sample
		} else {
			errors = append(errors, "cpu: "+err.Error())
		}
		name, ok := sampler.names[pid]
		if !ok {
			var nameErr error
			name, nameErr = p.NameWithContext(ctx)
			if nameErr != nil {
				errors = append(errors, "name: "+nameErr.Error())
			} else {
				sampler.names[pid] = name
			}
		}
		usage.Name = name
		usage.Error = strings.Join(errors, "; ")
		if usage.Error != "" {
			out.Complete = false
		}
		return usage
	}
	if ctx.Err() == nil {
		out.Processes = append(out.Processes, read(self, 0))
	}
	children := make([]ProcessUsage, 0, len(tree))
	for _, node := range tree {
		if ctx.Err() != nil {
			break
		}
		children = append(children, read(node.pid, node.parent))
	}
	out.Processes = append(out.Processes, children...)
	for _, p := range out.Processes {
		if p.Memory >= 0 {
			out.MemoryKnown++
			out.MemoryKnownBytes += p.Memory
		}
		if p.CPU >= 0 {
			out.CPUKnown++
			out.CPUKnownPercent += p.CPU
		}
	}
	slices.SortStableFunc(children, func(a, b ProcessUsage) int {
		if a.Memory > b.Memory {
			return -1
		}
		if a.Memory < b.Memory {
			return 1
		}
		if a.PID < b.PID {
			return -1
		}
		if a.PID > b.PID {
			return 1
		}
		return 0
	})
	if !all && len(children) > processLimit {
		children = children[:processLimit]
	}
	if len(out.Processes) > 0 {
		out.Processes = append(out.Processes[:1], children...)
	}
	out.ProcessesListed = len(out.Processes)
	out.ProcessesOmitted = out.ProcessCount - out.ProcessesListed
	for pid := range sampler.processes {
		if !seen[pid] {
			delete(sampler.processes, pid)
			delete(sampler.names, pid)
		}
	}
	diskMu.Lock()
	source := diskSource
	diskMu.Unlock()
	if source != nil && ctx.Err() == nil {
		out.Disk = source() // owner contract: prompt, latest immutable value; no synchronous scan
		if out.Disk != nil {
			copy := *out.Disk
			copy.Parts = slices.Clone(copy.Parts)
			copy.Workspaces = slices.Clone(copy.Workspaces)
			out.Disk = &copy
			out.WorkspaceListed = len(copy.Workspaces)
			out.WorkspaceOmitted = max(copy.WorkspaceCount-len(copy.Workspaces), 0)
			switch {
			case copy.Err != "":
				out.DiskStatus = "error"
				out.Complete = false
			case copy.MeasuredAt.IsZero():
				out.DiskStatus = "warming_up"
			case out.WorkspaceOmitted > 0:
				out.DiskStatus = "partial"
			default:
				out.DiskStatus = "available"
			}
		}
	}
	fail(ctx.Err())
	out.MemoryComplete = out.TreeComplete && out.MemoryKnown == out.ProcessCount
	out.CPUComplete = out.TreeComplete && out.CPUKnown == out.ProcessCount
	out.Complete = out.Complete && out.MemoryComplete && out.CPUComplete && out.System.CPU >= 0 && out.System.MemoryTotal >= 0 && out.System.MemoryAvailable >= 0
	out.Status = "partial"
	if out.Complete {
		out.Status = "available"
	} else if out.TreeComplete && out.MemoryComplete && out.CPUKnown == 0 && len(out.Errors) == 0 {
		out.Status = "warming_up"
	}
	if out.ProcessesOmitted > 0 || out.DiskStatus == "partial" || out.DiskStatus == "error" {
		out.Complete = false
		out.Status = "partial"
	}
	out.Completed = time.Now()
	return out
}

// CPU baselines are valid only for the same OS process creation identity.
func processCPU(last, current cpuSample) (float64, time.Time) {
	if current.created == 0 || last.created != current.created || !current.at.After(last.at) || current.busy < last.busy {
		return -1, time.Time{}
	}
	return (current.busy - last.busy) / current.at.Sub(last.at).Seconds() * 100, last.at
}

func clampPercent(v, limit float64) float64 {
	return min(max(v, 0), limit)
}

type pidParent struct{ pid, parent int32 }

// descendantsContext lists every process below root, parents before children.
func descendantsContext(ctx context.Context, root int32) ([]pidParent, error) {
	parents, err := processParents(ctx)
	treeErr := err
	children := make(map[int32][]int32)
	for pid, parent := range parents {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if pid != parent {
			children[parent] = append(children[parent], pid)
		}
	}
	var out []pidParent
	queue := []int32{root}
	visited := map[int32]bool{root: true}
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return out, err
		}
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
	return out, treeErr
}
