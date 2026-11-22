package sysload

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Roots are variables so that tests can supply a fake /proc and /sys.
var (
	pressureRoot = "/proc/pressure"
	powerRoot    = "/sys/class/power_supply"
)

// readStalls returns the cumulative "some" stall time of cpu, memory and io in
// microseconds, -1 for each resource the kernel does not report.
func readStalls() [3]float64 {
	out := [3]float64{-1, -1, -1}
	for i, name := range []string{"cpu", "memory", "io"} {
		data, err := os.ReadFile(filepath.Join(pressureRoot, name))
		if err == nil {
			out[i] = parseSomeTotal(string(data))
		}
	}
	return out
}

// readRecent returns the "some" avg10 share (0..1) of cpu, memory and io, -1
// for each resource the kernel does not report.
func readRecent() [3]float64 {
	out := [3]float64{-1, -1, -1}
	for i, name := range []string{"cpu", "memory", "io"} {
		data, err := os.ReadFile(filepath.Join(pressureRoot, name))
		if err == nil {
			if avg := parseSomeField(string(data), "avg10="); avg >= 0 {
				out[i] = avg / 100
			}
		}
	}
	return out
}

// parseSomeTotal reads total= of the "some" line, -1 when absent.
func parseSomeTotal(text string) float64 { return parseSomeField(text, "total=") }

// parseSomeField reads one field of the "some" line, -1 when absent.
func parseSomeField(text, prefix string) float64 {
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "some" {
			continue
		}
		for _, field := range fields[1:] {
			if value, ok := strings.CutPrefix(field, prefix); ok {
				if total, err := strconv.ParseFloat(value, 64); err == nil {
					return total
				}
			}
		}
	}
	return -1
}

// powerState reads the kernel's power supplies. Any online mains adapter
// means wall power. Otherwise a discharging battery means battery power.
func powerState() (known, onBattery bool) {
	entries, err := os.ReadDir(powerRoot)
	if err != nil {
		return false, false
	}
	mains, mainsOnline, discharging := false, false, false
	read := func(dir, name string) string {
		data, _ := os.ReadFile(filepath.Join(powerRoot, dir, name))
		return strings.TrimSpace(string(data))
	}
	for _, entry := range entries {
		switch read(entry.Name(), "type") {
		case "Mains", "USB":
			if read(entry.Name(), "online") == "1" {
				mainsOnline = true
			}
			mains = mains || read(entry.Name(), "type") == "Mains"
		case "Battery":
			if read(entry.Name(), "scope") != "Device" && read(entry.Name(), "status") == "Discharging" {
				discharging = true
			}
		}
	}
	switch {
	case mainsOnline:
		return true, false
	case discharging:
		return true, true
	case mains:
		return true, false // adapter present and offline, battery not discharging (full or absent)
	}
	return false, false
}
