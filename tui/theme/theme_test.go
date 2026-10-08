package theme

import (
	"image/color"
	"math"
	"reflect"
	"testing"
)

func TestFromPreset(t *testing.T) {
	if got := FromPreset("light").Name; got != "light" {
		t.Fatalf("light preset name=%q", got)
	}
	if got := FromPreset("high-contrast").Name; got != "high-contrast" {
		t.Fatalf("hc preset name=%q", got)
	}
	if got := FromPreset("warm").Name; got != "warm" {
		t.Fatalf("warm preset name=%q", got)
	}
	if got := FromPreset("midnight").Name; got != "midnight" {
		t.Fatalf("midnight preset name=%q", got)
	}
	if got := FromPreset("").Name; got != "dark" {
		t.Fatalf("default preset name=%q", got)
	}
	if got := FromPreset("HC").Name; got != "high-contrast" {
		t.Fatalf("alias preset name=%q", got)
	}
	if got := FromPreset("sepia").Name; got != "warm" {
		t.Fatalf("warm alias preset name=%q", got)
	}
	if got := FromPreset("nord").Name; got != "midnight" {
		t.Fatalf("midnight alias preset name=%q", got)
	}
}

func TestAllPresetsMatchCanonicalNames(t *testing.T) {
	presets := AllPresets()
	if len(presets) != len(AllPresetNames) {
		t.Fatalf("AllPresets len=%d want %d", len(presets), len(AllPresetNames))
	}
	for i, name := range AllPresetNames {
		if presets[i].Name != name {
			t.Fatalf("preset[%d]=%q want %q", i, presets[i].Name, name)
		}
		if FromPreset(name).Name != name {
			t.Fatalf("FromPreset(%q)=%q", name, FromPreset(name).Name)
		}
	}
}

func TestReferenceThemesShareMetricsAndGlyphs(t *testing.T) {
	for _, th := range AllPresets() {
		if th.Metrics != DefaultMetrics() {
			t.Fatalf("%s metrics diverged: %#v", th.Name, th.Metrics)
		}
		if th.Glyphs != DefaultGlyphs() {
			t.Fatalf("%s glyphs diverged: %#v", th.Name, th.Glyphs)
		}
		if th.Type != DefaultTypography() {
			t.Fatalf("%s typography diverged: %#v", th.Name, th.Type)
		}
		if th.Spacing.PadLR != 2 || th.Spacing.ButtonGap != 2 {
			t.Fatalf("%s spacing diverged: %#v", th.Name, th.Spacing)
		}
	}
}

func TestHighContrastDiffersFromDarkAndLight(t *testing.T) {
	hc := DefaultHighContrast()
	dark := DefaultDark()
	light := DefaultLight()
	if PresetName(hc) != "high-contrast" {
		t.Fatalf("preset=%q", PresetName(hc))
	}
	if hc.Colors.Bg == dark.Colors.Bg {
		t.Fatal("hc bg should differ from dark")
	}
	if hc.Colors.Bg == light.Colors.Bg {
		t.Fatal("hc bg should differ from light")
	}
}

func TestWarmAndMidnightDifferFromExisting(t *testing.T) {
	warm := DefaultWarm()
	midnight := DefaultMidnight()
	dark := DefaultDark()
	if warm.Colors.Bg == dark.Colors.Bg {
		t.Fatal("warm bg should differ from dark")
	}
	if midnight.Colors.Bg == dark.Colors.Bg {
		t.Fatal("midnight bg should differ from dark")
	}
	if warm.Colors.Bg == midnight.Colors.Bg {
		t.Fatal("warm and midnight backgrounds should differ")
	}
	if warm.Colors.Accent == midnight.Colors.Accent {
		t.Fatal("warm and midnight accents should differ")
	}
}

// TestAllPresetsHaveCompleteTokens walks every required Colors field (including
// nested Code/Spinner/Diff/Tree) and fails if any is nil or fully transparent
// zero — which would paint as black-on-black in the terminal.
func TestAllPresetsHaveCompleteTokens(t *testing.T) {
	for _, th := range AllPresets() {
		t.Run(th.Name, func(t *testing.T) {
			assertColorsComplete(t, reflect.ValueOf(th.Colors), "Colors")
		})
	}
}

