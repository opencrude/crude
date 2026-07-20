package ansi

import (
	"os"
	"testing"
)

// TestMain pins the width profile to Modern for the upstream x/ansi test
// suite. These tests encode grapheme-clustering semantics (flag emoji → 2,
// ZWJ family → 2, etc.) that only hold under WidthProfileModern. Without
// pinning, running the suite inside Windows Terminal (WT_SESSION set)
// would auto-detect WidthProfileWindowsTerminal and fail every clustering
// assertion. WT-specific behaviour is covered by TestWindowsTerminalProfile
// in width_profile_test.go, which sets the profile explicitly.
func TestMain(m *testing.M) {
	prev := activeWidthProfile
	activeWidthProfile = WidthProfileModern
	code := m.Run()
	activeWidthProfile = prev
	os.Exit(code)
}