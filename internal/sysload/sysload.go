// Package sysload observes the machine so that background work can yield to
// the user. It follows established designs instead of fixed load numbers:
//
//   - Pressure, not utilization (Linux PSI, docs.kernel.org/accounting/psi):
//     the share of time in which tasks wait for CPU, memory or I/O. A busy but
//     uncontended machine is not a reason to wait; contention is. The busy
//     threshold is systemd's default pressure threshold, 200 ms per 2 s
//     (systemd.io/PRESSURE).
//   - Without PSI, a background task that needs one core waits until at least
//     one core is idle.
//   - Battery power pauses background work, as KDE Baloo does for content
//     indexing (src/file/fileindexscheduler.cpp, LowPowerIdle).
//
// A Probe does file or system-call I/O. Call it off the UI goroutine.
package sysload

import (
	"runtime"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
)

// PressureThreshold is the share of stalled time that marks a resource busy.
const PressureThreshold = 0.10

// minWindow is the shortest interval over which a pressure share is
// meaningful; a shorter sample keeps the previous figures. A baseline older
// than maxWindow would average a current spike away, so it starts again:
// systemd measures its pressure threshold per 2 s window.
const (
	minWindow = 250 * time.Millisecond
	maxWindow = 2 * time.Second
)

// Sample is one observation. Unknown figures are negative or not Known.
type Sample struct {
	// Power: OnBattery is meaningful only when PowerKnown.
	PowerKnown, OnBattery bool
	// Stall shares since the previous sample (0..1), -1 when unknown.
	CPUPressure, MemoryPressure, IOPressure float64
	// IdleCores is the idle CPU capacity in cores, -1 when unknown. It is
	// measured only where pressure is not available.
	IdleCores float64
	// AvailableMemory is what the OS can give a new process without
	// swapping, in bytes; valid when MemoryKnown.
	MemoryKnown     bool
	AvailableMemory uint64
	// Warming means a load source exists but has no interval yet: the first
	// sample only sets the baseline. Background work waits for the next one.
	Warming bool
}

// Busy reports contention for CPU, memory or I/O.
func (s Sample) Busy() bool {
	for _, p := range []float64{s.CPUPressure, s.MemoryPressure, s.IOPressure} {
		if p >= PressureThreshold {
			return true
		}
	}
	return s.CPUPressure < 0 && s.IdleCores >= 0 && s.IdleCores < 1
}

// Probe keeps the previous counters so that each sample covers the time
// since the last one.
type Probe struct {
	mu       sync.Mutex
	at       time.Time
	stalls   [3]float64 // cumulative stall microseconds; -1 unknown
	cpuIdle  float64
	cpuTotal float64
	last     Sample
	sampled  bool
}

// NewProbe returns a probe; the first Sample has unknown shares.
func NewProbe() *Probe { return &Probe{} }

// Rebase starts a new measurement interval now. Background work calls it
// when it stops loading the computer, so that the next Sample measures only
// the time without it: its own load is not a reason to wait.
func (p *Probe) Rebase() {
	p.mu.Lock()
	defer p.mu.Unlock()
	stalls, idle, total := readCounters()
	p.at, p.stalls, p.cpuIdle, p.cpuTotal, p.sampled = time.Now(), stalls, idle, total, true
	p.last = Sample{CPUPressure: -1, MemoryPressure: -1, IOPressure: -1, IdleCores: -1,
		Warming: stalls[0] >= 0 || stalls[1] >= 0 || stalls[2] >= 0 || total >= 0}
}

// RecentContention reports whether any resource had at least
// PressureThreshold stalled time over the kernel's last ten seconds (PSI
// avg10). known is false where the system has no pressure interface.
func RecentContention() (contended, known bool) {
	for _, share := range readRecent() {
		if share >= 0 {
			known = true
			contended = contended || share >= PressureThreshold
		}
	}
	return contended, known
}

// Sample observes the machine now.
func (p *Probe) Sample() Sample {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	out := Sample{CPUPressure: -1, MemoryPressure: -1, IOPressure: -1, IdleCores: -1}
	out.PowerKnown, out.OnBattery = powerState()
	if vm, err := mem.VirtualMemory(); err == nil && vm.Total > 0 {
		out.MemoryKnown, out.AvailableMemory = true, vm.Available
	}
	stalls, idle, total := readCounters()
	elapsed := now.Sub(p.at)
	if elapsed > maxWindow {
		p.sampled = false
	}
	if !p.sampled {
		out.Warming = stalls[0] >= 0 || stalls[1] >= 0 || stalls[2] >= 0 || total >= 0
	}
	if p.sampled && elapsed < minWindow {
		// Too short to measure; keep the previous shares and counters. Right
		// after a baseline there are no shares yet, so it is still warming.
		out.CPUPressure, out.MemoryPressure, out.IOPressure = p.last.CPUPressure, p.last.MemoryPressure, p.last.IOPressure
		out.IdleCores, out.Warming = p.last.IdleCores, p.last.Warming
		return out
	}
	if p.sampled {
		window := float64(elapsed.Microseconds())
		shares := []*float64{&out.CPUPressure, &out.MemoryPressure, &out.IOPressure}
		for i, share := range shares {
			if stalls[i] >= 0 && p.stalls[i] >= 0 && stalls[i] >= p.stalls[i] {
				*share = min(1, (stalls[i]-p.stalls[i])/window)
			}
		}
		if total > p.cpuTotal && p.cpuTotal >= 0 && idle >= p.cpuIdle {
			out.IdleCores = (idle - p.cpuIdle) / (total - p.cpuTotal) * float64(runtime.NumCPU())
		}
	}
	p.at, p.stalls, p.cpuIdle, p.cpuTotal, p.last, p.sampled = now, stalls, idle, total, out, true
	return out
}

// readCounters reads the cumulative stall times, or the CPU idle and total
// times where the system has no pressure interface (-1 when unknown).
func readCounters() (stalls [3]float64, idle, total float64) {
	stalls, idle, total = readStalls(), -1, -1
	if stalls[0] < 0 {
		if times, err := cpu.Times(false); err == nil && len(times) == 1 {
			t := times[0]
			idle = t.Idle + t.Iowait
			total = idle + t.User + t.System + t.Nice + t.Irq + t.Softirq + t.Steal
		}
	}
	return stalls, idle, total
}