func assertColorsComplete(t *testing.T, v reflect.Value, path string) {
	t.Helper()
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			field := v.Type().Field(i)
			assertColorsComplete(t, v.Field(i), path+"."+field.Name)
		}
	case reflect.Interface:
		if v.IsNil() {
			t.Fatalf("%s is nil", path)
			return
		}
		c, ok := v.Interface().(color.Color)
		if !ok {
			t.Fatalf("%s is not color.Color", path)
			return
		}
		if colorUnset(c) {
			t.Fatalf("%s is unset/zero transparent", path)
		}
	default:
		// Non-color fields should not appear in Colors; ignore.
	}
}

func colorUnset(c color.Color) bool {
	if c == nil {
		return true
	}
	r, g, b, a := c.RGBA()
	return r == 0 && g == 0 && b == 0 && a == 0
}

// Contrast threshold rationale:
//   - Fg/Bg: WCAG 2.1 AA for normal text requires 4.5:1. Terminal body text is
//     dense and small, so we keep that bar.
//   - Accent/Bg: WCAG AA for UI components / large text is 3:1. Accents are
//     emphasis chrome (headers, prompts), not body copy, so 3:1 is the right bar.
const (
	minFgBgContrast     = 4.5
	minAccentBgContrast = 3.0
)

func TestAllPresetsReadableContrast(t *testing.T) {
	for _, th := range AllPresets() {
		t.Run(th.Name, func(t *testing.T) {
			fgBg := contrastRatio(th.Colors.Fg, th.Colors.Bg)
			if fgBg < minFgBgContrast {
				t.Fatalf("Fg/Bg contrast=%.2f want >= %.1f", fgBg, minFgBgContrast)
			}
			accentBg := contrastRatio(th.Colors.Accent, th.Colors.Bg)
			if accentBg < minAccentBgContrast {
				t.Fatalf("Accent/Bg contrast=%.2f want >= %.1f", accentBg, minAccentBgContrast)
			}
		})
	}
}

func contrastRatio(a, b color.Color) float64 {
	l1 := relativeLuminance(a)
	l2 := relativeLuminance(b)
	lighter := math.Max(l1, l2)
	darker := math.Min(l1, l2)
	return (lighter + 0.05) / (darker + 0.05)
}

func relativeLuminance(c color.Color) float64 {
	r16, g16, b16, _ := c.RGBA()
	rf := srgbChannel(float64(r16) / 65535.0)
	gf := srgbChannel(float64(g16) / 65535.0)
	bf := srgbChannel(float64(b16) / 65535.0)
	return 0.2126*rf + 0.7152*gf + 0.0722*bf
}

func srgbChannel(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func TestAllPresetsButtonTokensDistinctFromAccent(t *testing.T) {
	// PopupButtonBg must differ from Accent so unfocused secondary action-bar
	// buttons cannot be confused with keyboard-focused / primary Accent fills.
	for _, th := range AllPresets() {
		if sameRGB(th.Colors.PopupButtonBg, th.Colors.Accent) {
			t.Fatalf("%s PopupButtonBg equals Accent — action-bar focus becomes ambiguous", th.Name)
		}
		if sameRGB(th.Colors.Selection, th.Colors.Accent) {
			t.Fatalf("%s Selection equals Accent — list focus vs selection collapses", th.Name)
		}
	}
}

func TestDarkTextSelectionIsNeutralGrayAndReadable(t *testing.T) {
	th := DefaultDark()
	r, g, b, _ := th.Colors.TextSelection.RGBA()
	if r != g || g != b {
		t.Fatalf("dark text-selection fill must be neutral gray, got RGB %d,%d,%d", r, g, b)
	}
	for _, background := range []color.Color{th.Colors.Bg, th.Colors.PopupBg, th.Colors.SubPaneBg} {
		if ratio := contrastRatio(th.Colors.TextSelection, background); ratio < 3 {
			t.Fatalf("gray selection must remain distinct from its surface: %.2f:1", ratio)
		}
	}
	if ratio := contrastRatio(th.Colors.TextSelectionFg, th.Colors.TextSelection); ratio < 4.5 {
		t.Fatalf("gray selection ink must remain readable: %.2f:1", ratio)
	}
}

func sameRGB(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == b
	}
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	return ar == br && ag == bg && ab == bb
}
