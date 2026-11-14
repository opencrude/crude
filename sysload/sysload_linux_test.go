package sysload

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fakeRoots(t *testing.T) (pressure, power string) {
	t.Helper()
	dir := t.TempDir()
	oldPressure, oldPower := pressureRoot, powerRoot
	pressureRoot, powerRoot = filepath.Join(dir, "pressure"), filepath.Join(dir, "power")
	t.Cleanup(func() { pressureRoot, powerRoot = oldPressure, oldPower })
	return pressureRoot, powerRoot
}

func psi(total int) string {
	return "some avg10=0.00 avg60=0.00 avg300=0.00 total=" + strconv.Itoa(total) + "\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n"
}

// Contention, not utilization, makes the machine busy: a stall share at or
// above the systemd default threshold on any resource.
func TestPressureShareSinceTheLastSample(t *testing.T) {
	pressure, _ := fakeRoots(t)
	for _, name := range []string{"cpu", "memory", "io"} {
		writeFile(t, filepath.Join(pressure, name), psi(0))
	}
	probe := NewProbe()
	if first := probe.Sample(); first.CPUPressure != -1 || first.Busy() || !first.Warming {
		t.Fatalf("the first sample must only set the baseline: %+v", first)
	}
	time.Sleep(300 * time.Millisecond)
	// 5 ms of CPU stall in about 300 ms is quiet; 150 ms of I/O stall is not.
	writeFile(t, filepath.Join(pressure, "cpu"), psi(5000))
	writeFile(t, filepath.Join(pressure, "io"), psi(150000))
	sample := probe.Sample()
	if sample.CPUPressure <= 0 || sample.CPUPressure >= PressureThreshold {
		t.Fatalf("cpu share = %v", sample.CPUPressure)
	}
	if sample.IOPressure < PressureThreshold || !sample.Busy() {
		t.Fatalf("I/O contention not seen: %+v", sample)
	}
	time.Sleep(300 * time.Millisecond)
	if again := probe.Sample(); again.Busy() {
		t.Fatalf("pressure that stopped still counts: %+v", again)
	}
	// An old baseline would average a new spike away; it starts again.
	time.Sleep(maxWindow + 100*time.Millisecond)
	if stale := probe.Sample(); !stale.Warming || stale.CPUPressure != -1 {
		t.Fatalf("a baseline older than the window was used: %+v", stale)
	}
}

func TestPowerStateFromKernelSupplies(t *testing.T) {
	_, power := fakeRoots(t)
	if known, _ := powerState(); known {
		t.Fatal("no supplies must be unknown")
	}
	writeFile(t, filepath.Join(power, "ACAD", "type"), "Mains\n")
	writeFile(t, filepath.Join(power, "ACAD", "online"), "1\n")
	writeFile(t, filepath.Join(power, "BAT1", "type"), "Battery\n")
	writeFile(t, filepath.Join(power, "BAT1", "status"), "Full\n")
	if known, battery := powerState(); !known || battery {
		t.Fatalf("wall power = %v %v", known, battery)
	}
	writeFile(t, filepath.Join(power, "ACAD", "online"), "0\n")
	writeFile(t, filepath.Join(power, "BAT1", "status"), "Discharging\n")
	if known, battery := powerState(); !known || !battery {
		t.Fatalf("battery power = %v %v", known, battery)
	}
	// A wireless mouse battery is not the computer's battery.
	writeFile(t, filepath.Join(power, "BAT1", "status"), "Full\n")
	writeFile(t, filepath.Join(power, "hid-mouse", "type"), "Battery\n")
	writeFile(t, filepath.Join(power, "hid-mouse", "scope"), "Device\n")
	writeFile(t, filepath.Join(power, "hid-mouse", "status"), "Discharging\n")
	if _, battery := powerState(); battery {
		t.Fatal("a device battery paused the check")
	}
}

// Background work rebases the probe when it stops working, so the stall time
// that its own work caused before that moment is not counted as the
// computer's load; contention after it still is.
func TestRebaseExcludesEarlierStalls(t *testing.T) {
	pressure, _ := fakeRoots(t)
	for _, name := range []string{"cpu", "memory", "io"} {
		writeFile(t, filepath.Join(pressure, name), psi(0))
	}
	probe := NewProbe()
	probe.Sample()
	time.Sleep(300 * time.Millisecond)
	// Crude's own file check stalled the CPU heavily, then it stopped.
	writeFile(t, filepath.Join(pressure, "cpu"), psi(250000))
	probe.Rebase()
	if early := probe.Sample(); !early.Warming || early.Busy() {
		t.Fatalf("a sample right after the rebase must wait for an interval: %+v", early)
	}
	time.Sleep(300 * time.Millisecond)
	if quiet := probe.Sample(); quiet.Warming || quiet.CPUPressure != 0 || quiet.Busy() {
		t.Fatalf("stalls before the rebase counted: %+v", quiet)
	}
	time.Sleep(300 * time.Millisecond)
	writeFile(t, filepath.Join(pressure, "cpu"), psi(400000))
	if busy := probe.Sample(); !busy.Busy() {
		t.Fatalf("contention after the rebase was missed: %+v", busy)
	}
}

// The kernel's ten-second average covers one file's attempt.
func TestRecentContentionReadsTheTenSecondAverage(t *testing.T) {
	pressure, _ := fakeRoots(t)
	if _, known := RecentContention(); known {
		t.Fatal("a system without pressure files reported a value")
	}
	writeFile(t, filepath.Join(pressure, "cpu"), "some avg10=3.00 avg60=0.00 avg300=0.00 total=1\n")
	writeFile(t, filepath.Join(pressure, "io"), "some avg10=12.50 avg60=0.00 avg300=0.00 total=1\n")
	if contended, known := RecentContention(); !known || !contended {
		t.Fatalf("12.5%% I/O stall = %v %v", contended, known)
	}
	writeFile(t, filepath.Join(pressure, "io"), "some avg10=9.99 avg60=40.00 avg300=0.00 total=1\n")
	if contended, _ := RecentContention(); contended {
		t.Fatal("older pressure counted as recent")
	}
}
