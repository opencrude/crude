package metrics

import (
	"strings"
	"testing"
	"time"
)

// stalledUIWorkForTest stands for an Update that blocks the UI goroutine.
func stalledUIWorkForTest(release <-chan struct{}) { <-release }

// decoyWorkForTest blocks another goroutine the same way, so the capture
// must find the UI goroutine by identity, not by what it waits on.
func decoyWorkForTest(release <-chan struct{}) { <-release }

func uiLoopForTest(release <-chan struct{}, quick, stalled chan<- []string) {
	MarkUIGoroutine()
	BeginUIWork()
	_, stack := EndUIWork()
	quick <- stack.Stack
	BeginUIWork()
	stalledUIWorkForTest(release)
	_, stack = EndUIWork()
	stalled <- stack.Stack
}

func TestUIStallRecordsTheStalledStackAndArchivesIt(t *testing.T) {
	ResetForTest()
	ResetUIGoroutineForTest()
	t.Cleanup(ResetUIGoroutineForTest)
	release := make(chan struct{})
	for range 4 {
		go decoyWorkForTest(release)
	}
	quick, stalled := make(chan []string, 1), make(chan []string, 1)
	go uiLoopForTest(release, quick, stalled)
	if stack := <-quick; stack != nil {
		t.Fatalf("an iteration without a stall recorded %v", stack)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		uiWork.mu.Lock()
		captured := uiWork.evidence.Stack != nil
		uiWork.mu.Unlock()
		if captured || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	stack := <-stalled
	if !strings.Contains(strings.Join(stack, "\n"), "metrics.stalledUIWorkForTest (stall_test.go:") {
		t.Fatalf("stalled UI stack was not captured: %q", stack)
	}
	if strings.Contains(strings.Join(stack, "\n"), "decoyWorkForTest") {
		t.Fatalf("captured another goroutine: %q", stack)
	}

	// The stack travels with the stall event into the archived evidence.
	series := NewLatency(Meta{Name: "test.stall", Group: GroupUI, Title: "Test stall", Crit: 1_000})
	series.ObserveStack(60*time.Millisecond, "test message", stack)
	window := CaptureArchiveWindow(now().Unix()-1, now().Unix()+1)
	found := false
	for _, s := range window.Stalls {
		found = found || s.Series == "test.stall" && strings.Join(s.Stack, "\n") == strings.Join(stack, "\n")
	}
	if !found {
		t.Fatalf("archived stalls lost the stack: %+v", window.Stalls)
	}
}

func TestUIStackPicksTheGoroutineSharingTheLoopFrames(t *testing.T) {
	profile := []byte("goroutine profile: total 3\n" +
		"1 @ 0x1 0x2\n#\t0x1\truntime.gopark+0x10\tC:/go/src/runtime/proc.go:460\n#\t0x2\tother.worker+0x20\tC:/src/other.go:9\n\n" +
		"1 @ 0x3 0x4 0x5\n#\t0x3\tsync.(*Mutex).Lock+0x30\t\t\tC:/Program Files/Go/src/sync/mutex.go:90\n" +
		"#\t0x4\ttea.(*Program).eventLoop+0x40\tC:\\src\\tea.go:950\n#\t0x5\tmain.main+0x50\tC:/src/main.go:12\n\n")
	signature := map[string]struct{}{"tea.(*Program).eventLoop": {}, "main.main": {}}
	got := uiStack(profile, signature)
	want := []string{"sync.(*Mutex).Lock (mutex.go:90)", "tea.(*Program).eventLoop (tea.go:950)", "main.main (main.go:12)"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("uiStack = %q, want %q", got, want)
	}
	// Two equally good matches identify nothing.
	tied := append([]byte("1 @ 0x6\n#\t0x6\tmain.main+0x50\tC:/src/main.go:12\n\n"), profile...)
	if got := uiStack(tied, map[string]struct{}{"main.main": {}}); got != nil {
		t.Fatalf("an ambiguous match returned %q", got)
	}
}

// A stall reads only the sampler's published evidence. It must keep the
// original interval when more samples arrive and before archival takes place.
func TestStallContextIsRetainedWithoutObservationAllocations(t *testing.T) {
	wall := time.Unix(1_800_000_000, 0)
	clock := fixedClock(t, wall)
	prior := latestStallContext.Load()
	t.Cleanup(func() { latestStallContext.Store(prior) })
	context := &StallContext{From: wall.Add(-time.Second), Until: wall.Add(-10 * time.Millisecond),
		ProcessCPUPercent: 80, HostCPUPercent: 76, SchedulerMaxMicros: 10_000, GCPauseMaxMicros: 4_000, GCAssistCPUMicros: 2_000}
	latestStallContext.Store(context)
	series := NewLatency(Meta{Name: "test.stall.context", Crit: 50_000})
	if got := testing.AllocsPerRun(100, func() { series.Observe(70 * time.Millisecond) }); got != 0 {
		t.Fatalf("stall observation allocated %.1f times", got)
	}
	latestStallContext.Store(&StallContext{From: wall, Until: wall.Add(time.Second)})
	window := CaptureArchiveWindow(wall.Unix(), wall.Unix()+1)
	if len(window.Stalls) == 0 || window.Stalls[0].Context == nil || *window.Stalls[0].Context != *context {
		t.Fatal("archive lost or replaced the stall's contemporaneous context")
	}
	latestStallContext.Store(context)
	*clock = wall.Add(4 * time.Second)
	series.Observe(70 * time.Millisecond)
	if ev := EventsSince(*clock); len(ev) != 1 || ev[0].Context != nil {
		t.Fatalf("stale sample fabricated for a new stall: %+v", ev)
	}
}
