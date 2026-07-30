package ui

import (
	"testing"

	"crude/tui/theme"
)

func TestTokenSetMetricVersionColorOnlySwitch(t *testing.T) {
	ts := NewTokenSet(theme.DefaultDark())
	before := ts.MetricVersion
	colorBefore := ts.ColorVersion
	ts.Rebuild(theme.DefaultLight())
	if ts.MetricVersion != before {
		t.Fatalf("color-only rebuild bumped metrics %d -> %d", before, ts.MetricVersion)
	}
	if ts.ColorVersion == colorBefore {
		t.Fatal("color rebuild should bump ColorVersion")
	}
	ts.theme.Metrics.ScrollbarWidth = 2
	ts.Rebuild(ts.theme)
	if ts.MetricVersion == before {
		t.Fatal("metric change should bump MetricVersion")
	}
}

func TestTokenSetExposesGlyphSpacingTypography(t *testing.T) {
	ts := NewTokenSet(theme.DefaultHighContrast())
	if ts.Glyphs().Prompt != "❯" {
		t.Fatalf("prompt glyph=%q", ts.Glyphs().Prompt)
	}
	if ts.PadLR() != 2 || ts.Metrics().TitleBarHeight != 1 {
		t.Fatalf("spacing/metrics missing")
	}
	if !ts.Typography().TitleBold {
		t.Fatal("expected title bold typography token")
	}
}
