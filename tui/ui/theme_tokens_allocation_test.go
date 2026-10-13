package ui

import (
	"testing"

	"crude/tui/theme"
	"crude/types/control"
)

var measuredGlyphs theme.Glyphs

func TestGlyphTokensLookupDoesNotAllocate(t *testing.T) {
	th := theme.DefaultDark()
	th.Glyphs.SwitchOn, th.Glyphs.SwitchOff, th.Glyphs.CycleLeft, th.Glyphs.FocusEdge = "", "", "", ""
	tokens := NewTokenSet(th)
	if allocations := testing.AllocsPerRun(100, func() { measuredGlyphs = tokens.Glyphs() }); allocations != 0 {
		t.Fatalf("per-paint glyph lookup allocates: %g", allocations)
	}
	defaults := theme.DefaultGlyphs()
	if measuredGlyphs.SwitchOn != defaults.SwitchOn || measuredGlyphs.SwitchOff != defaults.SwitchOff || measuredGlyphs.CycleLeft != defaults.CycleLeft || measuredGlyphs.FocusEdge != defaults.FocusEdge {
		t.Fatal("missing glyph defaults were lost")
	}
	th.Glyphs.CycleLeft = "previous"
	if tokens.Glyphs().CycleLeft != "previous" {
		t.Fatal("current theme override was hidden")
	}
}

func TestDisabledControlDescriptorReportsKnownUnavailableReason(t *testing.T) {
	button := NewButton("disabled", "Unavailable action", "owner.action")
	button.SetDisabled(true)
	descriptors := button.ControlActions()
	if len(descriptors) != 1 || descriptors[0].Availability.Available || descriptors[0].Availability.Reason != control.TargetUnavailable || descriptors[0].Availability.Validate() != nil {
		t.Fatalf("disabled inspection has malformed availability: %+v", descriptors)
	}
}
