//go:build cgo

package sysload

import (
	"os/exec"
	"strings"
	"testing"
)

func TestSampleReadsMacPowerSource(t *testing.T) {
	current := func() (bool, bool) {
		out, err := exec.CommandContext(t.Context(), "/usr/bin/pmset", "-g", "batt").Output()
		if err != nil {
			t.Fatal(err)
		}
		first, _, _ := strings.Cut(string(out), "\n")
		switch first {
		case "Now drawing from 'AC Power'":
			return true, false
		case "Now drawing from 'Battery Power'":
			return true, true
		default:
			return false, false
		}
	}
	known, battery := current()
	sample := NewProbe().Sample()
	if afterKnown, afterBattery := current(); !known || known != afterKnown || battery != afterBattery {
		t.Skip("AC or battery power not stable during the sample")
	}
	if !sample.PowerKnown || sample.OnBattery != battery {
		t.Fatalf("power sample: known=%t battery=%t; pmset: battery=%t", sample.PowerKnown, sample.OnBattery, battery)
	}
}
