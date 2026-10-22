package metrics

import (
	"math"
	"runtime"
	"runtime/metrics"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

var (
	processStart = time.Now()
	process      atomic.Value // string
	startOnce    sync.Once
)

func processName() string {
	if name, ok := process.Load().(string); ok {
		return name
	}
	return "process"
}

// Group names shared by every recorder, so the dashboard sections line up.
const (
	GroupUI      = "UI thread"
	GroupRender  = "Rendering"
	GroupRPC     = "Core link"
	GroupCore    = "Core"
	GroupAgents  = "Agents"
	GroupTools   = "Tools"
	GroupLSP     = "Language servers"
	GroupFiles   = "Files & search"
	GroupGit     = "Git"
	GroupStorage = "Storage"
	GroupRuntime = "Go runtime"
)

var (
	runtimeGoroutines = NewGauge(Meta{Name: "runtime.goroutines", Group: GroupRuntime, Title: "Goroutines", Unit: "items",
		Help: "Live goroutines. A steady climb without matching work is a leak."}, nil)
	runtimeHeap = NewGauge(Meta{Name: "runtime.heap", Group: GroupRuntime, Title: "Live heap", Unit: "bytes",
		Help: "Memory occupied by live and not-yet-collected heap objects."}, nil)
	runtimeMemory = NewGauge(Meta{Name: "runtime.memory", Group: GroupRuntime, Title: "Process memory", Unit: "bytes",
		Help: "Memory mapped by the Go runtime (heap, stacks, metadata)."}, nil)
	runtimeCPU = NewGauge(Meta{Name: "runtime.cpu", Group: GroupRuntime, Title: "CPU", Unit: "%",
		Help: "Go CPU time over wall time, 100% = one core.", Warn: 80, Crit: 150}, nil)
	runtimeGCPause = NewGauge(Meta{Name: "runtime.gc.pause", Group: GroupRuntime, Title: "Worst GC pause", Unit: "µs",
		Help: "Longest stop-the-world pause in the second; every pause also stalls the UI thread.", Warn: 2_000, Crit: 10_000}, nil)
	runtimeGCCycles = NewCounter(Meta{Name: "runtime.gc.cycles", Group: GroupRuntime, Title: "GC cycles", Unit: "cycles",
		Help: "Completed collections. Many per second means allocation churn."})
	runtimeAllocs = NewGauge(Meta{Name: "runtime.alloc", Group: GroupRuntime, Title: "Allocation rate", Unit: "bytes/s",
		Help: "Bytes allocated per second; the driver of GC work."}, nil)
)

// Start names the process and starts the one-hertz runtime sampler. Later
// calls only keep the first name.
func Start(name string) {
	startOnce.Do(func() {
		process.Store(name)
		go sampleLoop()
	})
}

var runtimeSamples = []metrics.Sample{
	{Name: "/sched/goroutines:goroutines"},
	{Name: "/memory/classes/heap/objects:bytes"},
	{Name: "/memory/classes/total:bytes"},
	{Name: "/cpu/classes/total:cpu-seconds"},
	{Name: "/cpu/classes/idle:cpu-seconds"},
	{Name: "/gc/cycles/total:gc-cycles"},
	{Name: "/gc/heap/allocs:bytes"},
	{Name: "/sched/pauses/total/gc:seconds"},
}

func sampleLoop() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	samples := slices.Clone(runtimeSamples)
	var lastCPU, lastIdle, lastCycles, lastAllocs float64
	var lastPauses []uint64
	lastAt := time.Now()
	first := true
	for at := range tick.C {
		metrics.Read(samples)
		value := func(i int) float64 {
			switch samples[i].Value.Kind() {
			case metrics.KindUint64:
				return float64(samples[i].Value.Uint64())
			case metrics.KindFloat64:
				return samples[i].Value.Float64()
			}
			return 0
		}
		runtimeGoroutines.Set(int64(value(0)))
		runtimeHeap.Set(int64(value(1)))
		runtimeMemory.Set(int64(value(2)))
		cpu, idle, cycles, allocs := value(3), value(4), value(5), value(6)
		var pause int64
		if samples[7].Value.Kind() == metrics.KindFloat64Histogram {
			h := samples[7].Value.Float64Histogram()
			if lastPauses != nil && len(lastPauses) == len(h.Counts) {
				for i := len(h.Counts) - 1; i >= 0; i-- {
					if h.Counts[i] > lastPauses[i] {
						upper := h.Buckets[i+1]
						if math.IsInf(upper, 1) {
							upper = h.Buckets[i]
						}
						pause = int64(upper * 1e6)
						break
					}
				}
			}
			lastPauses = slices.Clone(h.Counts)
		}
		if !first {
			wall := at.Sub(lastAt).Seconds()
			if wall > 0 {
				busy := (cpu - lastCPU) - (idle - lastIdle)
				runtimeCPU.Set(int64(busy / wall * 100))
				runtimeAllocs.Set(int64((allocs - lastAllocs) / wall))
			}
			if delta := cycles - lastCycles; delta > 0 {
				runtimeGCCycles.Add(uint64(delta))
			}
			runtimeGCPause.Set(pause)
		}
		first = false
		lastCPU, lastIdle, lastCycles, lastAllocs, lastAt = cpu, idle, cycles, allocs, at
		sampleGauges()
	}
}

func sampleGauges() {
	reg.mu.Lock()
	gauges := slices.Clone(reg.gauges)
	reg.mu.Unlock()
	for _, g := range gauges {
		g.mu.Lock()
		sample := g.sample
		g.mu.Unlock()
		if sample != nil {
			g.Set(sample())
		}
	}
}

// --- UI goroutine contract -------------------------------------------------

var uiGoroutine atomic.Uint64

var uiBlocking = NewCounter(Meta{Name: "ui.blocking", Group: GroupUI, Title: "Blocking calls on UI thread",
	Help: "Core requests, file, Git or database I/O performed on the UI goroutine. Any nonzero value is a defect: input waits for it."})

// MarkUIGoroutine records the calling goroutine as the UI goroutine. The
// shell calls it from its update loop; only the first call binds.
func MarkUIGoroutine() {
	if uiGoroutine.Load() == 0 {
		uiGoroutine.CompareAndSwap(0, goroutineID())
	}
}

// OnUIGoroutine reports whether the caller is the bound UI goroutine.
func OnUIGoroutine() bool {
	id := uiGoroutine.Load()
	return id != 0 && goroutineID() == id
}

// Blocking records a blocking operation that ran on the UI goroutine. Call
// it after any I/O whose duration is known; it is a no-op elsewhere.
func Blocking(kind, label string, d time.Duration) {
	if uiGoroutine.Load() == 0 || !OnUIGoroutine() {
		return
	}
	uiBlocking.Inc()
	recordEvent(Event{At: now(), Group: GroupUI, Series: "ui.blocking", Title: "Blocking " + kind + " on UI thread", Label: label,
		Micros: int64(d / time.Microsecond), Severity: "crit"})
}

// ResetUIGoroutineForTest unbinds the UI goroutine.
func ResetUIGoroutineForTest() { uiGoroutine.Store(0) }

func goroutineID() uint64 {
	var buf [40]byte
	n := runtime.Stack(buf[:], false)
	// "goroutine 123 [running]:..."
	const prefix = len("goroutine ")
	if n <= prefix {
		return 0
	}
	end := prefix
	for end < n && buf[end] >= '0' && buf[end] <= '9' {
		end++
	}
	id, _ := strconv.ParseUint(string(buf[prefix:end]), 10, 64)
	return id
}
