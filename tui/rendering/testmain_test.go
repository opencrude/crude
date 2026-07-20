package rendering

import (
	"os"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestMain pins the width profile to Modern for the existing rendering
// tests, which encode grapheme-clustering semantics (emoji cluster → 2,
// ZWJ family → 2, flag → 2, trigram → 1, never-exceeds-2). Without
// pinning, running inside Windows Terminal (WT_SESSION set) would
// auto-detect the Windows Terminal per-codepoint profile and these
// assertions would fail. Windows Terminal–specific behaviour is covered
// by TestWindowsTerminalProfile in width_wt_test.go, which sets the
// profile explicitly. There is no branch in production code — x/ansi is
// the single decision point; this pin only makes the test suite hermetic.
//
// We do NOT touch a WidthMethod variable (there is none anymore): the
// width functions read ansi.ActiveMethod() live, which honors the pin.
func TestMain(m *testing.M) {
	prev := ansi.SetWidthProfile(ansi.WidthProfileModern)
	code := m.Run()
	ansi.SetWidthProfile(prev)
	os.Exit(code)
}