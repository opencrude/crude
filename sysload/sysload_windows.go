package sysload

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var getSystemPowerStatus = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetSystemPowerStatus")

// systemPowerStatus is SYSTEM_POWER_STATUS (winbase.h).
type systemPowerStatus struct {
	ACLineStatus        byte
	BatteryFlag         byte
	BatteryLifePercent  byte
	SystemStatusFlag    byte
	BatteryLifeTime     uint32
	BatteryFullLifeTime uint32
}

// Windows has no pressure interface; Sample measures idle cores instead.
func readStalls() [3]float64 { return [3]float64{-1, -1, -1} }

func readRecent() [3]float64 { return [3]float64{-1, -1, -1} }

// powerState: ACLineStatus 0 is battery power; SystemStatusFlag 1 is the
// battery saver, which also asks background work to wait.
func powerState() (known, onBattery bool) {
	var status systemPowerStatus
	if r, _, _ := getSystemPowerStatus.Call(uintptr(unsafe.Pointer(&status))); r == 0 {
		return false, false
	}
	switch {
	case status.SystemStatusFlag == 1:
		return true, true
	case status.ACLineStatus == 0:
		return true, true
	case status.ACLineStatus == 1:
		return true, false
	}
	return false, false
}
