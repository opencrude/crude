package perfdata

import (
	"strings"
	"testing"
	"time"

	"crude/metrics"
)

func resourceSnapshot(boot string, pid int32, memory int64) metrics.Snapshot {
	return metrics.Snapshot{Taken: time.Now(), HostIdentity: "boot_id", HostBootID: boot, Hostname: "same-name", Resources: &metrics.Resources{
		TreeComplete: true, Processes: []metrics.ProcessUsage{{PID: pid, CreatedMS: 100, Memory: memory, CPU: 5}},
		System: metrics.SystemUsage{MemoryTotal: 1000, MemoryAvailable: 500, CPU: 10},
	}}
}
func TestHostIdentityAndLifetimeRequiredForDeduplication(t *testing.T) {
	for _, tc := range []struct {
		name, shellBoot, coreBoot string
		unknown, missingBirth     bool
		wantViews                 int
	}{
		{name: "proven", shellBoot: "boot", coreBoot: "boot", wantViews: 1},
		{name: "different", shellBoot: "a", coreBoot: "b", wantViews: 2},
		{name: "empty", wantViews: 2},
		{name: "unknown", shellBoot: "boot", coreBoot: "boot", unknown: true, wantViews: 2},
		{name: "unknown lifetime", shellBoot: "boot", coreBoot: "boot", missingBirth: true, wantViews: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shell, core := resourceSnapshot(tc.shellBoot, 10, 100), resourceSnapshot(tc.coreBoot, 10, 200)
			if tc.unknown {
				shell.HostIdentity = "unknown"
			}
			if tc.missingBirth {
				core.Resources.Processes[0].CreatedMS = 0
			}
			views := ResourceViews(Data{Shell: shell, Core: &core})
			if len(views) != tc.wantViews {
				t.Fatalf("views=%+v", views)
			}
			if tc.wantViews == 2 {
				if views[0].Label != "Shell" || views[0].Memory != 100 || views[1].Label != "Core" || views[1].Memory != 200 {
					t.Fatalf("combined cross-host resources: %+v", views)
				}
			} else if views[0].Memory != 200 || len(views[0].Processes) != 1 {
				t.Fatalf("core not deduplicated: %+v", views)
			}
		})
	}
}
func TestReusedPIDNeverDeduplicated(t *testing.T) {
	shell, core := resourceSnapshot("boot", 10, 100), resourceSnapshot("boot", 10, 200)
	core.Resources.Processes[0].CreatedMS = 200
	views := ResourceViews(Data{Shell: shell, Core: &core})
	count := 0
	for _, v := range views {
		count += len(v.Processes)
	}
	if count != 2 {
		t.Fatalf("reused PID lost: %+v", views)
	}
}

func TestNestedProcessRootsKeepTheirRoleOnce(t *testing.T) {
	for _, parentRole := range []string{"shell", "core"} {
		shell, core := resourceSnapshot("boot", 10, 100), resourceSnapshot("boot", 20, 200)
		if parentRole == "core" {
			core.Resources.Processes = append(core.Resources.Processes, shell.Resources.Processes[0])
		} else {
			shell.Resources.Processes = append(shell.Resources.Processes, core.Resources.Processes[0])
		}
		views := ResourceViews(Data{Shell: shell, Core: &core})
		if len(views) != 1 || len(views[0].Processes) != 2 || views[0].Memory != 300 {
			t.Fatalf("bad nested totals %s: %+v", parentRole, views)
		}
		roles := map[string]int{}
		for _, p := range views[0].Processes {
			roles[p.Role]++
		}
		if roles["Shell"] != 1 || roles["Core"] != 1 {
			t.Fatalf("root roles lost %s: %+v", parentRole, roles)
		}
	}
}
func TestPartialResourceTotalsAndWarmupNeverClaimZero(t *testing.T) {
	s := resourceSnapshot("boot", 10, -1)
	r := s.Resources
	r.Processes[0].CPU = -1
	r.Processes[0].CPUStatus = "warming_up"
	r.ProcessCount = 40
	r.ProcessesOmitted = 39
	r.MemoryKnown = 0
	r.CPUKnown = 0
	v := ResourceViews(Data{Shell: s})[0]
	if v.MemoryComplete || v.CPUComplete {
		t.Fatalf("partial totals claimed complete: %+v", v)
	}
	h := ResourceHeadlines(v)
	if h[0].Value != "unknown" || h[0].Severity != SeverityUnknown || !strings.Contains(h[1].Value, "warming up") {
		t.Fatalf("headlines=%+v", h)
	}
	// All-process totals from the sampler must not become top-row-only totals.
	r.MemoryKnown = 40
	r.MemoryKnownBytes = 9000
	r.CPUKnown = 40
	r.CPUKnownPercent = 30
	v = ResourceViews(Data{Shell: s})[0]
	if v.Memory != 9000 || v.CPU != 30 || !v.MemoryComplete || !v.CPUComplete {
		t.Fatalf("omitted rows lost authoritative totals: %+v", v)
	}
}
func TestPressureFindingsKeepBothHostOwners(t *testing.T) {
	shell, core := resourceSnapshot("shell", 10, 100), resourceSnapshot("core", 10, 200)
	shell.Resources.System.MemoryAvailable = 20
	core.Resources.Disk = &metrics.DiskUsage{DeviceTotal: 1000, DeviceFree: 30, MeasuredAt: time.Now()}
	findings := ResourceFindings(Data{Shell: shell, Core: &core})
	var memory, disk bool
	for _, f := range findings {
		if f.Title == "Memory is running out" {
			memory = strings.HasPrefix(f.Detail, "Shell:") && f.Severity == SeverityCrit
		}
		if f.Title == "Disk almost full" {
			disk = strings.HasPrefix(f.Detail, "Core:") && f.Severity == SeverityCrit
		}
	}
	if !memory || !disk {
		t.Fatalf("lost owner-specific pressure: %+v", findings)
	}
}
