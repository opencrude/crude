//go:build !linux && !windows

package sysload

// Other systems have no pressure interface; Sample measures idle cores.
// Power state is not read here, so battery power does not pause work.
func readStalls() [3]float64 { return [3]float64{-1, -1, -1} }

func readRecent() [3]float64 { return [3]float64{-1, -1, -1} }

func powerState() (known, onBattery bool) { return false, false }
