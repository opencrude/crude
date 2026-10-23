package metrics

import (
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
