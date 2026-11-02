package metrics

import (
	"context"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestResourceProcessHelper(t *testing.T) {
	if os.Getenv("CRUDE_RESOURCE_PROCESS_HELPER") != "1" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func resourceProcess(t *testing.T) *exec.Cmd {
	t.Helper()
	child := exec.Command(os.Args[0], "-test.run=^TestResourceProcessHelper$")
	child.Env = append(os.Environ(), "CRUDE_RESOURCE_PROCESS_HELPER=1")
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		_ = stdin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = child.Wait() })
	return child
}

// A snapshot reports this process first, the processes it started with
// their parent, the machine's memory, and CPU shares from the second
// snapshot on.
func TestResourcesReportThisProcessItsChildrenAndTheMachine(t *testing.T) {
	child := resourceProcess(t)
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
	for range processLimit + 3 {
		resourceProcess(t)
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
