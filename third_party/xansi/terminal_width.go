package ansi

import (
	"sync"
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
	generation             uint64
	exact                  map[string]int
	ambiguousWidth         int
	legacyGraphemeClusters bool
}

var activeTerminalWidthProfile atomic.Pointer[terminalWidthProfile]
var terminalWidthPublishMu sync.Mutex

// TerminalWidthGeneration identifies the exact published width profile. A
// zero value is the initial Unicode guess. Measurement caches use this value
// with their source and geometry keys, without copying profile data.
func TerminalWidthGeneration() uint64 {
	if profile := activeTerminalWidthProfile.Load(); profile != nil {
		return profile.generation
	}
	return 0
}

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
	// Publish the profile and its generation together. Empty corrections are
	// still a new profile: caches must not reuse measurements from before it.
	terminalWidthPublishMu.Lock()
	generation := TerminalWidthGeneration() + 1
	activeTerminalWidthProfile.Store(&terminalWidthProfile{
		generation:             generation,
		exact:                  exact,
		ambiguousWidth:         ambiguousWidth,
		legacyGraphemeClusters: profile.LegacyGraphemeClusters,
	})
	terminalWidthPublishMu.Unlock()
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
