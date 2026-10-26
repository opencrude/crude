package metrics

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

// A snapshot reports this process first, the processes it started with
// their parent, the machine's memory, and CPU shares from the second
// snapshot on.
func TestResourcesReportThisProcessItsChildrenAndTheMachine(t *testing.T) {
	name, args := "sleep", []string{"30"}
	if runtime.GOOS == "windows" {
		name, args = "ping", []string{"-n", "30", "127.0.0.1"}
	}
	child := exec.Command(name, args...)
	if err := child.Start(); err != nil {
		t.Skipf("cannot start a child process: %v", err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	SetDiskSource(func() *DiskUsage { return &DiskUsage{Path: "data", Total: 42} })
	t.Cleanup(func() { SetDiskSource(nil) })

	sampler.mu.Lock()
	sampler.processes, sampler.names, sampler.system.set = map[int32]cpuSample{}, map[int32]string{}, false
	sampler.mu.Unlock()
	first := takeResources()
	time.Sleep(50 * time.Millisecond)
	second := takeResources()
	if len(first.Processes) == 0 || first.Processes[0].PID != int32(os.Getpid()) || first.Processes[0].CPU != -1 {
		t.Fatalf("first snapshot = %+v", first.Processes)
	}
	self := second.Processes[0]
	if self.Memory <= 0 || self.CPU < 0 || self.Name == "" {
		t.Fatalf("this process = %+v", self)
	}
	var found bool
	for _, p := range second.Processes[1:] {
		if p.PID == int32(child.Process.Pid) {
			found = p.Parent == int32(os.Getpid()) && p.Memory > 0
		}
	}
	if !found {
		t.Fatalf("started process %d missing or wrong: %+v", child.Process.Pid, second.Processes)
	}
	if second.System.MemoryTotal <= 0 || second.System.MemoryAvailable <= 0 || second.System.CPUs <= 0 {
		t.Fatalf("system = %+v", second.System)
	}
	if second.Disk == nil || second.Disk.Total != 42 {
		t.Fatalf("disk source not reported: %+v", second.Disk)
	}
}

func TestProcessCPURejectsReusedPIDAndUnknownIdentity(t *testing.T) {
	at := time.Unix(100, 0)
	last := cpuSample{busy: 3, at: at, created: 1000}
	current := cpuSample{busy: 4, at: at.Add(2 * time.Second), created: 1000}
	value, previous := processCPU(last, current)
	if value != 50 || !previous.Equal(at) {
		t.Fatal("valid interval", value, previous)
	}
	current.created = 2000
	if value, _ := processCPU(last, current); value != -1 {
		t.Fatal("PID reuse inherited CPU baseline", value)
	}
	current.created = 0
	if value, _ := processCPU(last, current); value != -1 {
		t.Fatal("unknown identity treated known", value)
	}
	current.created = 1000
	current.busy = 2
	if value, _ := processCPU(last, current); value != -1 {
		t.Fatal("counter rollback treated known", value)
	}
}

func TestResourceWaitingCancellationAndUnknowns(t *testing.T) {
	sampler.mu.Lock()
	defer sampler.mu.Unlock()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan *Resources, 1)
	go func() { done <- takeResourcesContext(ctx, true) }()
	cancel()
	select {
	case r := <-done:
		if r.Complete || r.System.MemoryTotal != -1 || r.System.MemoryAvailable != -1 || r.System.CPU != -1 || r.Status != "unavailable" || len(r.Errors) == 0 {
			t.Fatalf("cancelled read fabricated figures %+v", r)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation blocked behind sampler")
	}
}

func TestResourcesRetainEveryDescendantAndCountDiskSourceOmissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("large real process tree uses POSIX sleep; Windows cross-compilation checked separately")
	}
	for range processLimit + 3 {
		child := exec.Command("sleep", "30")
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	}
	SetDiskSource(func() *DiskUsage {
		return &DiskUsage{Path: "fixture", Total: 42, MeasuredAt: time.Now(), WorkspaceCount: 50, Workspaces: []WorkspaceDisk{{ID: "one", Bytes: 42}}}
	})
	t.Cleanup(func() { SetDiskSource(nil) })
	r := takeResourcesContext(t.Context(), true)
	if r.ProcessCount < processLimit+4 || r.ProcessesListed != r.ProcessCount || r.ProcessesOmitted != 0 {
		t.Fatalf("diagnostic process source truncated %+v", r)
	}
	known, bytes := 0, int64(0)
	for _, p := range r.Processes {
		if p.Memory >= 0 {
			known++
			bytes += p.Memory
		}
	}
	if r.MemoryKnown != known || r.MemoryKnownBytes != bytes {
		t.Fatal("known total excludes rows", r.MemoryKnown, r.MemoryKnownBytes, known, bytes)
	}
	if r.WorkspaceListed != 1 || r.WorkspaceOmitted != 49 || r.DiskStatus != "partial" || r.Complete {
		t.Fatalf("disk source truncation hidden %+v", r)
	}
	dashboard := takeResourcesContext(t.Context(), false)
	if len(dashboard.Processes) != processLimit+1 || dashboard.ProcessesOmitted < 3 || dashboard.MemoryKnown < len(dashboard.Processes) {
		t.Fatalf("dashboard totals lost omitted children %+v", dashboard)
	}
}
