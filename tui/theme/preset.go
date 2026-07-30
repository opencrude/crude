package theme

import "strings"

// AllPresetNames is the canonical ordered list of reference theme profiles.
// Settings, FromPreset aliases, and gallery tests must stay aligned with this.
var AllPresetNames = []string{
	"dark",
	"light",
	"high-contrast",
	"warm",
	"midnight",
}

// AllPresets returns a fresh slice of every reference theme built through
// the shared newTheme factory.
func AllPresets() []*Theme {
	return []*Theme{
		DefaultDark(),
		DefaultLight(),
		DefaultHighContrast(),
		DefaultWarm(),
		DefaultMidnight(),
	}
}

// FromPreset returns the reference theme for a named preset.
// Unknown or empty names fall back to dark.
func FromPreset(name string) *Theme {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "light":
		return DefaultLight()
	case "high-contrast", "highcontrast", "hc":
		return DefaultHighContrast()
	case "warm", "sepia", "ember":
		return DefaultWarm()
	case "midnight", "deep", "nord":
		return DefaultMidnight()
	default:
		return DefaultDark()
	}
}

// PresetName returns the canonical preset id for th, or "dark" if unknown.
func PresetName(th *Theme) string {
	if th == nil || th.Name == "" {
		return "dark"
	}
	return th.Name
}
