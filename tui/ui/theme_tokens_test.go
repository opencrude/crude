package ui

import (
	"testing"

	"crude/tui/cell"
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

func TestActivityTextStaysBetweenMutedAndPrimaryInk(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight(), theme.DefaultMidnight(), theme.DefaultWarm(), theme.DefaultHighContrast()} {
		t.Run(th.Name, func(t *testing.T) {
			tokens := NewTokenSet(th)
			bg := tokens.Color(ColorBg)
			muted := cell.ContrastRatio(tokens.Color(ColorMuted), bg)
			activity := cell.ContrastRatio(tokens.Color(ColorActivityText), bg)
			primary := cell.ContrastRatio(tokens.Color(ColorFg), bg)
			if activity <= muted || activity >= primary {
				t.Fatalf("activity contrast %.2f must sit between muted %.2f and primary %.2f", activity, muted, primary)
			}
		})
	}
}

func TestTokenSetExposesGlyphSpacingTypography(t *testing.T) {
	ts := NewTokenSet(theme.DefaultHighContrast())
	if ts.Glyphs().Prompt != "" {
		t.Fatalf("prompt glyph must stay empty, got %q", ts.Glyphs().Prompt)
	}
	if ts.PadLR() != 2 || ts.Metrics().TitleBarHeight != 1 {
		t.Fatalf("spacing/metrics missing")
	}
	if !ts.Typography().TitleBold {
		t.Fatal("expected title bold typography token")
	}
}
