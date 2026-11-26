//go:build !linux && !windows && (!darwin || !cgo)

package sysload

func powerState() (known, onBattery bool) { return false, false }
