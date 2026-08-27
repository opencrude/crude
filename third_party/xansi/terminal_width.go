package ansi

import (
	"sync/atomic"

	"github.com/mattn/go-runewidth"
)

// TerminalWidthProfile contains measurements from the effective terminal grid.
// The effective grid may be a multiplexer rather than the outer emulator.
// Values are immutable after publication so width measurement stays lock-free.
type TerminalWidthProfile struct {
	Exact                  map[string]int
	AmbiguousWidth         int
	LegacyGraphemeClusters bool
}

type terminalWidthProfile struct {
	exact                  map[string]int
	ambiguousWidth         int
	legacyGraphemeClusters bool
}

var activeTerminalWidthProfile atomic.Pointer[terminalWidthProfile]

// SetTerminalWidthProfile atomically replaces runtime terminal corrections.
// Invalid measurements are discarded. Passing a zero profile clears them.
func SetTerminalWidthProfile(profile TerminalWidthProfile) {
	exact := make(map[string]int, len(profile.Exact))
	for cluster, width := range profile.Exact {
		if cluster != "" && width >= 0 && width <= 16 {
			exact[cluster] = width
		}
	}
	ambiguousWidth := profile.AmbiguousWidth
	if ambiguousWidth != 1 && ambiguousWidth != 2 {
		ambiguousWidth = 0
	}
	if len(exact) == 0 && ambiguousWidth == 0 && !profile.LegacyGraphemeClusters {
		activeTerminalWidthProfile.Store(nil)
		return
	}
	activeTerminalWidthProfile.Store(&terminalWidthProfile{
		exact:                  exact,
		ambiguousWidth:         ambiguousWidth,
		legacyGraphemeClusters: profile.LegacyGraphemeClusters,
	})
}

func terminalExactWidth(cluster string) (int, bool) {
	profile := activeTerminalWidthProfile.Load()
	if profile == nil {
		return 0, false
	}
	width, ok := profile.exact[cluster]
	return width, ok
}

func terminalAmbiguousWidth(base rune) (int, bool) {
	profile := activeTerminalWidthProfile.Load()
	if profile == nil || profile.ambiguousWidth == 0 || !runewidth.IsAmbiguousWidth(base) {
		return 0, false
	}
	return profile.ambiguousWidth, true
}

func terminalUsesLegacyGraphemes() bool {
	profile := activeTerminalWidthProfile.Load()
	return profile != nil && profile.legacyGraphemeClusters
}
