//go:build !linux && !windows

package sysload

// Other systems have no pressure interface; Sample measures idle cores.
func readStalls() [3]float64 { return [3]float64{-1, -1, -1} }

func readRecent() [3]float64 { return [3]float64{-1, -1, -1} }
