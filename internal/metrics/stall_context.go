package metrics

import (
	"context"
	"math"
	"os"
	"runtime/metrics"
	"slices"
	"sync/atomic"
	"time"

	"crude/internal/buildinfo"

	"github.com/shirou/gopsutil/v4/cpu"
	gproc "github.com/shirou/gopsutil/v4/process"
)

// StallContext describes the most recent process-wide sample available when
// the stall ended. Its interval can precede the stall. A negative reading is
// unavailable, not zero. Histogram values are upper bucket bounds.
type StallContext struct {
	From               time.Time `json:"from"`
	Until              time.Time `json:"until"`
	ProcessCPUPercent  float64   `json:"process_cpu_pct"` // share of one core
	HostCPUPercent     float64   `json:"host_cpu_pct"`    // share of all logical cores
	SchedulerMaxMicros int64     `json:"scheduler_max_us"`
	GCPauseMaxMicros   int64     `json:"gc_pause_max_us"`
	GCAssistCPUMicros  int64     `json:"gc_assist_cpu_us"` // summed across goroutines, not elapsed UI time
}

var latestStallContext atomic.Pointer[StallContext]
var eventBuildID atomic.Value // string, set by the off-thread sampler

// cpuContextSampler only reads CPU counters. Process trees, paths, disks and
// memory stay demand sampled by the Resources owner.
type cpuContextSampler struct {
	self                 gproc.Process
	process, busy, total float64
	processAt            time.Time
	haveSystem           bool
}

func (s *cpuContextSampler) read() (process, system float64) {
	process, system = -1, -1
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if times, err := s.self.TimesWithContext(ctx); err == nil {
		at, used := time.Now(), times.User+times.System
		if wall := at.Sub(s.processAt).Seconds(); !s.processAt.IsZero() && wall > 0 && used >= s.process {
			process = (used - s.process) / wall * 100
		}
		s.process, s.processAt = used, at
	} else {
		s.processAt = time.Time{}
	}
	if times, err := cpu.TimesWithContext(ctx, false); err == nil && len(times) == 1 {
		busy, total := cpuTotals(times[0])
		if s.haveSystem && total > s.total && busy >= s.busy {
			system = clampPercent((busy-s.busy)/(total-s.total)*100, 100)
		}
		s.busy, s.total, s.haveSystem = busy, total, true
	} else {
		s.haveSystem = false
	}
	return process, system
}

func newCPUContextSampler() cpuContextSampler {
	return cpuContextSampler{self: gproc.Process{Pid: int32(os.Getpid())}}
}

func initEventBuildID() { eventBuildID.Store(buildinfo.ExecutableID()) }

// Attach an immutable, already measured sample. Do not call the OS, read
// runtime histograms or allocate from an observation.
func (e *Event) attachStallContext() {
	if build, ok := eventBuildID.Load().(string); ok {
		e.BuildID = build
	}
	if sample := latestStallContext.Load(); sample != nil {
		// An exceptionally delayed sampler should not lend stale context.
		if age := e.At.Sub(sample.Until); age >= 0 && age <= 3*time.Second {
			e.Context = sample
		}
	}
}

func histogramIntervalMax(sample metrics.Value, last *[]uint64) int64 {
	if sample.Kind() != metrics.KindFloat64Histogram {
		return -1
	}
	h := sample.Float64Histogram()
	if len(*last) != len(h.Counts) {
		*last = slices.Clone(h.Counts)
		return -1
	}
	var upper float64
	for i := len(h.Counts) - 1; i >= 0; i-- {
		if h.Counts[i] > (*last)[i] {
			upper = h.Buckets[i+1]
			if math.IsInf(upper, 1) {
				upper = h.Buckets[i]
			}
			break
		}
	}
	copy(*last, h.Counts)
	return int64(math.Ceil(upper * 1e6))
}
